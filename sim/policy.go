package sim

import (
	"math"
	"sort"
	"time"

	"github.com/Rakshit-gen/bidsieve/shape"
)

// Truth is what a policy would know if it could see the future. Only the
// oracle and clairvoyant reference policies read it.
type Truth struct {
	P, Mu float64 // the buyer's real bid chance and expected price
	Price float64 // what it will actually bid, 0 for no bid
	Seen  bool    // the buyer already received this impression
}

// Policy is anything that decides which buyers get a request.
type Policy interface {
	Name() string
	Decide(r *shape.Request, at time.Time, truth []Truth, out []shape.Decision) []shape.Decision
	Observe(o shape.Outcome)
}

// Bidsieve adapts a shape.Shaper.
type Bidsieve struct {
	Label string
	S     *shape.Shaper
}

func (b *Bidsieve) Name() string { return b.Label }
func (b *Bidsieve) Decide(r *shape.Request, at time.Time, _ []Truth, out []shape.Decision) []shape.Decision {
	return b.S.Decide(r, at, out)
}
func (b *Bidsieve) Observe(o shape.Outcome) { b.S.Observe(o) }

// NewBidsieve builds the shaper for a world with the default settings,
// changed by mod if it isn't nil.
func NewBidsieve(w *World, label string, mod func(*shape.Config)) *Bidsieve {
	cfg := shape.DefaultConfig()
	cfg.Buyers = w.Buyers()
	cfg.Seed = w.sc.Seed
	if mod != nil {
		mod(&cfg)
	}
	s, err := shape.New(cfg)
	if err != nil {
		panic(err)
	}
	return &Bidsieve{Label: label, S: s}
}

// Buyers lists the world's buyers with their caps.
func (w *World) Buyers() []shape.Buyer {
	out := make([]shape.Buyer, len(w.buyers))
	for i, b := range w.buyers {
		out[i] = shape.Buyer{Name: b.name, QPS: b.cap}
	}
	return out
}

func pacers(w *World) []*shape.Pacer {
	ps := make([]*shape.Pacer, len(w.buyers))
	for i, b := range w.buyers {
		ps[i] = shape.NewPacer(b.cap, shape.DefaultConfig().Headroom)
	}
	return ps
}

func rollAll(ps []*shape.Pacer, at time.Time) {
	sec := at.Unix()
	for _, p := range ps {
		p.Roll(sec)
	}
}

// take finishes a decision once the policy wants to send.
func take(p *shape.Pacer, d *shape.Decision) {
	if p.Take() {
		d.Send, d.Reason = true, shape.Sent
	} else {
		d.Reason = shape.OverCap
	}
}

// Random sends each request with the same probability, sized so the
// expected volume fits the cap. It's what a shaper with no model does.
type Random struct {
	ps    []*shape.Pacer
	rate  float64 // requests per second, smoothed
	count float64
	sec   int64
	seed  uint64
}

func NewRandom(w *World) *Random { return &Random{ps: pacers(w), sec: -1, seed: w.sc.Seed} }

func (p *Random) Name() string { return "random" }
func (p *Random) Decide(r *shape.Request, at time.Time, _ []Truth, out []shape.Decision) []shape.Decision {
	rollAll(p.ps, at)
	if s := at.Unix(); s != p.sec {
		if p.sec >= 0 {
			p.rate = 0.7*p.rate + 0.3*p.count
			if p.rate == 0 {
				p.rate = p.count
			}
		}
		p.sec, p.count = s, 0
	}
	p.count++
	out = out[:0]
	for b, pc := range p.ps {
		q := 1.0
		if p.rate > 0 {
			q = math.Min(1, float64(pc.Cap())*0.95/p.rate)
		}
		d := shape.Decision{Buyer: b, Prop: q, Reason: shape.Filtered}
		if shape.Unit(shape.Mix2(r.ID^p.seed, uint64(b)+1)) < q {
			take(pc, &d)
		}
		out = append(out, d)
	}
	return out
}
func (p *Random) Observe(shape.Outcome) {}

// History is the common production approach, modelled on the "low value"
// tuple lists in Amazon's Dynamic Traffic Engine. Every Refresh seconds it
// ranks full tuples by bid value per request over the last Window refreshes
// and blocks the worst ones until the expected volume fits the cap. Tuples
// with fewer than MinSent sends are left unblocked. Control is the share of
// each buyer's cap set aside for blocked traffic, sampled at random, so
// learning never stops (DTE's learning=1 group); zero turns it off.
type History struct {
	Label   string
	Key     func(shape.Features) uint64
	Refresh int
	Window  int
	MinSent int
	Control float64

	ps       []*shape.Pacer
	seed     uint64
	blocked  []map[uint64]bool
	allowed  []map[uint64]bool         // tuples with enough evidence to be kept
	blockNew []bool                    // whether tuples without evidence are blocked too
	controlQ []float64                 // chance a blocked request still goes, per buyer
	periods  [][]map[uint64]*tupleStat // per buyer, ring of refresh periods
	cur      int
	filled   int // refresh periods with data, up to Window
	last     int64
}

type tupleStat struct {
	seen, sent float64
	value      float64
}

func NewHistory(w *World, label string, key func(shape.Features) uint64, refresh, window int, control float64) *History {
	h := &History{Label: label, Key: key, Refresh: refresh, Window: window, MinSent: 30, Control: control,
		ps: pacers(w), seed: w.sc.Seed, last: -1, controlQ: make([]float64, len(w.buyers)),
		blockNew: make([]bool, len(w.buyers))}
	for range w.buyers {
		h.blocked = append(h.blocked, map[uint64]bool{})
		h.allowed = append(h.allowed, map[uint64]bool{})
		ring := make([]map[uint64]*tupleStat, window)
		for i := range ring {
			ring[i] = map[uint64]*tupleStat{}
		}
		h.periods = append(h.periods, ring)
	}
	return h
}

// TupleKey is the full DTE-style tuple, publisher included.
func TupleKey(f shape.Features) uint64 {
	return uint64(f.Publisher) | SegmentKey(f)
}

// SegmentKey drops the publisher. It is dense enough to learn on quickly,
// and blind to differences between publishers.
func SegmentKey(f shape.Features) uint64 {
	return uint64(f.Format)<<24 | uint64(f.Country)<<28 |
		uint64(f.Device)<<34 | uint64(f.Channel)<<37 | uint64(f.Pos)<<38
}

func (h *History) Name() string { return h.Label }

func (h *History) stat(b int, key uint64) *tupleStat {
	m := h.periods[b][h.cur]
	st := m[key]
	if st == nil {
		st = &tupleStat{}
		m[key] = st
	}
	return st
}

func (h *History) Decide(r *shape.Request, at time.Time, _ []Truth, out []shape.Decision) []shape.Decision {
	rollAll(h.ps, at)
	sec := at.Unix() - epoch.Unix()
	if sec/int64(h.Refresh) != h.last {
		if h.last >= 0 {
			h.rebuild()
		}
		h.last = sec / int64(h.Refresh)
	}
	key := h.Key(r.Features)
	out = out[:0]
	for b, pc := range h.ps {
		h.stat(b, key).seen++
		q := 1.0
		if h.blocked[b][key] || (h.blockNew[b] && !h.allowed[b][key]) {
			q = h.controlQ[b]
		}
		d := shape.Decision{Buyer: b, Prop: q, Reason: shape.Filtered}
		if q > 0 && shape.Unit(shape.Mix2(r.ID^h.seed, uint64(b)+1)) < q {
			take(pc, &d)
			if q < 1 {
				d.Reason = shape.Explored
			}
		}
		out = append(out, d)
	}
	return out
}

func (h *History) Observe(o shape.Outcome) {
	st := h.stat(o.Buyer, h.Key(o.Features))
	st.sent++
	if o.Bid {
		st.value += o.Price
	}
}

func (h *History) rebuild() {
	h.filled = min(h.filled+1, h.Window)
	seconds := float64(h.Refresh * h.filled)
	for b, pc := range h.ps {
		agg := map[uint64]*tupleStat{}
		for _, m := range h.periods[b] {
			for k, st := range m {
				a := agg[k]
				if a == nil {
					a = &tupleStat{}
					agg[k] = a
				}
				a.seen += st.seen
				a.sent += st.sent
				a.value += st.value
			}
		}
		type ranked struct {
			key  uint64
			vpr  float64
			rate float64
		}
		var known []ranked
		var volume, blockedRate float64
		var pooled tupleStat // tuples too thin to judge alone, judged together
		blocked := map[uint64]bool{}
		for k, a := range agg {
			rate := a.seen / seconds
			switch {
			case a.sent >= float64(h.MinSent):
				known = append(known, ranked{k, a.value / a.sent, rate})
				volume += rate
			case h.blocked[b][k]:
				// Blocked last time and no fresh evidence since: it stays
				// blocked. This is where starvation comes from, and also
				// what keeps the list from flapping.
				blocked[k] = true
				blockedRate += rate
			default:
				pooled.seen += rate
				pooled.sent += a.sent
				pooled.value += a.value
			}
		}
		const newKey = ^uint64(0)
		volume += pooled.seen
		if pooled.sent >= float64(h.MinSent) {
			known = append(known, ranked{newKey, pooled.value / pooled.sent, pooled.seen})
		}
		// Block from the bottom up until what's left fits.
		sort.Slice(known, func(i, j int) bool { return known[i].vpr < known[j].vpr })
		budget := float64(pc.Cap()) * 0.95 * (1 - h.Control)
		blockNew := false
		allowed := map[uint64]bool{}
		for _, t := range known {
			if volume <= budget {
				if t.key != newKey {
					allowed[t.key] = true
				}
				continue
			}
			if t.key == newKey {
				blockNew = true
			} else {
				blocked[t.key] = true
			}
			volume -= t.rate
			blockedRate += t.rate
		}
		h.blocked[b], h.allowed[b], h.blockNew[b] = blocked, allowed, blockNew
		h.controlQ[b] = 0
		if blockedRate > 0 {
			h.controlQ[b] = math.Min(1, h.Control*float64(pc.Cap())*0.95/blockedRate)
		}
	}
	h.cur = (h.cur + 1) % h.Window
	for b := range h.ps {
		h.periods[b][h.cur] = map[uint64]*tupleStat{}
	}
}

// Oracle knows each buyer's true expected bid value and spends the cap on
// the best of it. No real shaper can do better on average without seeing
// the future, which makes it the yardstick.
type Oracle struct{ ps []*shape.Pacer }

func NewOracle(w *World) *Oracle { return &Oracle{ps: pacers(w)} }

func (p *Oracle) Name() string { return "oracle" }
func (p *Oracle) Decide(r *shape.Request, at time.Time, truth []Truth, out []shape.Decision) []shape.Decision {
	rollAll(p.ps, at)
	out = out[:0]
	for b, pc := range p.ps {
		t := truth[b]
		d := shape.Decision{Buyer: b, Reason: shape.Filtered, Value: t.P * t.Mu}
		switch {
		case t.Seen:
			d.Reason = shape.Duplicate
		case d.Value >= pc.Lambda():
			d.Prop = 1
			take(pc, &d)
		}
		out = append(out, d)
	}
	return out
}
func (p *Oracle) Observe(shape.Outcome) {}

// Clairvoyant knows the actual bids before sending. It's the ceiling for
// what any shaper could capture under the caps.
type Clairvoyant struct{ ps []*shape.Pacer }

func NewClairvoyant(w *World) *Clairvoyant { return &Clairvoyant{ps: pacers(w)} }

func (p *Clairvoyant) Name() string { return "clairvoyant" }
func (p *Clairvoyant) Decide(r *shape.Request, at time.Time, truth []Truth, out []shape.Decision) []shape.Decision {
	rollAll(p.ps, at)
	out = out[:0]
	for b, pc := range p.ps {
		t := truth[b]
		d := shape.Decision{Buyer: b, Reason: shape.Filtered, Value: t.Price}
		switch {
		case t.Seen:
			d.Reason = shape.Duplicate
		case t.Price > 0 && t.Price >= pc.Lambda():
			d.Prop = 1
			take(pc, &d)
		}
		out = append(out, d)
	}
	return out
}
func (p *Clairvoyant) Observe(shape.Outcome) {}

// Policies returns every policy the main comparison runs, in table order.
func Policies(w *World) []Policy {
	return []Policy{
		NewClairvoyant(w),
		NewOracle(w),
		NewBidsieve(w, "bidsieve", nil),
		NewHistory(w, "segment history+control", SegmentKey, 60, 5, 0.05),
		NewHistory(w, "segment history", SegmentKey, 60, 5, 0),
		NewHistory(w, "tuple history+control", TupleKey, 300, 3, 0.05),
		NewHistory(w, "tuple history", TupleKey, 300, 3, 0),
		NewRandom(w),
	}
}

// Ablations returns bidsieve with one idea at a time switched off.
func Ablations(w *World) []Policy {
	return []Policy{
		NewBidsieve(w, "bidsieve", nil),
		NewBidsieve(w, "no fading", func(c *shape.Config) { c.HalfLife = -1 }),
		NewBidsieve(w, "no backoff", func(c *shape.Config) { c.Ablation.NoBackoff = true }),
		NewBidsieve(w, "no IPS weights", func(c *shape.Config) { c.Ablation.NoIPSWeights = true }),
		NewBidsieve(w, "fixed 5% explore", func(c *shape.Config) { c.Ablation.FixedExplore = 0.05 }),
		NewBidsieve(w, "no dedupe", func(c *shape.Config) { c.DedupeWindow = 0 }),
	}
}

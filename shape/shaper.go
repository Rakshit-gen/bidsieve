package shape

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Buyer is one demand partner and the requests per second it accepts.
type Buyer struct {
	Name string
	QPS  int
}

// Config tunes a Shaper. Zero fields take the defaults from DefaultConfig.
type Config struct {
	Buyers []Buyer

	// HalfLife is how long it takes evidence to lose half its weight.
	// Shorter reacts faster to buyers changing their minds and costs more
	// exploration. Negative turns fading off.
	HalfLife time.Duration

	// PriorStrength is how many requests' worth of trust a level gives the
	// level above it.
	PriorStrength float64

	// PriorBidRate and PriorPrice are the starting guess for a buyer the
	// shaper knows nothing about.
	PriorBidRate float64
	PriorPrice   float64

	// MinExplore is the lowest chance any request has of being sent.
	MinExplore float64

	// MaxWeight caps the inverse-propensity weight on coarse levels.
	MaxWeight float64

	// Headroom is the share of each buyer's cap the pacer aims for.
	Headroom float64

	// DedupeWindow is how long a fingerprint match counts as the same
	// impression. Zero turns duplicate detection off.
	DedupeWindow time.Duration
	// TIDWindow is the same for shared transaction IDs. It can be longer,
	// because a tid match is exact.
	TIDWindow time.Duration

	// Seed makes the send draws reproducible.
	Seed uint64

	// Ablation switches parts of the design off, to measure what each one
	// is worth. Leave it zero in production.
	Ablation Ablation
}

// Ablation turns individual ideas off for comparison runs.
type Ablation struct {
	NoBackoff    bool    // estimate from the full tuple and the buyer total only
	NoIPSWeights bool    // count every sample once on coarse levels
	FixedExplore float64 // send by a hard threshold plus this much random traffic
}

// DefaultConfig returns the settings the benchmarks use.
func DefaultConfig() Config {
	return Config{
		HalfLife:      2 * time.Minute,
		PriorStrength: 20,
		PriorBidRate:  0.02,
		PriorPrice:    1,
		MinExplore:    0.002,
		MaxWeight:     10,
		Headroom:      0.95,
		DedupeWindow:  300 * time.Millisecond,
		TIDWindow:     10 * time.Second,
	}
}

// Request is one impression opportunity as it reaches the exchange.
type Request struct {
	// ID must differ between requests, including between copies of the same
	// impression. It seeds the send draw, so the same ID always gets the
	// same decision for the same model state.
	ID       uint64
	Features Features
	Dupe     DupeKey
}

// Reason says why a request did or didn't go to a buyer.
type Reason uint8

const (
	Sent      Reason = iota // the model expected a bid worth the price
	Explored                // sent to learn: the model expected it to fall short
	Filtered                // not sent: below the buyer's price
	OverCap                 // wanted to go, but the second's cap was used up
	Duplicate               // this buyer already got the same impression
	numReasons
)

var reasonNames = [...]string{"sent", "explored", "filtered", "over_cap", "duplicate"}

func (r Reason) String() string {
	if int(r) < len(reasonNames) {
		return reasonNames[r]
	}
	return fmt.Sprintf("reason(%d)", r)
}

// Decision is the shaper's answer for one buyer.
type Decision struct {
	Buyer  int
	Send   bool
	Reason Reason
	// Prop is the probability this request had of being sent. Log it next
	// to the outcome: it's what offline evaluation needs.
	Prop float64
	// Value is the expected bid value the model gave the request.
	Value float64
}

// Outcome is what came back from a buyer for a request that was sent.
type Outcome struct {
	Buyer    int
	Features Features
	Prop     float64 // Decision.Prop for this request
	Bid      bool
	Price    float64 // CPM, when Bid is true
	At       time.Time
}

// Shaper decides, per request and per buyer, whether to forward the request.
// It is safe for concurrent use.
type Shaper struct {
	cfg    Config
	tab    *table
	pacers []*Pacer
	dd     *dedupe
	stats  []buyerStats
	sweepN atomic.Int64
}

// New builds a Shaper. It returns an error if the config can't work.
func New(cfg Config) (*Shaper, error) {
	def := DefaultConfig()
	if len(cfg.Buyers) == 0 {
		return nil, errors.New("shape: no buyers")
	}
	if len(cfg.Buyers) > 64 {
		return nil, errors.New("shape: at most 64 buyers")
	}
	for i, b := range cfg.Buyers {
		if b.QPS <= 0 {
			return nil, fmt.Errorf("shape: buyer %d (%s) needs a positive QPS", i, b.Name)
		}
	}
	if cfg.HalfLife == 0 {
		cfg.HalfLife = def.HalfLife
	}
	if cfg.PriorStrength <= 0 {
		cfg.PriorStrength = def.PriorStrength
	}
	if cfg.PriorBidRate <= 0 {
		cfg.PriorBidRate = def.PriorBidRate
	}
	if cfg.PriorPrice <= 0 {
		cfg.PriorPrice = def.PriorPrice
	}
	if cfg.MinExplore <= 0 {
		cfg.MinExplore = def.MinExplore
	}
	if cfg.MaxWeight <= 0 {
		cfg.MaxWeight = def.MaxWeight
	}
	if cfg.Headroom <= 0 || cfg.Headroom > 1 {
		cfg.Headroom = def.Headroom
	}
	if cfg.TIDWindow < cfg.DedupeWindow {
		cfg.TIDWindow = cfg.DedupeWindow
	}
	var k float64
	if cfg.HalfLife > 0 {
		k = math.Ln2 / float64(cfg.HalfLife)
	}
	s := &Shaper{cfg: cfg, tab: newTable(k), stats: make([]buyerStats, len(cfg.Buyers))}
	for _, b := range cfg.Buyers {
		s.pacers = append(s.pacers, NewPacer(b.QPS, cfg.Headroom))
	}
	if cfg.DedupeWindow > 0 {
		s.dd = newDedupe(int64(cfg.DedupeWindow), int64(cfg.TIDWindow))
	}
	for i := range s.stats {
		s.stats[i].segments = map[Features]float64{}
	}
	return s, nil
}

// Buyers returns the configured buyers.
func (s *Shaper) Buyers() []Buyer { return s.cfg.Buyers }

// Lambda returns a buyer's current price per request.
func (s *Shaper) Lambda(buyer int) float64 { return s.pacers[buyer].Lambda() }

// Estimate returns the model's view of a request for one buyer at time now.
func (s *Shaper) Estimate(buyer int, f Features, now time.Time) Estimate {
	return s.estimate(buyer, f.pack(), s.tab.view(now.UnixNano()))
}

// Decide fills out with one decision per buyer and returns it. Pass a slice
// with room for every buyer to avoid allocating.
func (s *Shaper) Decide(r *Request, now time.Time, out []Decision) []Decision {
	ns := now.UnixNano()
	sec := ns / int64(time.Second)
	out = out[:0]
	packed := r.Features.pack()

	var seen uint64
	if s.dd != nil {
		seen = s.dd.seen(r.Dupe, ns)
	}
	var sent uint64
	v := s.tab.view(ns)
	for b, pc := range s.pacers {
		if pc.Roll(sec) && b == 0 {
			s.tab.sweep(int(s.sweepN.Add(1)), ns)
		}
		st := &s.stats[b]
		st.count[0].Add(1) // requests seen
		if seen&(1<<b) != 0 {
			st.count[1+Duplicate].Add(1)
			out = append(out, Decision{Buyer: b, Reason: Duplicate})
			continue
		}
		e := s.estimate(b, packed, v)
		lambda := pc.Lambda()
		q := s.sendProb(e, lambda)
		d := Decision{Buyer: b, Prop: q, Value: e.Value}
		switch u := Unit(Mix2(r.ID^s.cfg.Seed, uint64(b)+1)); {
		case u >= q:
			d.Reason = Filtered
		case !pc.Take():
			d.Reason = OverCap
		default:
			d.Send = true
			d.Reason = Sent
			if e.Value < lambda {
				d.Reason = Explored
			}
			sent |= 1 << b
		}
		st.count[1+d.Reason].Add(1)
		if !d.Send {
			st.withheld(r, d.Value)
		}
		out = append(out, d)
	}
	if s.dd != nil && sent != 0 {
		s.dd.record(r.Dupe, ns, sent)
	}
	return out
}

// Observe feeds back a buyer's response to a request that was sent.
// Responses that never came back (timeouts) should be reported as no bid:
// to the cap and to the publisher, they cost the same.
func (s *Shaper) Observe(o Outcome) {
	if o.Buyer < 0 || o.Buyer >= len(s.pacers) || o.Prop <= 0 {
		return
	}
	v := s.tab.view(o.At.UnixNano())
	up := 1 / v.down
	packed := o.Features.pack()
	// Within a full tuple, which requests went depends only on the tuple and
	// the past, so plain counts are fair. Coarser levels mix tuples that
	// were sent at very different rates; weighting by 1/Prop keeps them an
	// estimate of all the traffic, not just the traffic we liked.
	w := 1.0
	if !s.cfg.Ablation.NoIPSWeights {
		w = math.Min(s.cfg.MaxWeight, 1/o.Prop)
	}
	for l := range numLevels {
		if s.cfg.Ablation.NoBackoff && l != 0 && l != finest {
			continue
		}
		lw := w
		if l == finest {
			lw = 1
		}
		s.tab.add(cellKey(o.Buyer, l, packed), v, up, lw, o.Bid, o.Price)
	}
	if o.Bid {
		s.stats[o.Buyer].bids.Add(1)
	}
}

// Snapshot returns an independent copy of the shaper's model and prices.
// Use it to freeze a policy for offline evaluation.
func (s *Shaper) Snapshot() *Shaper {
	c := &Shaper{cfg: s.cfg, tab: s.tab.clone(), stats: make([]buyerStats, len(s.stats))}
	for i, p := range s.pacers {
		np := NewPacer(int(p.cap), s.cfg.Headroom)
		np.lambda.Store(p.lambda.Load())
		c.pacers = append(c.pacers, np)
		c.stats[i].segments = map[Features]float64{}
	}
	return c
}

// Cells is how many model cells are in memory.
func (s *Shaper) Cells() int { return s.tab.size() }

type buyerStats struct {
	count [1 + numReasons]atomic.Int64
	bids  atomic.Int64

	mu       sync.Mutex
	value    float64 // expected value of everything withheld
	segments map[Features]float64
}

// Segment totals are kept for one request in sampleEvery, so that reporting
// doesn't put a lock on every decision.
const sampleEvery = 16

func (st *buyerStats) withheld(r *Request, v float64) {
	if mix(r.ID)%sampleEvery != 0 {
		return
	}
	st.mu.Lock()
	st.value += v * sampleEvery
	st.segments[SegmentOf(r.Features)] += v * sampleEvery
	st.mu.Unlock()
}

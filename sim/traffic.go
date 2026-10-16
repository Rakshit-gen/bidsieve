package sim

import (
	"container/heap"
	"math"
	"math/rand/v2"

	"github.com/Rakshit-gen/bidsieve/shape"
)

const maxBuyers = 16

type answer struct {
	p, mu, price float64 // price is 0 when the buyer wouldn't bid
}

// impression is one ad opportunity. Its copies all point here, so the
// buyers it reached and the best bid so far are shared between them.
type impression struct {
	id      uint64
	f       shape.Features
	born    float64
	isNew   bool
	drift   bool
	dropped bool
	reload  bool
	truth   [maxBuyers]answer
	reached uint64 // buyers that have received some copy
	best    float64
	ceiling float64 // best bid any buyer would have made
}

// event is one copy of an impression reaching the exchange.
type event struct {
	at  float64
	imp *impression
	req shape.Request
}

type eventHeap []*event

func (h eventHeap) Len() int           { return len(h) }
func (h eventHeap) Less(i, j int) bool { return h[i].at < h[j].at }
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)        { *h = append(*h, x.(*event)) }
func (h *eventHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// stream produces request copies in arrival order, a second at a time.
type stream struct {
	w       *World
	rng     *rand.Rand
	pending eventHeap
	nextImp uint64
	nextReq uint64
	born    func(*impression) // called once per impression, when created
}

func newStream(w *World, born func(*impression)) *stream {
	return &stream{w: w, rng: rand.New(rand.NewPCG(w.sc.Seed, 0x7a11)), born: born}
}

func (s *stream) rate(sec int) float64 {
	sc := &s.w.sc
	r := sc.Rate
	if sc.SpikeLen > 0 && sec >= sc.SpikeAt && sec < sc.SpikeAt+sc.SpikeLen {
		r *= sc.SpikeMult
	}
	return r
}

// second returns the copies arriving in [sec, sec+1), in time order.
func (s *stream) second(sec int, out []*event) []*event {
	n := s.rate(sec)
	n = math.Max(0, math.Round(n+math.Sqrt(n)*s.rng.NormFloat64()))
	for range int(n) {
		s.newImpression(float64(sec) + s.rng.Float64())
	}
	out = out[:0]
	end := float64(sec + 1)
	for s.pending.Len() > 0 && s.pending[0].at < end {
		out = append(out, heap.Pop(&s.pending).(*event))
	}
	return out
}

func (s *stream) pickPublisher(t float64) *publisher {
	sc := &s.w.sc
	if sc.NewPubAt > 0 && t >= float64(sc.NewPubAt) && s.rng.Float64() < sc.NewPubShare {
		return s.w.pubs[sc.Publishers+pick(s.rng, s.w.newCum)]
	}
	return s.w.pubs[pick(s.rng, s.w.oldCum)]
}

func (s *stream) newImpression(t float64) {
	rng := s.rng
	p := s.pickPublisher(t)
	f := shape.Features{Publisher: p.id}
	f.Country = p.home
	if rng.Float64() > 0.65 {
		f.Country = randCountry(rng)
	}
	switch p.kind {
	case webPub:
		f.Channel = shape.Web
		f.Device = []shape.Device{shape.Desktop, shape.Mobile, shape.Tablet}[weighted(rng, []float64{.45, .45, .10})]
	case appPub:
		f.Channel = shape.App
		f.Device = shape.Mobile
		if rng.Float64() < 0.2 {
			f.Device = shape.Tablet
		}
	case ctvPub:
		f.Channel = shape.App
		f.Device = shape.CTV
	}
	si := rng.IntN(len(p.slots))
	sl := p.slots[si]
	if f.Device == shape.Mobile && (sl.format == shape.Banner728x90 || sl.format == shape.Banner160x600) {
		sl.format = shape.Banner320x50
	}
	f.Format, f.Pos = sl.format, sl.pos

	page := uint64(rng.IntN(p.pages))
	user := rng.Uint64()
	fp := shape.Mix2(shape.Mix2(uint64(p.id), page), shape.Mix2(uint64(si), user)) | 1
	s.emit(p, f, fp, t, false)
	if rng.Float64() < s.w.sc.Reloads {
		// Same person, same page, same slot, moments later: a real new
		// impression that looks exactly like a duplicate.
		s.emit(p, f, fp, t+0.2+1.8*rng.Float64(), true)
	}
}

func (s *stream) emit(p *publisher, f shape.Features, fp uint64, t float64, reload bool) {
	s.nextImp++
	imp := &impression{
		id: s.nextImp, f: f, born: t, isNew: p.isNew, reload: reload,
		drift: inDriftSegment(f), dropped: inDroppedSegment(f),
	}
	for b := range s.w.buyers {
		pb, mu := s.w.expect(b, f, p.quality, t)
		imp.truth[b] = answer{p: pb, mu: mu, price: realize(imp.id, b, pb, mu)}
		imp.ceiling = math.Max(imp.ceiling, imp.truth[b].price)
	}
	if s.born != nil {
		s.born(imp)
	}
	copies := 1
	if s.w.sc.Copies {
		copies += p.paths
	}
	shared := s.rng.Uint64() | 1
	for c := range copies {
		at := t
		if c > 0 {
			at += math.Min(0.6, 0.01+s.rng.ExpFloat64()*0.08)
		}
		s.nextReq++
		tid := s.rng.Uint64() | 1 // a tid made up for this path alone
		if s.rng.Float64() < s.w.sc.SharedTID {
			tid = shared
		}
		heap.Push(&s.pending, &event{at: at, imp: imp, req: shape.Request{
			ID:       s.nextReq,
			Features: f,
			Dupe:     shape.DupeKey{Fingerprint: fp, TID: tid},
		}})
	}
}

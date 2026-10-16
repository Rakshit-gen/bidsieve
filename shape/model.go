package shape

import (
	"math"
	"sync"
	"sync/atomic"
)

// cell holds what one buyer did with one group of requests. Every field
// fades with the same half-life, so a cell that stops getting traffic loses
// weight instead of keeping its last opinion forever.
//
// Fading uses forward decay: a sample at time t is stored multiplied by
// e^(k(t-t0)) for a fixed landmark t0, and a read at time now multiplies
// the stored sum by e^(-k(now-t0)). Every cell then fades by the same
// factor, so a request pays for one exponential instead of one per cell,
// and cells don't need to remember when they were last touched.
type cell struct {
	n float64 // requests sent (weighted)
	s float64 // bids received
	v float64 // sum of bid prices
}

const numShards = 64

type shard struct {
	mu sync.RWMutex
	t0 int64 // landmark this shard's cells are scaled to
	m  map[uint64]*cell
	_  [24]byte // keep neighbouring locks off the same cache line
}

type table struct {
	k  float64      // decay rate per nanosecond, 0 for none
	t0 atomic.Int64 // current landmark, 0 until first use
	sh [numShards]shard
}

func newTable(k float64) *table {
	t := &table{k: k}
	for i := range t.sh {
		t.sh[i].m = make(map[uint64]*cell)
	}
	return t
}

func shardOf(key uint64) int { return int(mix(key) & (numShards - 1)) }

// The landmark moves forward once stored values have grown by e^maxGrowth,
// long before they could overflow a float64.
const maxGrowth = 300

// view is the landmark and the factor that turns stored values into
// values at time now. One per request.
type view struct {
	t0   int64
	down float64
	now  int64
}

func (t *table) view(now int64) view {
	if t.k == 0 {
		return view{down: 1, now: now}
	}
	t0 := t.t0.Load()
	if t0 == 0 || t.k*float64(now-t0) > maxGrowth {
		t.t0.CompareAndSwap(t0, now)
		t0 = t.t0.Load()
	}
	return view{t0: t0, down: math.Exp(-t.k * float64(now-t0)), now: now}
}

// rescale moves a shard to landmark t0. Callers hold the write lock.
func (t *table) rescale(sh *shard, t0 int64) {
	if sh.t0 == t0 || t.k == 0 {
		return
	}
	f := math.Exp(-t.k * float64(t0-sh.t0))
	for _, c := range sh.m {
		c.n, c.s, c.v = c.n*f, c.s*f, c.v*f
	}
	sh.t0 = t0
}

func (t *table) get(key uint64, v view) (n, s, val float64) {
	sh := &t.sh[shardOf(key)]
	sh.mu.RLock()
	if c := sh.m[key]; c != nil {
		f := v.down
		if sh.t0 != v.t0 && t.k != 0 {
			f = math.Exp(-t.k * float64(v.now-sh.t0))
		}
		n, s, val = c.n*f, c.s*f, c.v*f
	}
	sh.mu.RUnlock()
	return
}

// add records one sample. up is 1/v.down, passed in so a multi-level update
// computes it once.
func (t *table) add(key uint64, v view, up, w float64, bid bool, price float64) {
	sh := &t.sh[shardOf(key)]
	sh.mu.Lock()
	t.rescale(sh, v.t0)
	c := sh.m[key]
	if c == nil {
		c = &cell{}
		sh.m[key] = c
	}
	w *= up
	c.n += w
	if bid {
		c.s += w
		c.v += w * price
	}
	sh.mu.Unlock()
}

// sweep drops cells in one shard whose weight has faded to nothing.
// Without it, tuples that stop appearing would stay in memory forever.
func (t *table) sweep(i int, now int64) (removed int) {
	if t.k == 0 {
		return 0
	}
	v := t.view(now)
	sh := &t.sh[i%numShards]
	sh.mu.Lock()
	t.rescale(sh, v.t0)
	for key, c := range sh.m {
		if c.n*v.down < 1e-3 {
			delete(sh.m, key)
			removed++
		}
	}
	sh.mu.Unlock()
	return removed
}

func (t *table) size() (n int) {
	for i := range t.sh {
		sh := &t.sh[i]
		sh.mu.RLock()
		n += len(sh.m)
		sh.mu.RUnlock()
	}
	return n
}

func (t *table) clone() *table {
	c := newTable(t.k)
	c.t0.Store(t.t0.Load())
	for i := range t.sh {
		sh := &t.sh[i]
		sh.mu.RLock()
		c.sh[i].t0 = sh.t0
		for k, v := range sh.m {
			cp := *v
			c.sh[i].m[k] = &cp
		}
		sh.mu.RUnlock()
	}
	return c
}

// Estimate is what the shaper believes about one request for one buyer.
type Estimate struct {
	P     float64 // chance the buyer bids
	Price float64 // expected price if it does (CPM)
	Value float64 // P * Price: the expected bid value of sending it
	N     float64 // effective number of requests behind P
	Sigma float64 // standard error of P
}

// estimate walks the levels from coarse to fine. Each level's counts are
// mixed with the level above it, which acts as a prior worth up to
// cfg.PriorStrength requests. The prior can't be worth more than the
// evidence the parent actually has, so when everything has faded the
// estimate is honest about how little it knows.
func (s *Shaper) estimate(dsp int, packed uint64, v view) Estimate {
	cfg := &s.cfg
	// The configured starting guess counts as a single request.
	p, mu, ne := cfg.PriorBidRate, cfg.PriorPrice, 1.0
	for l := range numLevels {
		if cfg.Ablation.NoBackoff && l != 0 && l != finest {
			continue
		}
		n, b, val := s.tab.get(cellKey(dsp, l, packed), v)
		m := math.Min(cfg.PriorStrength, ne)
		if n+m > 0 {
			pseudoBids := m * p
			p = (b + pseudoBids) / (n + m)
			if b+pseudoBids > 0 {
				mu = (val + pseudoBids*mu) / (b + pseudoBids)
			}
		}
		ne = n + m
	}
	pc := math.Min(1-1e-3, math.Max(1e-3, p))
	return Estimate{
		P:     p,
		Price: mu,
		Value: p * mu,
		N:     ne,
		Sigma: math.Sqrt(pc * (1 - pc) / (ne + 1)),
	}
}

// sendProb is the chance a request should go, given the estimate and the
// buyer's current price per request.
//
// It's the probability, under a normal approximation of what we know, that
// the request's true value is above the price. A request we're sure about
// goes (or doesn't) almost every time. A request we're unsure about goes
// some of the time, and how often depends on how close it is to the line.
// Faded evidence widens Sigma, so a segment nobody has looked at in a while
// starts getting sampled again without any schedule telling it to.
//
// The floor keeps every request's chance above zero. That costs very little
// QPS and is what makes the decision logs usable for offline evaluation.
func (s *Shaper) sendProb(e Estimate, lambda float64) float64 {
	cfg := &s.cfg
	if fx := cfg.Ablation.FixedExplore; fx > 0 {
		if e.Value >= lambda {
			return 1
		}
		return fx
	}
	if e.Price <= 0 {
		return cfg.MinExplore
	}
	z := (e.P - lambda/e.Price) / e.Sigma
	switch {
	case z < -5: // Φ(-5) is under 3e-7, far below any useful floor
		return cfg.MinExplore
	case z > 5:
		return 1
	}
	q := 0.5 * math.Erfc(-z/math.Sqrt2)
	return math.Max(cfg.MinExplore, q)
}

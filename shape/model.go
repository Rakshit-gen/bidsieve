package shape

import (
	"math"
	"sync"
)

// cell holds what one buyer did with one group of requests. Every field
// fades with the same half-life, so a cell that stops getting traffic loses
// weight instead of keeping its last opinion forever.
type cell struct {
	n float64 // requests sent (weighted)
	s float64 // bids received
	v float64 // sum of bid prices
	t int64   // unix nanos of the last update
}

func (c *cell) at(now int64, k float64) (n, s, v float64) {
	if k == 0 || now <= c.t {
		return c.n, c.s, c.v
	}
	f := math.Exp(-float64(now-c.t) * k)
	return c.n * f, c.s * f, c.v * f
}

const numShards = 64

type table struct {
	shards [numShards]struct {
		mu sync.RWMutex
		m  map[uint64]*cell
		_  [32]byte // keep neighbouring locks off the same cache line
	}
}

func newTable() *table {
	t := &table{}
	for i := range t.shards {
		t.shards[i].m = make(map[uint64]*cell)
	}
	return t
}

func shardOf(key uint64) int { return int(mix(key) & (numShards - 1)) }

func (t *table) get(key uint64, now int64, k float64) (n, s, v float64) {
	sh := &t.shards[shardOf(key)]
	sh.mu.RLock()
	c := sh.m[key]
	if c != nil {
		n, s, v = c.at(now, k)
	}
	sh.mu.RUnlock()
	return
}

func (t *table) add(key uint64, now int64, k, w float64, bid bool, price float64) {
	sh := &t.shards[shardOf(key)]
	sh.mu.Lock()
	c := sh.m[key]
	if c == nil {
		c = &cell{t: now}
		sh.m[key] = c
	}
	c.n, c.s, c.v = c.at(now, k)
	if now > c.t {
		c.t = now
	}
	c.n += w
	if bid {
		c.s += w
		c.v += w * price
	}
	sh.mu.Unlock()
}

// sweep drops cells in one shard whose weight has faded to nothing.
// Without it, tuples that stop appearing would stay in memory forever.
func (t *table) sweep(shard int, now int64, k float64) (removed int) {
	if k == 0 {
		return 0
	}
	sh := &t.shards[shard%numShards]
	sh.mu.Lock()
	for key, c := range sh.m {
		if n, _, _ := c.at(now, k); n < 1e-3 {
			delete(sh.m, key)
			removed++
		}
	}
	sh.mu.Unlock()
	return removed
}

func (t *table) size() (n int) {
	for i := range t.shards {
		sh := &t.shards[i]
		sh.mu.RLock()
		n += len(sh.m)
		sh.mu.RUnlock()
	}
	return n
}

func (t *table) clone() *table {
	c := newTable()
	for i := range t.shards {
		sh := &t.shards[i]
		sh.mu.RLock()
		for k, v := range sh.m {
			cp := *v
			c.shards[i].m[k] = &cp
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
func (s *Shaper) estimate(dsp int, packed uint64, now int64) Estimate {
	cfg := &s.cfg
	// The configured starting guess counts as a single request.
	p, mu, ne := cfg.PriorBidRate, cfg.PriorPrice, 1.0
	for l := range numLevels {
		if cfg.Ablation.NoBackoff && l != 0 && l != finest {
			continue
		}
		n, b, v := s.tab.get(cellKey(dsp, l, packed), now, s.decay)
		m := math.Min(cfg.PriorStrength, ne)
		if n+m > 0 {
			pseudoBids := m * p
			p = (b + pseudoBids) / (n + m)
			if b+pseudoBids > 0 {
				mu = (v + pseudoBids*mu) / (b + pseudoBids)
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
	q := 0.5 * math.Erfc(-z/math.Sqrt2)
	return math.Max(cfg.MinExplore, q)
}

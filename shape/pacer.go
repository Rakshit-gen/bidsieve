package shape

import (
	"math"
	"sync"
	"sync/atomic"
)

// Pacer holds one buyer's request budget.
//
// It does two jobs. Take is a hard per-second limit, so a buyer never sees
// more than its cap no matter what the scoring does. Lambda is a price per
// request: a request is worth sending only if its expected value beats it.
// Once a second the price moves up when more requests wanted to go than the
// target allows, and down when fewer did. That's the online form of a
// knapsack: fill the cap with the most valuable requests without ever
// sorting the whole stream.
type Pacer struct {
	cap    int64
	target float64
	gain   float64

	lambda atomic.Uint64 // float64 bits
	sec    atomic.Int64
	used   atomic.Int64 // requests let through this second
	demand atomic.Int64 // requests that asked to go this second

	mu sync.Mutex
}

const (
	minLambda = 1e-7
	maxLambda = 1e4
)

// NewPacer makes a pacer for a buyer that accepts qps requests per second
// and aims to use headroom (for example 0.95) of it, leaving the rest for
// bursts within a second.
func NewPacer(qps int, headroom float64) *Pacer {
	p := &Pacer{cap: int64(qps), target: math.Max(1, float64(qps)*headroom), gain: 0.6}
	p.lambda.Store(math.Float64bits(1e-3))
	p.sec.Store(math.MinInt64)
	return p
}

// Cap is the buyer's requests-per-second limit.
func (p *Pacer) Cap() int64 { return p.cap }

// Lambda is the current price of one request, in the same units as the
// scores compared against it.
func (p *Pacer) Lambda() float64 { return math.Float64frombits(p.lambda.Load()) }

// Roll moves the pacer to second sec. The first call in a new second
// reprices using the demand seen in the seconds since the last roll.
// It returns true for the call that did the repricing.
func (p *Pacer) Roll(sec int64) bool {
	if p.sec.Load() >= sec {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.sec.Load()
	if old >= sec {
		return false
	}
	d := p.demand.Swap(0)
	p.used.Store(0)
	if old != math.MinInt64 {
		// If whole seconds went by with no traffic, average over them.
		rate := float64(d) / float64(sec-old)
		ratio := math.Min(4, math.Max(0.25, rate/p.target))
		l := p.Lambda() * math.Pow(ratio, p.gain)
		p.lambda.Store(math.Float64bits(math.Min(maxLambda, math.Max(minLambda, l))))
	}
	p.sec.Store(sec)
	return true
}

// Take records that a request wants to go to this buyer and reports whether
// the second still has room for it.
func (p *Pacer) Take() bool {
	p.demand.Add(1)
	if p.used.Add(1) > p.cap {
		p.used.Add(-1)
		return false
	}
	return true
}

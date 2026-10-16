package ope

import (
	"math"
	"math/rand/v2"
	"testing"
)

// A world where the truth is known: each request has a value, the logger
// sends good-looking requests more often, and the target is a different
// threshold. IPS and DR should land on the truth; the naive estimate
// shouldn't.
func TestEstimatorsRecoverTheTruth(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var e Estimator
	var truth float64
	for range 400_000 {
		quality := rng.Float64()
		p := 0.02 + 0.9*quality // logger favours high quality
		q := 0.0
		if quality > 0.3 { // target sends a wider slice
			q = 1
		}
		var r float64
		if rng.Float64() < 0.1*quality {
			r = 1 + 4*quality
		}
		truth += q * r
		sent := rng.Float64() < p
		reward := 0.0
		if sent {
			reward = r
		}
		e.Add(Sample{P: p, Q: q, Sent: sent, Reward: reward, Predicted: 0.1 * quality * (1 + 4*quality)})
	}
	for name, res := range map[string]Result{"IPS": e.IPS(), "DR": e.DR()} {
		if math.Abs(res.Total-truth) > 3*res.StdErr {
			t.Errorf("%s = %.0f ± %.0f, truth %.0f", name, res.Total, res.StdErr, truth)
		}
	}
	if math.Abs(e.SNIPS()-truth)/truth > 0.02 {
		t.Errorf("SNIPS = %.0f, truth %.0f", e.SNIPS(), truth)
	}
	if e.DR().StdErr >= e.IPS().StdErr {
		t.Errorf("DR with a good model should be tighter: %.1f vs %.1f", e.DR().StdErr, e.IPS().StdErr)
	}
	if e.Naive() < truth*1.05 {
		t.Errorf("naive replay should overstate here: %.0f vs truth %.0f", e.Naive(), truth)
	}
}

func TestUnsupportedTraffic(t *testing.T) {
	var e Estimator
	e.Add(Sample{P: 0, Q: 1})
	e.Add(Sample{P: 1, Q: 1, Sent: true, Reward: 2})
	if got := e.Unsupported(); got != 0.5 {
		t.Fatalf("unsupported = %v, want 0.5", got)
	}
	if e.IPS().Total != 2 {
		t.Fatalf("IPS = %v, want 2: the unsupported request is invisible", e.IPS().Total)
	}
}

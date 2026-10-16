package sim

import (
	"math"
	"testing"
)

func short() Scenario {
	sc := Standard()
	sc.Seconds = 240
	sc.Rate = 400
	sc.Publishers, sc.NewPublishers = 120, 20
	sc.DriftAt, sc.NewPubAt, sc.SpikeAt, sc.SpikeLen = 80, 140, 200, 20
	return sc
}

func TestSameScenarioSameResult(t *testing.T) {
	sc := short()
	builds := map[string]func(*World) Policy{
		"bidsieve": func(w *World) Policy { return NewBidsieve(w, "b", nil) },
		"history":  func(w *World) Policy { return NewHistory(w, "h", SegmentKey, 20, 5, 0.05) },
	}
	for name, build := range builds {
		a := Run(sc, build(NewWorld(sc)), nil)
		for range 3 {
			b := Run(sc, build(NewWorld(sc)), nil)
			if a.Revenue != b.Revenue || a.Sent != b.Sent || a.Requests != b.Requests {
				t.Fatalf("%s: two runs of one scenario differ: %v/%v/%v vs %v/%v/%v",
					name, a.Revenue, a.Sent, a.Requests, b.Revenue, b.Sent, b.Requests)
			}
		}
	}
}

// The comparison is only fair if every policy faces the same traffic and
// the same buyer answers.
func TestEveryPolicySeesTheSameWorld(t *testing.T) {
	sc := short()
	res := Compare(sc, Policies)
	for _, r := range res[1:] {
		if r.Requests != res[0].Requests || r.Impressions != res[0].Impressions || r.Ceiling != res[0].Ceiling {
			t.Fatalf("%s saw a different world than %s", r.Policy, res[0].Policy)
		}
		for b := range r.Buyers {
			if r.Buyers[b].AvailValue != res[0].Buyers[b].AvailValue {
				t.Fatalf("%s: buyer %d would bid differently", r.Policy, b)
			}
		}
	}
}

func TestReferencePoliciesBoundTheRest(t *testing.T) {
	sc := short()
	res := Compare(sc, Policies)
	clair, oracle := find(res, "clairvoyant"), find(res, "oracle")
	for _, r := range res {
		if r.Revenue > clair.Revenue*1.001 {
			t.Errorf("%s beat the clairvoyant: %.0f > %.0f", r.Policy, r.Revenue, clair.Revenue)
		}
		if r.Revenue > r.Ceiling {
			t.Errorf("%s captured more than buyers offered", r.Policy)
		}
	}
	if oracle.Revenue < find(res, "random").Revenue {
		t.Error("the oracle should beat random")
	}
}

func TestCapsAreNeverExceeded(t *testing.T) {
	sc := short()
	for _, r := range Compare(sc, Policies) {
		for _, b := range r.Buyers {
			if max := float64(b.Cap) * float64(sc.Seconds); float64(b.Sent) > max {
				t.Errorf("%s sent %d to %s, over its cap total of %.0f", r.Policy, b.Sent, b.Name, max)
			}
		}
	}
}

func TestRealizedBidsMatchExpectations(t *testing.T) {
	var n, bids int
	var sum float64
	for id := range uint64(200_000) {
		if pr := realize(id, 3, 0.2, 4); pr > 0 {
			bids++
			sum += pr
		}
		n++
	}
	if rate := float64(bids) / float64(n); math.Abs(rate-0.2) > 0.005 {
		t.Fatalf("bid rate %.4f, want 0.2", rate)
	}
	if mean := sum / float64(bids); math.Abs(mean-4)/4 > 0.02 {
		t.Fatalf("mean price %.3f, want 4", mean)
	}
}

func find(res []*Result, name string) *Result {
	for _, r := range res {
		if r.Policy == name {
			return r
		}
	}
	panic(name)
}

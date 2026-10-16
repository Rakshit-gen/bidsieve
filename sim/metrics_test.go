package sim

import (
	"math"
	"testing"
)

// series builds a result whose drift capture is caught/avail per bucket.
func series(caught []int64) (Scenario, *Result) {
	sc := Scenario{DriftAt: 20, Bucket: 10, Seconds: 10 * (2 + len(caught))}
	r := &Result{Series: make([]Bucket, 2+len(caught))}
	for i, c := range caught {
		r.Series[2+i] = Bucket{T: 10 * (2 + i), DriftAvail: 100, DriftCaught: c}
	}
	return sc, r
}

func TestRecoveryCountsFromTheDrift(t *testing.T) {
	sc, r := series([]int64{0, 10, 40, 80, 90, 90})
	// A 30s rolling window first averages 70% over buckets 3-5 (40, 80, 90),
	// which ends 50s after the drift at t=20.
	if got := recovery(sc, r, 0.7); got != 50 {
		t.Fatalf("recovery = %d, want 50", got)
	}
	if got := recovery(sc, r, 0.95); got != -1 {
		t.Fatalf("recovery to an unreached target = %d, want -1", got)
	}
}

func TestDriftCaptureIsPooledNotAveraged(t *testing.T) {
	sc, r := series([]int64{0, 100})
	r.Series[3].DriftAvail = 300
	r.Series[3].DriftCaught = 300
	// (0 + 300) / (100 + 300), not the mean of 0% and 100%.
	if got := driftCapture(sc, r, 20, 40); got != 0.75 {
		t.Fatalf("capture = %g, want 0.75", got)
	}
}

func TestSmoothSkipsGaps(t *testing.T) {
	nan := math.NaN()
	got := Smooth([]float64{1, nan, 3, nan, nan}, 1)
	want := []float64{1, 2, 3, 3, nan}
	for i := range want {
		if math.IsNaN(want[i]) != math.IsNaN(got[i]) || (!math.IsNaN(want[i]) && got[i] != want[i]) {
			t.Fatalf("Smooth = %v, want %v", got, want)
		}
	}
}

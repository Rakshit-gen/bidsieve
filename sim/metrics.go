package sim

import "math"

// Summary boils a Result down to the numbers the comparison tables show.
type Summary struct {
	Policy string

	Revenue    float64 // dollars: best bid per impression, CPM/1000
	RevenuePct float64 // share of the ceiling
	VsOracle   float64 // share of the oracle's revenue, 0 if no oracle given

	BidRate    float64 // bids per sent request
	ValuePer1k float64 // bid value (CPM sum) per 1000 requests sent, in dollars
	CapUse     float64 // sent / (sum of caps x seconds)
	DupShare   float64 // share of sends that went to a buyer that already had the impression
	OverCap    int64
	Explore    float64 // share of sends made to learn

	DriftEarly, DriftMid, DriftLate float64 // capture of the drifted segment's bids
	DriftRecover                    int     // seconds until 80% of the oracle's late capture, -1 if never

	NewFirstMin, NewFirst4Min float64 // capture of new publishers' bids
	DroppedPerSec             float64 // sends to the dropped segment after the campaign ended

	RequestsPerSec float64 // simulated request copies per wall-clock second
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// Summarize derives the table numbers. oracle may be nil.
func Summarize(sc Scenario, r, oracle *Result) Summary {
	var caps float64
	for _, b := range r.Buyers {
		caps += float64(b.Cap)
	}
	s := Summary{
		Policy:         r.Policy,
		Revenue:        r.Revenue / 1000,
		RevenuePct:     ratio(r.Revenue, r.Ceiling),
		BidRate:        ratio(float64(r.Bids), float64(r.Sent)),
		ValuePer1k:     ratio(r.BidValue, float64(r.Sent)),
		CapUse:         ratio(float64(r.Sent), caps*float64(r.Seconds)),
		DupShare:       ratio(float64(r.DupSends), float64(r.Sent)),
		OverCap:        r.OverCap,
		Explore:        ratio(float64(r.Explored), float64(r.Sent)),
		RequestsPerSec: float64(r.Requests) / r.Elapsed.Seconds(),
		DriftRecover:   -1,
	}
	if oracle != nil {
		s.VsOracle = ratio(r.Revenue, oracle.Revenue)
	}
	if d := sc.DriftAt; d > 0 {
		s.DriftEarly = driftCapture(sc, r, d, d+60)
		s.DriftMid = driftCapture(sc, r, d+60, d+240)
		s.DriftLate = driftCapture(sc, r, d+240, d+480)
		var sent int64
		for _, b := range bucketsIn(sc, r, d, d+480) {
			sent += b.DroppedSent
		}
		s.DroppedPerSec = float64(sent) / 480
		if oracle != nil {
			s.DriftRecover = recovery(sc, r, 0.8*driftCapture(sc, oracle, d+240, d+480))
		}
	}
	if n := sc.NewPubAt; n > 0 {
		s.NewFirstMin = newCapture(sc, r, n, n+60)
		s.NewFirst4Min = newCapture(sc, r, n, n+240)
	}
	return s
}

func bucketsIn(sc Scenario, r *Result, from, to int) []Bucket {
	lo, hi := from/sc.Bucket, min(to/sc.Bucket, len(r.Series))
	if lo >= hi {
		return nil
	}
	return r.Series[lo:hi]
}

func driftCapture(sc Scenario, r *Result, from, to int) float64 {
	var a, c int64
	for _, b := range bucketsIn(sc, r, from, to) {
		a += b.DriftAvail
		c += b.DriftCaught
	}
	return ratio(float64(c), float64(a))
}

func newCapture(sc Scenario, r *Result, from, to int) float64 {
	var a, c int64
	for _, b := range bucketsIn(sc, r, from, to) {
		a += b.NewAvail
		c += b.NewCaught
	}
	return ratio(float64(c), float64(a))
}

// recovery is how many seconds after the drift a 30-second rolling capture
// first reaches target.
func recovery(sc Scenario, r *Result, target float64) int {
	start := sc.DriftAt / sc.Bucket
	w := max(1, 30/sc.Bucket)
	for i := start; i+w <= len(r.Series); i++ {
		var a, c int64
		for _, b := range r.Series[i : i+w] {
			a += b.DriftAvail
			c += b.DriftCaught
		}
		if a > 0 && float64(c)/float64(a) >= target {
			return (i+w)*sc.Bucket - sc.DriftAt
		}
	}
	return -1
}

// Smooth is a centred moving average, for charts.
func Smooth(y []float64, k int) []float64 {
	out := make([]float64, len(y))
	for i := range y {
		var s float64
		n := 0
		for j := max(0, i-k); j <= min(len(y)-1, i+k); j++ {
			if !math.IsNaN(y[j]) {
				s += y[j]
				n++
			}
		}
		out[i] = math.NaN()
		if n > 0 {
			out[i] = s / float64(n)
		}
	}
	return out
}

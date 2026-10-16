package shape

import (
	"fmt"
	"io"
	"sort"
)

// BuyerReport is what the shaper did for one buyer since it started. It's
// meant to be shared with the buyer: the requests it never saw, why, and
// roughly what they would have been worth.
type BuyerReport struct {
	Name      string
	Cap       int64
	Lambda    float64
	Requests  int64 // requests the exchange had for this buyer
	Sent      int64 // includes Explored
	Explored  int64
	Filtered  int64
	OverCap   int64
	Duplicate int64
	Bids      int64

	// WithheldValue is the model's expected bid value (sum of CPMs) of the
	// requests that were not sent. It's an estimate, built from a sample.
	WithheldValue float64
	TopWithheld   []SegmentValue
}

// SegmentValue is withheld value for one format, country and device.
type SegmentValue struct {
	Segment Features
	Value   float64
}

// Report summarises every buyer. top is how many withheld segments to list.
func (s *Shaper) Report(top int) []BuyerReport {
	out := make([]BuyerReport, len(s.pacers))
	for i := range s.pacers {
		st := &s.stats[i]
		r := BuyerReport{
			Name:      s.cfg.Buyers[i].Name,
			Cap:       s.pacers[i].Cap(),
			Lambda:    s.pacers[i].Lambda(),
			Requests:  st.count[0].Load(),
			Explored:  st.count[1+Explored].Load(),
			Filtered:  st.count[1+Filtered].Load(),
			OverCap:   st.count[1+OverCap].Load(),
			Duplicate: st.count[1+Duplicate].Load(),
			Bids:      st.bids.Load(),
		}
		r.Sent = st.count[1+Sent].Load() + r.Explored
		st.mu.Lock()
		r.WithheldValue = st.value
		for seg, v := range st.segments {
			r.TopWithheld = append(r.TopWithheld, SegmentValue{seg, v})
		}
		st.mu.Unlock()
		sort.Slice(r.TopWithheld, func(a, b int) bool { return r.TopWithheld[a].Value > r.TopWithheld[b].Value })
		if len(r.TopWithheld) > top {
			r.TopWithheld = r.TopWithheld[:top]
		}
		out[i] = r
	}
	return out
}

// WriteReport prints reports as plain text.
func WriteReport(w io.Writer, reps []BuyerReport, countryName func(uint8) string) {
	for _, r := range reps {
		pct := func(n int64) float64 {
			if r.Requests == 0 {
				return 0
			}
			return 100 * float64(n) / float64(r.Requests)
		}
		fmt.Fprintf(w, "%s (cap %d/s, price %.4f)\n", r.Name, r.Cap, r.Lambda)
		fmt.Fprintf(w, "  requests %d, sent %d (%.1f%%), of which explored %d\n", r.Requests, r.Sent, pct(r.Sent), r.Explored)
		fmt.Fprintf(w, "  withheld: filtered %d (%.1f%%), over cap %d, duplicate %d\n", r.Filtered, pct(r.Filtered), r.OverCap, r.Duplicate)
		bidRate := 0.0
		if r.Sent > 0 {
			bidRate = 100 * float64(r.Bids) / float64(r.Sent)
		}
		fmt.Fprintf(w, "  bids %d (%.1f%% of sent); estimated value withheld $%.2f\n", r.Bids, bidRate, r.WithheldValue/1000)
		for _, sv := range r.TopWithheld {
			fmt.Fprintf(w, "    %-8s %-3s %-8s $%.2f\n", sv.Segment.Format, countryName(sv.Segment.Country), sv.Segment.Device, sv.Value/1000)
		}
	}
}

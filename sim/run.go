package sim

import (
	"time"

	"github.com/Rakshit-gen/bidsieve/shape"
)

// epoch is simulated time zero. Any fixed instant works; this one keeps
// timestamps readable.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec float64) time.Time { return epoch.Add(time.Duration(sec * float64(time.Second))) }

// BuyerResult is one buyer's side of a run.
type BuyerResult struct {
	Name       string
	Cap        int
	Sent       int64
	Bids       int64
	Value      float64 // sum of bid prices received
	AvailBids  int64   // bids the buyer would have made if sent everything
	AvailValue float64
}

// Bucket is one slice of the time series.
type Bucket struct {
	T           int
	Revenue     float64
	Ceiling     float64
	Sent        int64
	DriftAvail  int64 // bids the drift buyer would make on its new segment
	DriftCaught int64
	NewAvail    int64 // bids any buyer would make on new publishers
	NewCaught   int64
	DroppedSent int64 // requests sent to the buyer that stopped bidding, on the dropped segment
	OverCap     int64
}

// Result is everything measured in one run.
type Result struct {
	Policy      string
	Scenario    string
	Seconds     int
	Requests    int64 // request copies seen
	Impressions int64
	Revenue     float64 // best bid per impression, summed
	Ceiling     float64 // best bid any buyer would have made, summed

	Sent      int64
	Bids      int64
	BidValue  float64
	Explored  int64
	OverCap   int64 // the policy wanted to send but the cap was full
	DupSends  int64 // sent to a buyer that already had the impression
	TrueDupes int64 // skipped as duplicate, correctly
	FalseDups int64 // skipped as duplicate, but the buyer had never seen it

	Buyers []BuyerResult
	Series []Bucket

	Elapsed time.Duration
	Cells   int
}

// Hooks let an experiment watch a run without copying the loop.
type Hooks struct {
	// Second is called at the start of each simulated second.
	Second func(sec int)
	// Decided is called after every decision, with the truth for each buyer.
	Decided func(r *shape.Request, now time.Time, decs []shape.Decision, truth []Truth)
}

// Run plays a scenario through one policy.
func Run(sc Scenario, pol Policy, hooks *Hooks) *Result {
	start := time.Now()
	w := NewWorld(sc)
	nb := len(w.buyers)
	res := &Result{Policy: pol.Name(), Scenario: sc.Name, Seconds: sc.Seconds}
	for _, b := range w.buyers {
		res.Buyers = append(res.Buyers, BuyerResult{Name: b.name, Cap: b.cap})
	}
	nbuckets := (sc.Seconds + sc.Bucket - 1) / sc.Bucket
	res.Series = make([]Bucket, nbuckets)
	for i := range res.Series {
		res.Series[i].T = i * sc.Bucket
	}
	bucket := func(t float64) *Bucket {
		i := int(t) / sc.Bucket
		if i >= nbuckets {
			i = nbuckets - 1
		}
		return &res.Series[i]
	}

	st := newStream(w, func(imp *impression) {
		res.Impressions++
		res.Ceiling += imp.ceiling
		bk := bucket(imp.born)
		bk.Ceiling += imp.ceiling
		for b := range nb {
			if pr := imp.truth[b].price; pr > 0 {
				res.Buyers[b].AvailBids++
				res.Buyers[b].AvailValue += pr
				if imp.isNew {
					bk.NewAvail++
				}
				if b == driftBuyer && imp.drift {
					bk.DriftAvail++
				}
			}
		}
	})

	truth := make([]Truth, nb)
	decs := make([]shape.Decision, 0, nb)
	var events []*event
	for sec := range sc.Seconds {
		if hooks != nil && hooks.Second != nil {
			hooks.Second(sec)
		}
		events = st.second(sec, events)
		for _, ev := range events {
			imp := ev.imp
			now := at(ev.at)
			for b := range nb {
				a := imp.truth[b]
				truth[b] = Truth{P: a.p, Mu: a.mu, Price: a.price, Seen: imp.reached&(1<<b) != 0}
			}
			res.Requests++
			decs = pol.Decide(&ev.req, now, truth, decs)
			if hooks != nil && hooks.Decided != nil {
				hooks.Decided(&ev.req, now, decs, truth)
			}
			bk := bucket(ev.at)
			for _, d := range decs {
				b := d.Buyer
				seen := imp.reached&(1<<b) != 0
				switch d.Reason {
				case shape.OverCap:
					res.OverCap++
					bk.OverCap++
				case shape.Duplicate:
					if seen {
						res.TrueDupes++
					} else {
						res.FalseDups++
					}
				}
				if !d.Send {
					continue
				}
				res.Sent++
				bk.Sent++
				res.Buyers[b].Sent++
				if d.Reason == shape.Explored {
					res.Explored++
				}
				if b == droppedBuyer && imp.dropped && sc.DriftAt > 0 && ev.at >= float64(sc.DriftAt) {
					bk.DroppedSent++
				}
				price := 0.0
				if seen {
					res.DupSends++ // the buyer drops repeats of an impression it already had
				} else {
					imp.reached |= 1 << b
					price = truth[b].Price
				}
				if price > 0 {
					res.Bids++
					res.BidValue += price
					res.Buyers[b].Bids++
					res.Buyers[b].Value += price
					if price > imp.best {
						res.Revenue += price - imp.best
						bucket(imp.born).Revenue += price - imp.best
						imp.best = price
					}
					if imp.isNew {
						bucket(imp.born).NewCaught++
					}
					if b == driftBuyer && imp.drift {
						bucket(imp.born).DriftCaught++
					}
				}
				pol.Observe(shape.Outcome{Buyer: b, Features: imp.f, Prop: d.Prop, Bid: price > 0, Price: price, At: now})
			}
		}
	}
	if bs, ok := pol.(*Bidsieve); ok {
		res.Cells = bs.S.Cells()
	}
	res.Elapsed = time.Since(start)
	return res
}

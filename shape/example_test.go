package shape_test

import (
	"fmt"
	"time"

	"github.com/Rakshit-gen/bidsieve/shape"
)

// The whole loop an exchange runs: decide per buyer, forward the ones that
// say Send, and report back what each buyer answered.
func Example() {
	cfg := shape.DefaultConfig()
	cfg.Buyers = []shape.Buyer{{Name: "dsp-a", QPS: 5000}, {Name: "dsp-b", QPS: 800}}
	s, err := shape.New(cfg)
	if err != nil {
		panic(err)
	}

	now := time.Unix(1_700_000_000, 0)
	req := shape.Request{
		ID: 42,
		Features: shape.Features{
			Publisher: 7,
			Format:    shape.Banner300x250,
			Country:   shape.CountryIndex("USA"),
			Device:    shape.Mobile,
			Channel:   shape.App,
		},
		Dupe: shape.DupeKey{Fingerprint: shape.Fingerprint("pub-7", "com.example.app", "slot-1", "ifa-123")},
	}

	out := s.Decide(&req, now, nil)
	for _, d := range out {
		if !d.Send {
			continue
		}
		// Forward the request to the buyer here, then tell the shaper what
		// came back. A timeout counts as no bid.
		s.Observe(shape.Outcome{Buyer: d.Buyer, Features: req.Features, Prop: d.Prop, Bid: false, At: now})
	}
	fmt.Println(len(out), "decisions")
	// Output: 2 decisions
}

package shape

import (
	"math"
	"sync"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func newTestShaper(t *testing.T, mod func(*Config)) *Shaper {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Buyers = []Buyer{{Name: "a", QPS: 100}}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func feed(s *Shaper, f Features, at time.Time, n int, bidRate, price float64) {
	for i := range n {
		bid := float64(i%100) < bidRate*100
		s.Observe(Outcome{Buyer: 0, Features: f, Prop: 1, Bid: bid, Price: price, At: at})
	}
}

func TestNewPublisherBorrowsFromItsSegment(t *testing.T) {
	s := newTestShaper(t, nil)
	ctvVideo := Features{Publisher: 1, Format: Video, Country: 1, Device: CTV, Channel: App}
	banner := Features{Publisher: 2, Format: Banner320x50, Country: 1, Device: Mobile, Channel: App}
	feed(s, ctvVideo, t0, 2000, 0.40, 10)
	feed(s, banner, t0, 2000, 0.02, 1)

	fresh := ctvVideo
	fresh.Publisher = 99
	e := s.Estimate(0, fresh, t0)
	if math.Abs(e.P-0.40) > 0.05 {
		t.Fatalf("unseen CTV publisher: P = %.3f, want about 0.40", e.P)
	}
	if math.Abs(e.Price-10) > 1 {
		t.Fatalf("unseen CTV publisher: price = %.2f, want about 10", e.Price)
	}
	freshBanner := banner
	freshBanner.Publisher = 98
	if e := s.Estimate(0, freshBanner, t0); e.P > 0.05 {
		t.Fatalf("unseen banner publisher: P = %.3f, want close to 0.02", e.P)
	}
}

func TestEvidenceFadesIntoUncertainty(t *testing.T) {
	s := newTestShaper(t, nil)
	f := Features{Publisher: 1, Format: Video, Country: 2, Device: Desktop}
	feed(s, f, t0, 5000, 0.01, 2)
	fresh := s.Estimate(0, f, t0)
	stale := s.Estimate(0, f, t0.Add(20*s.cfg.HalfLife))
	if fresh.N < 1000 {
		t.Fatalf("fresh evidence N = %.0f, want thousands", fresh.N)
	}
	if stale.N > 2 {
		t.Fatalf("after 20 half-lives N = %.2f, want under 2", stale.N)
	}
	if stale.Sigma < 10*fresh.Sigma {
		t.Fatalf("stale sigma %.4f should be far wider than fresh %.4f", stale.Sigma, fresh.Sigma)
	}
}

func TestNoDecayKeepsOldOpinions(t *testing.T) {
	s := newTestShaper(t, func(c *Config) { c.HalfLife = -1 })
	f := Features{Publisher: 1, Format: Video}
	feed(s, f, t0, 1000, 0.01, 2)
	a := s.Estimate(0, f, t0)
	b := s.Estimate(0, f, t0.Add(24*time.Hour))
	if a != b {
		t.Fatalf("with fading off the estimate moved: %+v vs %+v", a, b)
	}
}

func TestSendProbStaysInBounds(t *testing.T) {
	s := newTestShaper(t, nil)
	for _, e := range []Estimate{
		{P: 0, Price: 0, Sigma: 0.1},
		{P: 0.001, Price: 1, Sigma: 0.0001},
		{P: 0.9, Price: 20, Sigma: 0.01},
		{P: 0.05, Price: 1, Sigma: 0.05},
	} {
		e.Value = e.P * e.Price
		for _, lambda := range []float64{1e-7, 0.05, 100} {
			q := s.sendProb(e, lambda)
			if q < s.cfg.MinExplore || q > 1 || math.IsNaN(q) {
				t.Fatalf("sendProb(%+v, %g) = %g", e, lambda, q)
			}
		}
	}
	sure := Estimate{P: 0.5, Price: 10, Value: 5, Sigma: 0.001}
	if q := s.sendProb(sure, 0.1); q < 0.999 {
		t.Fatalf("a request worth 50x the price went with q = %g", q)
	}
	if q := s.sendProb(sure, 500); q != s.cfg.MinExplore {
		t.Fatalf("a request worth 1%% of the price went with q = %g, want the floor", q)
	}
}

func TestPacerHoldsTheCap(t *testing.T) {
	s := newTestShaper(t, nil)
	out := make([]Decision, 0, 1)
	var id uint64
	lastSecond := 0
	for sec := range 60 {
		sent := 0
		for i := range 1000 {
			id++
			// A spread of values so the price has something to cut.
			f := Features{Publisher: uint32(i % 50), Format: Format(i % 7), Country: uint8(i % 12)}
			at := t0.Add(time.Duration(sec)*time.Second + time.Duration(i)*time.Millisecond)
			out = s.Decide(&Request{ID: id, Features: f}, at, out)
			if out[0].Send {
				sent++
				s.Observe(Outcome{Features: f, Prop: out[0].Prop, Bid: i%50 < int(f.Format)*3, Price: 1 + float64(i%5), At: at})
			}
		}
		if sent > 100 {
			t.Fatalf("second %d: sent %d with a cap of 100", sec, sent)
		}
		lastSecond = sent
	}
	if lastSecond < 85 {
		t.Fatalf("after a minute the pacer only used %d of 100", lastSecond)
	}
	rep := s.Report(3)[0]
	if rep.Requests != 60_000 || rep.Sent+rep.Filtered+rep.OverCap+rep.Duplicate != rep.Requests {
		t.Fatalf("report doesn't add up: %+v", rep)
	}
}

// The case history-based shapers get wrong: a buyer ignored a segment, the
// shaper learned to stop sending it, then the buyer started wanting it.
func TestStaleSegmentIsRediscovered(t *testing.T) {
	run := func(halfLife time.Duration) (sentLastMinute int) {
		s := newTestShaper(t, func(c *Config) {
			c.HalfLife = halfLife
			c.Buyers = []Buyer{{Name: "a", QPS: 200}}
		})
		good := Features{Publisher: 1, Format: Banner300x250, Country: 1, Device: Desktop}
		flips := Features{Publisher: 2, Format: Video, Country: 3, Device: CTV}
		out := make([]Decision, 0, 1)
		var id uint64
		for sec := range 900 {
			for i := range 1000 {
				id++
				f := good
				if i%5 == 0 {
					f = flips
				}
				at := t0.Add(time.Duration(sec)*time.Second + time.Duration(i)*time.Millisecond)
				out = s.Decide(&Request{ID: id, Features: f}, at, out)
				if !out[0].Send {
					continue
				}
				bid := id%10 == 0
				price := 1.0
				if f == flips {
					bid = sec >= 300 && id%2 == 0 // from minute five on, it bids on half
					price = 8
				}
				if f == flips && sec >= 840 {
					sentLastMinute++
				}
				s.Observe(Outcome{Features: f, Prop: out[0].Prop, Bid: bid, Price: price, At: at})
			}
		}
		return sentLastMinute
	}
	// 200 flipped requests a second are now worth 4 CPM each against
	// 0.1 for the rest, so a working shaper sends nearly all of them.
	if got := run(2 * time.Minute); got < 0.9*200*60 {
		t.Fatalf("sent %d of %d flipped requests in the last minute", got, 200*60)
	}
}

func TestDuplicatesSkipBuyersThatAlreadyHaveTheImpression(t *testing.T) {
	s := newTestShaper(t, func(c *Config) {
		c.Buyers = []Buyer{{Name: "a", QPS: 1000}, {Name: "b", QPS: 1000}}
		c.MinExplore = 1 // send everything, so only dedupe decides
	})
	f := Features{Publisher: 1, Format: Video}
	key := DupeKey{Fingerprint: 42}
	out := s.Decide(&Request{ID: 1, Features: f, Dupe: key}, t0, nil)
	if !out[0].Send || !out[1].Send {
		t.Fatalf("first copy should go to both: %+v", out)
	}
	out = s.Decide(&Request{ID: 2, Features: f, Dupe: key}, t0.Add(100*time.Millisecond), out)
	if out[0].Reason != Duplicate || out[1].Reason != Duplicate {
		t.Fatalf("copy 100ms later should be a duplicate: %+v", out)
	}
	out = s.Decide(&Request{ID: 3, Features: f, Dupe: key}, t0.Add(time.Second), out)
	if !out[0].Send {
		t.Fatalf("a second later it's a new impression: %+v", out)
	}

	// A shared tid catches a copy the fingerprint window has let go.
	tk := DupeKey{Fingerprint: 7, TID: 99}
	s.Decide(&Request{ID: 4, Features: f, Dupe: tk}, t0.Add(5*time.Second), out)
	late := DupeKey{Fingerprint: 8, TID: 99}
	out = s.Decide(&Request{ID: 5, Features: f, Dupe: late}, t0.Add(7*time.Second), out)
	if out[0].Reason != Duplicate {
		t.Fatalf("tid match two seconds later should be a duplicate: %+v", out)
	}
}

func TestConcurrentUse(t *testing.T) {
	s := newTestShaper(t, func(c *Config) {
		c.Buyers = []Buyer{{Name: "a", QPS: 500}, {Name: "b", QPS: 50}}
	})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := make([]Decision, 0, 2)
			for i := range 5000 {
				id := uint64(g*1_000_000 + i)
				f := Features{Publisher: uint32(i % 300), Format: Format(i % 7), Country: uint8(i % 20)}
				at := t0.Add(time.Duration(i) * time.Millisecond)
				out = s.Decide(&Request{ID: id, Features: f, Dupe: DupeKey{Fingerprint: id}}, at, out)
				for _, d := range out {
					if d.Send {
						s.Observe(Outcome{Buyer: d.Buyer, Features: f, Prop: d.Prop, Bid: i%7 == 0, Price: 2, At: at})
					}
				}
			}
		}()
	}
	wg.Wait()
	if s.Cells() == 0 {
		t.Fatal("no cells learned")
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	s := newTestShaper(t, nil)
	f := Features{Publisher: 1, Format: Native}
	feed(s, f, t0, 500, 0.2, 3)
	snap := s.Snapshot()
	before := snap.Estimate(0, f, t0)
	feed(s, f, t0, 5000, 0.9, 3)
	if after := snap.Estimate(0, f, t0); after != before {
		t.Fatalf("snapshot moved with the live model: %+v vs %+v", after, before)
	}
}

func TestConfigErrors(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("no buyers should be an error")
	}
	if _, err := New(Config{Buyers: []Buyer{{Name: "x"}}}); err == nil {
		t.Fatal("zero QPS should be an error")
	}
}

func TestPackRoundTripsEveryField(t *testing.T) {
	f := Features{Publisher: maxPublisher - 1, Format: Native, Country: 63, Device: CTV, Channel: App, Pos: PosOther}
	if !f.Valid() {
		t.Fatal("max values should be valid")
	}
	p := f.pack()
	if got := uint32(p & maskPub); got != f.Publisher {
		t.Fatalf("publisher %d", got)
	}
	if got := Format(p & maskFormat >> 24); got != f.Format {
		t.Fatalf("format %d", got)
	}
	if got := uint8(p & maskCountry >> 28); got != f.Country {
		t.Fatalf("country %d", got)
	}
	if got := Position(p & maskPos >> 38); got != f.Pos {
		t.Fatalf("pos %d", got)
	}
	// Different levels and buyers never share a key.
	seen := map[uint64]bool{}
	for b := range 64 {
		for l := range numLevels {
			k := cellKey(b, l, p)
			if seen[k] {
				t.Fatalf("key collision at buyer %d level %d", b, l)
			}
			seen[k] = true
		}
	}
}

// Stored values grow between landmark moves. A shaper running for days must
// move its landmark and keep giving the same answers.
func TestLandmarkMovesWithoutChangingAnswers(t *testing.T) {
	s := newTestShaper(t, nil)
	f := Features{Publisher: 3, Format: Video, Country: 4, Device: CTV}
	start := t0
	for h := range 72 {
		at := start.Add(time.Duration(h) * time.Hour)
		feed(s, f, at, 300, 0.3, 5)
		e := s.Estimate(0, f, at)
		if math.IsNaN(e.P) || math.IsInf(e.N, 0) || math.Abs(e.P-0.3) > 0.03 || e.N < 250 {
			t.Fatalf("hour %d: %+v", h, e)
		}
	}
	if s.tab.t0.Load() == start.UnixNano() {
		t.Fatal("landmark never moved in three days")
	}
}

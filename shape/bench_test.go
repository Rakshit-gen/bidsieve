package shape

import (
	"testing"
	"time"
)

// benchShaper returns a shaper with ten buyers and a model warmed on a few
// hundred thousand outcomes, so lookups hit populated cells at every level.
func benchShaper(b *testing.B) (*Shaper, []Request) {
	b.Helper()
	cfg := DefaultConfig()
	for i := range 10 {
		cfg.Buyers = append(cfg.Buyers, Buyer{Name: "b", QPS: 200 + 100*i})
	}
	s, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	reqs := make([]Request, 4096)
	for i := range reqs {
		h := mix(uint64(i) + 1)
		reqs[i] = Request{
			ID: h,
			Features: Features{
				Publisher: uint32(h % 2000),
				Format:    Format(h >> 11 % 6),
				Country:   uint8(1 + h>>17%12),
				Device:    Device(h >> 23 % 4),
				Channel:   Channel(h >> 29 % 2),
				Pos:       Position(h >> 31 % 3),
			},
			Dupe: DupeKey{Fingerprint: h},
		}
	}
	for i := range 300_000 {
		r := &reqs[i%len(reqs)]
		s.Observe(Outcome{Buyer: i % 10, Features: r.Features, Prop: 0.5, Bid: i%7 == 0, Price: 2, At: t0})
	}
	return s, reqs
}

func BenchmarkDecide(b *testing.B) {
	s, reqs := benchShaper(b)
	out := make([]Decision, 0, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		r := reqs[i%len(reqs)]
		r.ID = uint64(i)
		out = s.Decide(&r, t0.Add(time.Duration(i)*time.Microsecond), out)
	}
}

func BenchmarkDecideParallel(b *testing.B) {
	s, reqs := benchShaper(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		out := make([]Decision, 0, 10)
		i := 0
		for pb.Next() {
			r := reqs[i%len(reqs)]
			r.ID = uint64(i)
			out = s.Decide(&r, t0, out)
			i++
		}
	})
}

func BenchmarkObserve(b *testing.B) {
	s, reqs := benchShaper(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		s.Observe(Outcome{Buyer: i % 10, Features: reqs[i%len(reqs)].Features, Prop: 0.3, Bid: i%5 == 0, Price: 3, At: t0})
	}
}

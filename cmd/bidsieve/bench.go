package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Rakshit-gen/bidsieve/shape"
	"github.com/Rakshit-gen/bidsieve/sim"
)

// bench measures what an exchange would care about before putting the
// shaper in its request path: how long one decision takes, how that holds
// up with many goroutines sharing one shaper, and how much memory the model
// needs once warm.
func bench(dir string) {
	sc := sim.Standard()
	sc.Seconds = 600
	w := sim.NewWorld(sc)
	p := sim.NewBidsieve(w, "bidsieve", nil)

	// Warm the model on ten simulated minutes and keep the last requests
	// as the replay set, with the times they arrived at.
	type rec struct {
		r  shape.Request
		at time.Time
	}
	var recs []rec
	keepFrom := time.Duration(sc.Seconds-60) * time.Second
	var first time.Time
	sim.Run(sc, p, &sim.Hooks{Decided: func(r *shape.Request, now time.Time, _ []shape.Decision, _ []sim.Truth) {
		if first.IsZero() {
			first = now
		}
		if now.Sub(first) >= keepFrom && len(recs) < 200_000 {
			recs = append(recs, rec{*r, now})
		}
	}})
	s := p.S
	nb := len(s.Buyers())

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	cells := s.Cells()

	// Single goroutine, each call timed on its own.
	lat := make([]time.Duration, len(recs))
	out := make([]shape.Decision, 0, nb)
	for i := range recs {
		t := time.Now()
		out = s.Decide(&recs[i].r, recs[i].at, out)
		lat[i] = time.Since(t)
	}
	slices.Sort(lat)

	// Observe on its own.
	obs := make([]shape.Outcome, 0, 100_000)
	for i := range recs {
		if len(obs) == cap(obs) {
			break
		}
		obs = append(obs, shape.Outcome{Buyer: i % nb, Features: recs[i].r.Features, Prop: 0.5, Bid: i%9 == 0, Price: 2, At: recs[i].at})
	}
	t := time.Now()
	for _, o := range obs {
		s.Observe(o)
	}
	obsNs := float64(time.Since(t).Nanoseconds()) / float64(len(obs))

	var b strings.Builder
	b.WriteString("## Decision cost\n\n")
	fmt.Fprintf(&b, "Machine: %s, %d cores, %s. The model was warmed on %d simulated seconds of the standard scenario and holds %d cells (%.1f MB heap in use, about %.0f bytes per cell including maps). Each Decide covers all %d buyers.\n\n",
		cpuName(), runtime.NumCPU(), runtime.Version(), sc.Seconds, cells, float64(ms.HeapAlloc)/1e6, float64(ms.HeapAlloc)/float64(max(1, cells)), nb)
	b.WriteString("| | p50 | p90 | p99 | p99.9 | max |\n|---|---:|---:|---:|---:|---:|\n")
	q := func(f float64) time.Duration { return lat[min(len(lat)-1, int(f*float64(len(lat))))] }
	fmt.Fprintf(&b, "| Decide, one goroutine (%d calls) | %s | %s | %s | %s | %s |\n\n", len(lat), us(q(.5)), us(q(.9)), us(q(.99)), us(q(.999)), us(lat[len(lat)-1]))
	fmt.Fprintf(&b, "Observe, one outcome across six levels: %.0f ns.\n\n", obsNs)

	b.WriteString("Many goroutines sharing one shaper, each replaying its own slice of the requests:\n\n")
	b.WriteString("| goroutines | decisions/s | p50 | p99 |\n|---|---:|---:|---:|\n")
	for _, g := range []int{1, 2, 4, 8} {
		if g > runtime.NumCPU() {
			break
		}
		lats := make([][]time.Duration, g)
		var wg sync.WaitGroup
		start := time.Now()
		for k := range g {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out := make([]shape.Decision, 0, nb)
				my := make([]time.Duration, 0, len(recs)/g+1)
				for i := k; i < len(recs); i += g {
					t := time.Now()
					out = s.Decide(&recs[i].r, recs[i].at, out)
					my = append(my, time.Since(t))
				}
				lats[k] = my
			}()
		}
		wg.Wait()
		el := time.Since(start)
		all := slices.Concat(lats...)
		slices.Sort(all)
		fmt.Fprintf(&b, "| %d | %.0f | %s | %s |\n", g, float64(len(all))/el.Seconds(), us(all[len(all)/2]), us(all[len(all)*99/100]))
	}
	b.WriteString("\nTimings include `time.Now()` around each call, which costs a few tens of nanoseconds on this machine.\n\n")
	emit(dir, "bench.md", b.String())
}

func us(d time.Duration) string { return fmt.Sprintf("%.2f µs", float64(d.Nanoseconds())/1000) }

func cpuName() string {
	if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return runtime.GOARCH
}

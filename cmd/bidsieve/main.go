// Command bidsieve runs the experiments behind the README and prints the
// results. Every number in the README comes from `bidsieve all`.
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Rakshit-gen/bidsieve/internal/svg"
	"github.com/Rakshit-gen/bidsieve/shape"
	"github.com/Rakshit-gen/bidsieve/sim"
)

const usage = `usage: bidsieve <command> [-out dir]

commands:
  compare   every policy on the standard scenario and the single-path one
  ablate    bidsieve with one idea switched off at a time
  dedupe    duplicate detection across fingerprint windows
  ope       how well logged decisions predict other policies
  bench     decision latency, throughput and memory
  report    the per-buyer withheld-traffic report from one run
  seeds     the main comparison repeated over five random worlds
  all       everything above, written to -out
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	out := fs.String("out", "", "directory for markdown, CSV and SVG output (default: print only)")
	fs.Parse(os.Args[2:])

	if *out != "" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fatal(err)
		}
	}
	run := map[string]func(string){
		"compare": compare,
		"ablate":  ablate,
		"dedupe":  dedupe,
		"ope":     runOPE,
		"bench":   bench,
		"report":  report,
		"seeds":   seeds,
	}
	if cmd == "all" {
		for _, name := range []string{"compare", "ablate", "dedupe", "ope", "bench", "report", "seeds"} {
			fmt.Fprintf(os.Stderr, "running %s...\n", name)
			start := time.Now()
			run[name](*out)
			fmt.Fprintf(os.Stderr, "  %s done in %.1fs\n", name, time.Since(start).Seconds())
		}
		return
	}
	f, ok := run[cmd]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	f(*out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bidsieve:", err)
	os.Exit(1)
}

// emit prints a section and, with -out, writes it to name as well.
func emit(dir, name, body string) {
	fmt.Print(body)
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		fatal(err)
	}
}

func writeFile(dir, name string, write func(io.Writer) error) {
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		fatal(err)
	}
	if err := write(f); err != nil {
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func compare(dir string) {
	var b strings.Builder
	for _, sc := range []sim.Scenario{sim.Standard(), sim.SinglePath()} {
		res := sim.Compare(sc, sim.Policies)
		oracle := find(res, "oracle")
		fmt.Fprintf(&b, "## Scenario: %s\n\n%s\n\n", sc.Name, describe(sc))
		b.WriteString("| policy | revenue | of ceiling | of oracle | bid rate | value per 1k sent | cap used | sent to a buyer that had it | over-cap refusals |\n")
		b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		var sums []sim.Summary
		for _, r := range res {
			s := sim.Summarize(sc, r, oracle)
			sums = append(sums, s)
			fmt.Fprintf(&b, "| %s | $%.0f | %s | %s | %s | $%.2f | %s | %s | %d |\n",
				s.Policy, s.Revenue, pct(s.RevenuePct), pct(s.VsOracle), pct(s.BidRate), s.ValuePer1k, pct(s.CapUse), pct(s.DupShare), s.OverCap)
		}
		if sc.DriftAt > 0 {
			fmt.Fprintf(&b, "\nWhen %s starts bidding on European video at t=%ds, and %s stops bidding on US/Indian mobile banners:\n\n", "dsp02", sc.DriftAt, "dsp05")
			b.WriteString("| policy | new segment caught, first minute | minutes 1-4 | minutes 4-8 | time to 80% of oracle | wasted sends/s to the dead segment |\n|---|---:|---:|---:|---:|---:|\n")
			for _, s := range sums {
				rec := "never"
				if s.DriftRecover >= 0 {
					rec = fmt.Sprintf("%ds", s.DriftRecover)
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %.1f |\n", s.Policy, pct(s.DriftEarly), pct(s.DriftMid), pct(s.DriftLate), rec, s.DroppedPerSec)
			}
			fmt.Fprintf(&b, "\nWhen %d new publishers go live at t=%ds (share of the bids buyers would make on them that actually happened):\n\n", sc.NewPublishers, sc.NewPubAt)
			b.WriteString("| policy | first minute | first four minutes |\n|---|---:|---:|\n")
			for _, s := range sums {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Policy, pct(s.NewFirstMin), pct(s.NewFirst4Min))
			}
			charts(dir, sc, res)
		}
		b.WriteString("\n| policy | simulated requests per second of wall time |\n|---|---:|\n")
		for _, s := range sums {
			fmt.Fprintf(&b, "| %s | %.0f |\n", s.Policy, s.RequestsPerSec)
		}
		b.WriteString("\n")
		writeFile(dir, "series-"+sc.Name+".csv", func(w io.Writer) error { return seriesCSV(w, res) })
	}
	emit(dir, "compare.md", b.String())
}

func describe(sc sim.Scenario) string {
	copies := "Impressions arrive once."
	if sc.Copies {
		copies = fmt.Sprintf("Impressions arrive through up to four paths; %.0f%% of copies carry a shared tid, %.0f%% of page views are reloaded within two seconds.", 100*sc.SharedTID, 100*sc.Reloads)
	}
	return fmt.Sprintf("%d seconds, %.0f impressions/s, %d publishers (+%d new at t=%ds), %d buyers, traffic doubles for %ds at t=%ds. %s",
		sc.Seconds, sc.Rate, sc.Publishers, sc.NewPublishers, sc.NewPubAt, len(sc.BuyerCaps), sc.SpikeLen, sc.SpikeAt, copies)
}

func find(res []*sim.Result, name string) *sim.Result {
	for _, r := range res {
		if r.Policy == name {
			return r
		}
	}
	return nil
}

func seriesCSV(w io.Writer, res []*sim.Result) error {
	fmt.Fprintln(w, "policy,t,revenue,ceiling,sent,drift_avail,drift_caught,new_avail,new_caught,dropped_sent,over_cap")
	for _, r := range res {
		for _, b := range r.Series {
			_, err := fmt.Fprintf(w, "%s,%d,%.3f,%.3f,%d,%d,%d,%d,%d,%d,%d\n", r.Policy, b.T, b.Revenue/1000, b.Ceiling/1000,
				b.Sent, b.DriftAvail, b.DriftCaught, b.NewAvail, b.NewCaught, b.DroppedSent, b.OverCap)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func charts(dir string, sc sim.Scenario, res []*sim.Result) {
	show := []string{"oracle", "bidsieve", "segment history+control", "segment history", "tuple history+control", "random"}
	line := func(f func(sim.Bucket) (float64, bool)) []svg.Series {
		var ss []svg.Series
		for _, name := range show {
			r := find(res, name)
			if r == nil {
				continue
			}
			var xs, ys []float64
			for _, b := range r.Series {
				y, ok := f(b)
				if !ok {
					y = nanv
				}
				xs = append(xs, float64(b.T+sc.Bucket/2))
				ys = append(ys, y)
			}
			ss = append(ss, svg.Series{Name: name, X: xs, Y: sim.Smooth(ys, 1), Dash: name == "oracle"})
		}
		return ss
	}
	frac := func(a, b int64) (float64, bool) {
		if b == 0 {
			return 0, false
		}
		return float64(a) / float64(b), true
	}
	marks := []svg.Mark{{X: float64(sc.DriftAt), Label: "dsp02 starts bidding"}, {X: float64(sc.NewPubAt), Label: "new publishers"}, {X: float64(sc.SpikeAt), Label: "2x traffic"}}

	drift := &svg.Chart{
		Title: "Share of dsp02's bids on its new segment that the shaper let through", XLabel: "seconds", YLabel: "bids caught",
		XMin: float64(sc.DriftAt - 60), XMax: float64(sc.SpikeAt), YMax: 1, YPercent: true,
		Series: line(func(b sim.Bucket) (float64, bool) { return frac(b.DriftCaught, b.DriftAvail) }),
		Marks:  marks[:2],
	}
	newPubs := &svg.Chart{
		Title: "Share of buyers' bids on brand-new publishers that happened", XLabel: "seconds", YLabel: "bids caught",
		XMin: float64(sc.NewPubAt - 20), XMax: float64(sc.Seconds), YMax: 1, YPercent: true,
		Series: line(func(b sim.Bucket) (float64, bool) { return frac(b.NewCaught, b.NewAvail) }),
		Marks:  marks[1:],
	}
	revenue := &svg.Chart{
		Title: "Revenue captured, as a share of every bid buyers would have made", XLabel: "seconds", YLabel: "of ceiling",
		XMin: 0, XMax: float64(sc.Seconds), YMax: 1, YPercent: true,
		Series: line(func(b sim.Bucket) (float64, bool) {
			if b.Ceiling == 0 {
				return 0, false
			}
			return b.Revenue / b.Ceiling, true
		}),
		Marks: marks,
	}
	writeFile(dir, "drift.svg", drift.Write)
	writeFile(dir, "new-publishers.svg", newPubs.Write)
	writeFile(dir, "revenue.svg", revenue.Write)
}

var nanv = func() float64 { var z float64; return z / z }()

func ablate(dir string) {
	sc := sim.Standard()
	res := sim.Compare(sc, sim.Ablations)
	full := res[0]
	var b strings.Builder
	b.WriteString("## Ablations (standard scenario)\n\nEach row switches one idea off. Revenue is relative to full bidsieve on the same world.\n\n")
	b.WriteString("| variant | revenue | vs full | bid rate | European video, minutes 1-4 | new publishers, first minute | sent to a buyer that had it | explore share |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range res {
		s := sim.Summarize(sc, r, nil)
		fmt.Fprintf(&b, "| %s | $%.0f | %+.1f%% | %s | %s | %s | %s | %s |\n", s.Policy, s.Revenue,
			100*(r.Revenue/full.Revenue-1), pct(s.BidRate), pct(s.DriftMid), pct(s.NewFirstMin), pct(s.DupShare), pct(s.Explore))
	}
	b.WriteString("\n")
	emit(dir, "ablate.md", b.String())
}

func dedupe(dir string) {
	sc := sim.Standard()
	windows := []time.Duration{0, 25 * time.Millisecond, 100 * time.Millisecond, 300 * time.Millisecond, 600 * time.Millisecond, time.Second, 2 * time.Second}
	rows := sim.DedupeSweep(sc, windows)
	var b strings.Builder
	b.WriteString("## Duplicate detection (standard scenario)\n\nA copy is a duplicate if its fingerprint (publisher, page, slot, device) was seen within the window, or its shared tid was seen in the last 10 seconds. Precision counts reloads wrongly skipped; recall counts copies that still got through.\n\n")
	b.WriteString("| window | revenue | requests sent | copies sent to a buyer that had it | real impressions skipped | precision | recall |\n|---|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range rows {
		w := r.Window.String()
		if r.Window == 0 {
			w = "off"
		}
		fmt.Fprintf(&b, "| %s | $%.0f | %d | %d | %d | %s | %s |\n", w, r.Revenue, r.Sent, r.DupSends, r.FalseDups, pct(r.Precision), pct(r.Recall))
	}
	b.WriteString("\n")
	emit(dir, "dedupe.md", b.String())
}

func runOPE(dir string) {
	sc := sim.OPEScenario()
	w := sim.NewWorld(sc)
	bs, snap := sim.RunOPE(sc, sim.NewBidsieve(w, "bidsieve", nil), nil)
	hist, _ := sim.RunOPE(sc, sim.NewHistory(sim.NewWorld(sc), "segment history", sim.SegmentKey, 60, 5, 0), snap)
	var b strings.Builder
	fmt.Fprintf(&b, "## Offline evaluation\n\n%d seconds without duplicates or events. At t=%ds bidsieve's model is frozen; the targets below are fixed rules on that frozen model. Their true value is summed from the world; the estimates use only the logs from t=%ds on. Values are dollars of bid value received by buyers, before caps.\n\n", sc.Seconds, sc.Seconds/2, sc.Seconds/2)
	for _, rep := range []*sim.OPEReport{bs, hist} {
		fmt.Fprintf(&b, "Logged by **%s** (%d request-buyer samples):\n\n", rep.Logger, rep.Logged)
		b.WriteString("| target | sends/s (of caps) | truth | IPS | DR | SNIPS | naive replay | target sends the logger could never make |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, r := range rep.Rows {
			fmt.Fprintf(&b, "| %s | %.0f (%.0f%%) | $%.0f | $%.0f ± %.0f (%+.1f%%) | $%.0f ± %.0f (%+.1f%%) | $%.0f (%+.1f%%) | $%.0f (%+.1f%%) | %s |\n",
				r.Target, r.Volume, 100*r.CapShare, r.Truth,
				r.IPS.Total, 1.96*r.IPS.StdErr, errPct(r.IPS.Total, r.Truth),
				r.DR.Total, 1.96*r.DR.StdErr, errPct(r.DR.Total, r.Truth),
				r.SNIPS, errPct(r.SNIPS, r.Truth),
				r.Naive, errPct(r.Naive, r.Truth),
				pct(r.Unsupported))
		}
		b.WriteString("\n")
	}
	b.WriteString("± is a 95% interval (1.96 standard errors).\n\n")
	emit(dir, "ope.md", b.String())
}

func errPct(est, truth float64) float64 {
	if truth == 0 {
		return 0
	}
	return 100 * (est/truth - 1)
}

func report(dir string) {
	sc := sim.Standard()
	w := sim.NewWorld(sc)
	p := sim.NewBidsieve(w, "bidsieve", nil)
	sim.Run(sc, p, nil)
	var b strings.Builder
	b.WriteString("## Withheld-traffic report (standard scenario, end of run)\n\nWhat each buyer would get from the exchange: what it never saw, why, and roughly what it was worth by the shaper's own estimate.\n\n```\n")
	shape.WriteReport(&b, p.S.Report(3), shape.CountryName)
	b.WriteString("```\n\n")
	emit(dir, "report.md", b.String())
}

// seeds repeats the comparison on worlds built from different seeds, so a
// result that only holds for seed 7 shows up as a wide spread.
func seeds(dir string) {
	keep := []string{"oracle", "bidsieve", "segment history+control", "segment history", "tuple history+control"}
	build := func(w *sim.World) []sim.Policy {
		var ps []sim.Policy
		for _, p := range sim.Policies(w) {
			if slices.Contains(keep, p.Name()) {
				ps = append(ps, p)
			}
		}
		return ps
	}
	const n = 5
	type col struct{ rev, vsOracle, drift, newPubs []float64 }
	cols := make([]col, len(keep))
	sums := make([][]sim.Summary, n)
	var wg sync.WaitGroup
	for seed := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := sim.Standard()
			sc.Seed = 100 + uint64(seed)
			res := sim.Compare(sc, build)
			oracle := find(res, "oracle")
			for _, r := range res {
				sums[seed] = append(sums[seed], sim.Summarize(sc, r, oracle))
			}
		}()
	}
	wg.Wait()
	for _, row := range sums {
		for i, s := range row {
			c := &cols[i]
			c.rev = append(c.rev, s.RevenuePct)
			c.vsOracle = append(c.vsOracle, s.VsOracle)
			c.drift = append(c.drift, s.DriftMid)
			c.newPubs = append(c.newPubs, s.NewFirstMin)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Across seeds\n\nThe standard scenario rebuilt with seeds 100 to %d: different publishers, buyer tastes and traffic each time. Mean ± standard deviation over %d runs.\n\n", 100+n-1, n)
	b.WriteString("| policy | of ceiling | of oracle | European video, minutes 1-4 | new publishers, first minute |\n|---|---:|---:|---:|---:|\n")
	for i, name := range keep {
		c := cols[i]
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", name, meanSD(c.rev), meanSD(c.vsOracle), meanSD(c.drift), meanSD(c.newPubs))
	}
	b.WriteString("\n")
	emit(dir, "seeds.md", b.String())
}

func meanSD(xs []float64) string {
	var m, v float64
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	sd := math.Sqrt(v / float64(max(1, len(xs)-1)))
	return fmt.Sprintf("%.1f%% ± %.1f", 100*m, 100*sd)
}

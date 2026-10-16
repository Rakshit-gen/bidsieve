package sim

import (
	"runtime"
	"sync"
	"time"

	"github.com/Rakshit-gen/bidsieve/ope"
	"github.com/Rakshit-gen/bidsieve/shape"
)

// SinglePath is Standard with every impression arriving exactly once. It
// takes duplicates out of the picture, so the comparison is about the
// shaping model alone.
func SinglePath() Scenario {
	sc := Standard()
	sc.Name = "single-path"
	sc.Copies = false
	sc.Reloads = 0
	return sc
}

// Compare runs every policy on its own copy of the world, in parallel.
// Results come back in the same order as the policies.
func Compare(sc Scenario, build func(*World) []Policy) []*Result {
	pols := build(NewWorld(sc))
	out := make([]*Result, len(pols))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i := range pols {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Each policy gets a fresh world so nothing is shared between runs.
			p := build(NewWorld(sc))[i]
			out[i] = Run(sc, p, nil)
		}()
	}
	wg.Wait()
	return out
}

// DedupeRow is one window setting in the duplicate-detection sweep.
type DedupeRow struct {
	Window    time.Duration
	Revenue   float64 // dollars
	Sent      int64
	DupSends  int64 // copies sent to a buyer that already had the impression
	TrueDupes int64
	FalseDups int64 // real impressions skipped as duplicates
	Precision float64
	Recall    float64
}

// DedupeSweep runs bidsieve with each fingerprint window.
func DedupeSweep(sc Scenario, windows []time.Duration) []DedupeRow {
	rows := make([]DedupeRow, len(windows))
	var wg sync.WaitGroup
	for i, win := range windows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := NewWorld(sc)
			p := NewBidsieve(w, "bidsieve", func(c *shape.Config) {
				c.DedupeWindow = win
				if win == 0 {
					c.TIDWindow = 0
				}
			})
			r := Run(sc, p, nil)
			rows[i] = DedupeRow{
				Window: win, Revenue: r.Revenue / 1000, Sent: r.Sent,
				DupSends: r.DupSends, TrueDupes: r.TrueDupes, FalseDups: r.FalseDups,
				Precision: ratio(float64(r.TrueDupes), float64(r.TrueDupes+r.FalseDups)),
				Recall:    ratio(float64(r.TrueDupes), float64(r.TrueDupes+r.DupSends)),
			}
		}()
	}
	wg.Wait()
	return rows
}

// OPEScenario is the world the offline-evaluation experiment runs in: no
// duplicates and no scheduled events, so every (request, buyer) outcome is
// independent of what else was sent.
func OPEScenario() Scenario {
	sc := Standard()
	sc.Name = "ope"
	sc.Seconds = 900
	sc.Copies, sc.Reloads = false, 0
	sc.DriftAt, sc.NewPubAt, sc.SpikeLen = 0, 0, 0
	return sc
}

// OPERow compares estimates of one target policy with its true value.
type OPERow struct {
	Target      string
	Truth       float64 // dollars of bid value the target would have received
	IPS, DR     ope.Result
	SNIPS       float64
	Naive       float64
	Volume      float64 // requests per second the target would send
	CapShare    float64 // that volume over the sum of caps
	Unsupported float64
}

// OPEReport is one logging policy's evaluation of every target.
type OPEReport struct {
	Logger string
	Rows   []OPERow
	// Logged is how many (request, buyer) samples phase two produced.
	Logged int64
}

type target struct {
	name string
	q    func(e shape.Estimate, lambda float64) float64
}

var opeTargets = []target{
	{"frozen model, same price", func(e shape.Estimate, l float64) float64 { return step(e.Value >= l) }},
	{"frozen model, half price", func(e shape.Estimate, l float64) float64 { return step(e.Value >= l/2) }},
	{"frozen model, double price", func(e shape.Estimate, l float64) float64 { return step(e.Value >= 2*l) }},
	{"random 30%", func(shape.Estimate, float64) float64 { return 0.3 }},
	{"send everything", func(shape.Estimate, float64) float64 { return 1 }},
}

func step(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// RunOPE logs decisions from the second half of a run and scores fixed
// target policies against them. The targets are bidsieve's model frozen at
// the halfway point; frozen, so their true value can be computed exactly
// from the world. If snap is nil, the logger must be a *Bidsieve and its
// own halfway snapshot is used; RunOPE returns that snapshot so a second
// logger can be scored on the same targets.
func RunOPE(sc Scenario, logger Policy, snap *shape.Shaper) (*OPEReport, *shape.Shaper) {
	half := sc.Seconds / 2
	snapT := at(float64(half))
	w := NewWorld(sc)
	ests := make([]ope.Estimator, len(opeTargets))
	truths := make([]float64, len(opeTargets))
	rep := &OPEReport{Logger: logger.Name()}
	live := snap == nil

	// A request the policy wanted to send can still be refused because the
	// second's cap ran out. Its real chance of going was the policy's
	// probability times the share of that second's wanted sends the buyer
	// accepted. That share is only known once the second is over, so
	// samples wait in a buffer until then. Refusals are first come, first
	// served, which makes them close to independent of the request itself.
	type pending struct {
		buyer    int
		s        ope.Sample
		qs       []float64
		realized float64
	}
	var buf []pending
	nb := len(w.buyers)
	wanted := make([]float64, nb)
	accepted := make([]float64, nb)
	flush := func() {
		for _, p := range buf {
			if p.s.P > 0 && wanted[p.buyer] > 0 {
				p.s.P *= accepted[p.buyer] / wanted[p.buyer]
			}
			for i, q := range p.qs {
				truths[i] += q * p.realized
				smp := p.s
				smp.Q = q
				ests[i].Add(smp)
			}
		}
		buf = buf[:0]
		clear(wanted)
		clear(accepted)
	}
	hooks := &Hooks{
		Second: func(sec int) {
			flush()
			if sec == half && live {
				snap = logger.(*Bidsieve).S.Snapshot()
			}
		},
		Decided: func(r *shape.Request, now time.Time, decs []shape.Decision, truth []Truth) {
			if now.Before(snapT) {
				return
			}
			for _, d := range decs {
				b := d.Buyer
				if d.Send || d.Reason == shape.OverCap {
					wanted[b]++
				}
				if d.Send {
					accepted[b]++
				}
				e := snap.Estimate(b, r.Features, snapT)
				lam := snap.Lambda(b)
				reward := 0.0
				if d.Send {
					reward = truth[b].Price
				}
				qs := make([]float64, len(opeTargets))
				for i, t := range opeTargets {
					qs[i] = t.q(e, lam)
				}
				rep.Logged++
				buf = append(buf, pending{b, ope.Sample{P: d.Prop, Sent: d.Send, Reward: reward, Predicted: d.Value}, qs, truth[b].Price})
			}
		},
	}
	Run(sc, logger, hooks)
	flush()
	var caps float64
	for _, b := range w.buyers {
		caps += float64(b.cap)
	}
	secs := float64(sc.Seconds - half)
	for i, t := range opeTargets {
		e := &ests[i]
		ips, dr := e.IPS(), e.DR()
		vol := e.Volume() / secs
		rep.Rows = append(rep.Rows, OPERow{
			Target:      t.name,
			Truth:       truths[i] / 1000,
			IPS:         ope.Result{Total: ips.Total / 1000, StdErr: ips.StdErr / 1000},
			DR:          ope.Result{Total: dr.Total / 1000, StdErr: dr.StdErr / 1000},
			SNIPS:       e.SNIPS() / 1000,
			Naive:       e.Naive() / 1000,
			Volume:      vol,
			CapShare:    vol / caps,
			Unsupported: e.Unsupported(),
		})
	}
	return rep, snap
}

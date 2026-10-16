# bidsieve

Traffic shaping for ad exchanges that doesn't starve itself.

An exchange forwards bid requests to buyers (DSPs) that each take a fixed number of requests per second. Shaping picks which requests fill that budget. bidsieve does it with a model that keeps its uncertainty, lets old evidence fade, and logs a send probability with every decision, so it notices when buyers change their minds and its logs can be used to test other policies offline.

It's a Go library (`shape`), a simulated exchange to measure it in (`sim`), and a command that runs every experiment behind the numbers below.

## The problem

Ad exchanges send far more requests than buyers want. PubMatic put the scale at 50 billion impressions a day times 100 DSPs, about 5 trillion requests ([PubMatic, 2018](https://pubmatic.com/blog/bid-throttling-efficient-infrastructure/)). Rivr, a shaping vendor, says unanswered requests are over 90% of most bidstreams, and BidSwitch says a DSP may receive the same impression 20 to 50 times through different paths ([ppc.land](https://ppc.land/traffic-shaping/)). Buyers protect themselves with QPS caps, so the exchange has to choose what goes inside the cap.

Most exchanges and buyers do this from history: look at what a buyer bid on last period, keep sending what it liked, stop sending what it didn't. Amazon's donated Demand Traffic Exclusion (DTE) works this way, with tuple lists refreshed every 5 minutes and a small always-forwarded control group ([ppc.land, April 2026](https://ppc.land/amazon-gives-away-the-tool-that-fixes-programmatics-qps-waste-problem)). PubMatic started charging publishers for sending too much inventory in April 2026 ([ppc.land](https://ppc.land/pubmatic-is-now-charging-publishers-for-sending-too-much-inventory)), so the cost of waste is now on both sides.

## Why it's stuck

History-based shaping has three problems that come from the same place: it only learns from what it chose to send.

1. **It starves itself.** Once a segment is blocked, the shaper never sees the buyer's answer change. A buyer that starts a campaign on a segment it ignored last week doesn't get those requests. Control groups help a little, but a control group sized to be cheap is too thin to notice a new campaign quickly.
2. **New supply starts at zero.** A new publisher has no history, so it either gets blocked by default or gets a flat share regardless of what it is.
3. **You can't test a change safely.** If the shaper never sends some requests, the logs say nothing about them, and no offline estimate of a different policy can be trusted. The only test is live, on real revenue.

On top of that, buyers get no account of what was withheld from them, and duplicate copies of one impression eat into the same caps.

These aren't problems one exchange has. Media.net, PubMatic, Magnite, Index and every DSP on the other side run some version of this.

## The idea

Treat every (request, buyer) decision as a guess with an error bar, and send with the probability that the request is worth the buyer's budget.

- **Estimate from coarse to fine.** Counts are kept at six levels, from "everything sent to this buyer" down to the full tuple of format, country, device, channel, position and publisher. Each level borrows strength from the one above it, so a new publisher starts with its segment's numbers instead of nothing.
- **Let evidence fade.** Every count halves every two minutes. An old "no" loses weight, the error bar widens, and the segment starts being sampled again without any schedule telling it to. That is what stops the starvation loop.
- **Price each buyer's budget.** Each buyer has a price per request that moves once a second with demand against its cap. A request is worth sending when its expected bid value beats the price.
- **Send by probability, not by threshold.** A request goes with the probability that its true bid rate is above break-even, `Φ((p − λ/price) / σ)`, floored at 0.2%. Sure requests go or don't. Unsure ones near the line get sampled. Exploration ends up where it's useful instead of being a flat random share.
- **Log the probability.** Every decision returns the chance it had of going. With that, inverse propensity scoring gives honest offline estimates of other policies from the same logs.
- **Skip copies.** A short memory of which buyers each impression went to, keyed by a fingerprint of publisher, page, slot and device, plus the OpenRTB `source.tid` when it can be trusted.
- **Tell buyers what they didn't get.** A per-buyer report of what was withheld, why, and roughly what it was worth.

[DESIGN.md](DESIGN.md) goes through each step, the maths in plain words, and what breaks when you take it out.

## Using the library

```go
cfg := shape.DefaultConfig()
cfg.Buyers = []shape.Buyer{{Name: "dsp-a", QPS: 5000}, {Name: "dsp-b", QPS: 800}}
s, err := shape.New(cfg)
if err != nil {
	return err
}

// Per incoming request. FromOpenRTB turns one bid request into one
// Request per impression.
reqs, err := shape.FromOpenRTB(body, &publishers, false)
decs := make([]shape.Decision, 0, len(cfg.Buyers))
for i := range reqs {
	decs = s.Decide(&reqs[i], time.Now(), decs)
	for _, d := range decs {
		if d.Send {
			forward(d.Buyer, reqs[i], d.Prop) // keep Prop with the request
		}
	}
}

// When a buyer answers, or times out (report that as no bid):
s.Observe(shape.Outcome{Buyer: b, Features: req.Features, Prop: prop, Bid: bid, Price: cpm, At: time.Now()})
```

`Shaper` is safe for concurrent use. `Decide` doesn't allocate when `decs` has room for every buyer. Log `Decision.Prop` next to each outcome if you want to evaluate other policies offline later; the `ope` package does the estimating. `s.Report(n)` returns what each buyer had withheld and why, with the top `n` segments by estimated value.

Pass `trustTID = true` to `FromOpenRTB` only if every path to your exchange carries the same `source.tid` for the same impression. OpenRTB 2.6 says it must, but wrappers have sent each bidder a different one, so by default only the fingerprint is used.

## Running it

Go 1.24 or later. Everything runs on a laptop CPU.

```
go test -race ./...
go run ./cmd/bidsieve all -out results     # every experiment, about five minutes
go run ./cmd/bidsieve compare              # or one at a time: ablate, dedupe, ope, bench, report, seeds, sweep
go run ./cmd/bidsieve decide -buyers a:100,b:5 < cmd/bidsieve/testdata/requests.jsonl
go test -bench . ./shape
```

`decide` reads OpenRTB 2.x bid requests, one per line, and prints one JSON line per impression and buyer. The model starts empty there, so it shows cold-start behaviour and checks that real requests parse.

## Layout

```
shape/           the library: model, pacer, send rule, dedupe, reports, OpenRTB input
ope/             streaming IPS, SNIPS and doubly robust estimators
sim/             the simulated exchange, baseline policies and experiments
cmd/bidsieve/    the command behind every number in results/
internal/svg/    the line charts in results/
results/         generated output: markdown tables, CSV series, SVG charts
```

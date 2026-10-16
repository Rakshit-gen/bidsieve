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

# How bidsieve decides

This is the long version of the README's "the idea" section. It walks through what happens to one request, why each piece is there, and what breaks if you take it out. The numbers quoted come from `results/`.

## The job

An exchange gets an impression and has up to a few dozen buyers it could forward it to. Each buyer has agreed to take at most some number of requests per second. Most requests come back without a bid, so the exchange wants to spend each buyer's budget on the requests that buyer is most likely to bid well on.

For every (request, buyer) pair the shaper answers one question: send it or not. It has to answer in a few microseconds, and it has to keep answering well when buyers change their minds.

## Step 1: what does this buyer think of requests like this one?

A request is described by six fields: publisher, format, country, device, channel (web or app) and position. The shaper keeps counts for the buyer at six levels, each one finer than the last:

1. everything this buyer has been sent
2. by format
3. by format and country
4. by format, country and device
5. by format, country, device, channel and position
6. all of that plus the publisher

Each level stores three faded sums: requests sent, bids received, and total bid price.

To estimate a request, the shaper starts at the top and works down. At each level it blends that level's counts with the estimate from the level above, treating the level above as if it were a few requests of evidence:

```
p_level = (bids + m * p_parent) / (sent + m)
m       = min(PriorStrength, evidence behind p_parent)
```

`PriorStrength` is 20. So a publisher the shaper has seen 5 times leans mostly on its segment, and one it has seen 5,000 times mostly speaks for itself. A brand-new publisher gets the estimate for its segment straight away instead of starting at zero. That's why bidsieve catches 60.6% of buyers' bids on new publishers in their first minute, against 62.2% for the oracle.

The cap on `m` matters. If the parent itself has faded down to 3 requests of evidence, it can't lend 20. Without that cap a faded parent would hand its children false confidence.

The price estimate uses the same blend, with bids as the weight instead of requests.

## Step 2: how sure are we?

Alongside `p`, the shaper keeps `N`, the effective number of requests behind it. From that it gets a standard error:

```
sigma = sqrt(p * (1 - p) / (N + 1))
```

A cell with lots of recent evidence has a small sigma. A cell that hasn't been touched in a while has a large one, because its counts have faded.

## Step 3: fading

Every count loses half its weight every `HalfLife` (2 minutes by default). This is the piece that keeps a shaper from starving itself.

Here's the trap history-based shapers fall into. A buyer ignores some segment for a while. The shaper learns that, stops sending it, and so never sees the buyer's answer change. If the buyer starts a campaign on that segment, the shaper has no way to find out. In the standard scenario `dsp02` starts a European video campaign at t=480s. Segment history, which only learns from what it sends, catches 0.0% of those bids at every stage. With fading, the old "no" loses weight, sigma grows, and the segment starts getting sampled again on its own. bidsieve catches 96.3% of the new campaign's bids in minutes 1 to 4.

Turning fading off costs only 0.9% of revenue across the whole run, which looks harmless. The European video capture in minutes 1 to 4 drops from 96.3% to 40.8%. Overall revenue hides the starvation because it's one segment out of many.

Fading is done with forward decay. Instead of shrinking every cell every second, a sample at time `t` is stored scaled up by `e^(k(t - t0))` for a fixed landmark `t0`, and a read at `now` scales the sum down by `e^(-k(now - t0))`. Every cell fades by the same factor, so one request pays for one `exp` call no matter how many cells it touches, and cells don't need timestamps. When stored values have grown by `e^300` the landmark moves forward and each shard rescales once, long before a float64 could overflow.

A slow sweep, one shard per second, deletes cells whose weight has dropped below 0.001 requests, so tuples that stop showing up don't stay in memory forever.

## Step 4: what's a request worth to send?

Each buyer has a price per request, `lambda`, in the same units as expected bid value (probability of bid times CPM). A request is worth sending when its expected value beats the price.

The price comes from the pacer. Once a second it looks at how many requests wanted to go to this buyer in the last second and compares that with the target, 95% of the cap:

```
lambda_next = lambda * clamp(demand / target, 0.25, 4) ^ 0.6
```

Too much demand pushes the price up, which filters out the weakest requests. Too little brings it down. That is the online version of a knapsack problem: fill the cap with the most valuable requests without ever sorting the stream. The exponent 0.6 and the clamp stop the price from swinging wildly when traffic doubles, as it does at t=960s in the standard scenario.

On top of the price, a hard per-second counter stops a buyer from ever getting more than its cap, whatever the scoring does.

## Step 5: send with some probability

The shaper doesn't send everything above the line and drop everything below it. It sends with the probability that the request's true bid rate is above the break-even rate, under a normal approximation:

```
q = Phi((p - lambda / price) / sigma)
q = max(q, MinExplore)
```

A request the shaper is sure about goes almost every time or almost never. A request it's unsure about goes some of the time, more often the closer it is to the line. This is probability matching, the same idea as Thompson sampling, done in closed form so it costs one `erfc`. When `|z| > 5` the answer is 0 or 1 to within 3e-7 and the `erfc` is skipped.

The floor, `MinExplore = 0.002`, keeps every request's chance above zero. It costs very little volume and it's what makes the logs usable for offline evaluation (step 7).

Compare the alternative most shapers use: send by a hard threshold, then forward a fixed random share to learn. With a 5% random share, bidsieve loses 33.3% of its revenue. Uniform exploration spends the budget on requests that are clearly bad, while probability matching spends it on requests that are close to the line.

The send draw is a hash of the request ID and the buyer, so the same request gets the same answer for the same model state. Runs are reproducible and two threads can't disagree.

## Step 6: learning from what was sent

When a buyer answers, the shaper adds the outcome to every level. On the finest level each outcome counts once. On the coarser levels it's weighted by `1 / q`, capped at 10.

The reason: a coarse level mixes tuples that were sent at very different rates. If the shaper loves one tuple and sends it every time, and ignores another and sends it 1 time in 500, plain counts at the parent would describe mostly the loved tuple. Weighting by `1 / q` makes the parent an estimate of all the traffic under it, not just the traffic the shaper liked. Within one full tuple, which requests went depends only on the tuple and the past, so plain counts are already fair there.

In this simulator the weights barely matter: switching them off costs 0.4% of revenue. That's an honest result. The prior strength is small enough that the parents rarely dominate a well-observed child, so a biased parent doesn't hurt much. The weights are kept because they're cheap and make the coarse estimates mean what they say.

## Step 7: duplicates

The same impression often reaches an exchange more than once, through different wrappers and resellers. Sending a buyer a second copy wastes its budget on a request it has already answered.

The shaper keeps a short memory of which buyers each impression went to, under two keys:

- a fingerprint of publisher, page or app bundle, slot and device ID, matched within 300 ms
- the OpenRTB `source.tid`, matched within 10 s, but only when the caller says the tid is shared across paths

The tid would be enough on its own if everyone followed OpenRTB 2.6, which says it must be common across all participants. In practice wrappers have sent each bidder a different tid, so a tid match proves a duplicate and a mismatch proves nothing. The fingerprint does most of the work.

The fingerprint window is a trade-off. A real page reload within the window looks like a copy and gets skipped. At 300 ms, 99.8% of skips are real copies and 98.1% of copies are caught. At 2 s, recall reaches 100% but precision falls to 94.6%. With dedupe off, 1.44M copies go to buyers that already had the impression, and revenue drops 17.7%.

Memory is bounded without a background goroutine: each shard keeps two generations of maps, each as long as the window, and drops the older one when time moves on.

## Offline evaluation

Every decision comes with `Prop`, the probability it had of being sent. Log that next to the outcome and you can estimate what a different policy would have earned from the same logs, using inverse propensity scoring (IPS) or the doubly robust (DR) estimator that also uses the model's own prediction.

This only works when every request had a non-zero chance of going. With bidsieve's logs, IPS and DR land within 0.6% of the truth for the frozen-model targets, with DR within 0.1%. Naive replay, counting only the requests that were sent, overstates by up to 252%. With segment history's logs, 11% to 78.5% of what the target would send was never possible under the logger, and IPS is off by 8% to 28%. Nothing can fix that afterwards: the data simply isn't there.

There's one subtlety. A request the policy chose to send can still be refused because the buyer's cap ran out that second. Its real chance of going was `q` times the share of that second's wanted sends the buyer accepted. The evaluation multiplies that in once the second is over.

## What it costs

On an Apple M4, a decision across 10 buyers takes 2.83 µs at the median and 5.12 µs at p99. Feeding back one outcome takes 303 ns. One goroutine handles 308k decisions per second; four handle 815k. Eight do worse than four (730k), because the M4's extra cores are efficiency cores. After ten simulated minutes the model holds 86k cells in 20.5 MB of heap.

## What this design doesn't do

- It scores each buyer on its own. Two buyers who would bid on the same impression are both counted, even though only one can win. A shaper that knew the auction would send the second one less.
- It doesn't model price uncertainty, only bid-rate uncertainty. A segment where prices changed but bid rates didn't is relearned only as fast as the price average fades.
- Feedback in the simulator is immediate. A real buyer answers within a timeout, and some answers never come back. The API asks for timeouts to be reported as no bid.

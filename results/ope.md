## Offline evaluation

900 seconds without duplicates or events. At t=450s bidsieve's model is frozen; the targets below are fixed rules on that frozen model. Their true value is summed from the world; the estimates use only the logs from t=450s on. Values are dollars of bid value received by buyers, before caps.

Logged by **bidsieve** (6743860 request-buyer samples):

| target | sends/s (of caps) | truth | IPS | DR | SNIPS | naive replay | target sends the logger could never make |
|---|---:|---:|---:|---:|---:|---:|---:|
| frozen model, same price | 2942 (85%) | $402 | $402 ± 4 (-0.1%) | $402 ± 4 (-0.0%) | $402 (-0.1%) | $440 (+9.3%) | 0.0% |
| frozen model, half price | 3976 (115%) | $476 | $473 ± 7 (-0.6%) | $475 ± 7 (-0.1%) | $475 (-0.1%) | $570 (+19.9%) | 0.0% |
| frozen model, double price | 2201 (64%) | $341 | $341 ± 4 (+0.0%) | $341 ± 4 (+0.1%) | $341 (+0.0%) | $355 (+4.1%) | 0.0% |
| random 30% | 4496 (130%) | $160 | $155 ± 4 (-3.4%) | $155 ± 4 (-2.9%) | $156 (-2.6%) | $563 (+251.8%) | 0.0% |
| send everything | 14986 (434%) | $533 | $515 ± 12 (-3.4%) | $518 ± 12 (-2.9%) | $520 (-2.6%) | $1877 (+251.8%) | 0.0% |

Logged by **segment history** (6743860 request-buyer samples):

| target | sends/s (of caps) | truth | IPS | DR | SNIPS | naive replay | target sends the logger could never make |
|---|---:|---:|---:|---:|---:|---:|---:|
| frozen model, same price | 2942 (85%) | $402 | $357 ± 4 (-11.3%) | $357 ± 4 (-11.3%) | $449 (+11.6%) | $447 (+11.2%) | 20.5% |
| frozen model, half price | 3976 (115%) | $476 | $378 ± 4 (-20.5%) | $378 ± 4 (-20.5%) | $560 (+17.7%) | $558 (+17.3%) | 32.5% |
| frozen model, double price | 2201 (64%) | $341 | $316 ± 3 (-7.4%) | $316 ± 3 (-7.4%) | $355 (+4.0%) | $354 (+3.6%) | 11.0% |
| random 30% | 4496 (130%) | $160 | $116 ± 1 (-27.5%) | $116 ± 1 (-27.5%) | $540 (+237.7%) | $542 (+238.6%) | 78.5% |
| send everything | 14986 (434%) | $533 | $387 ± 4 (-27.5%) | $387 ± 4 (-27.5%) | $1801 (+237.7%) | $1806 (+238.6%) | 78.5% |

± is a 95% interval (1.96 standard errors).


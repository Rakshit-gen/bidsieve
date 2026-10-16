## Scenario: standard

1200 seconds, 1500 impressions/s, 400 publishers (+60 new at t=720s), 10 buyers, traffic doubles for 60s at t=960s. Impressions arrive through up to four paths; 35% of copies carry a shared tid, 3% of page views are reloaded within two seconds.

| policy | revenue | of ceiling | of oracle | bid rate | value per 1k sent | cap used | sent to a buyer that had it | over-cap refusals |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| clairvoyant | $1678 | 99.6% | 120.7% | 100.0% | $2.15 | 21.8% | 0.0% | 9467 |
| oracle | $1390 | 82.5% | 100.0% | 14.7% | $0.41 | 92.4% | 0.0% | 225201 |
| bidsieve | $1328 | 78.8% | 95.5% | 13.8% | $0.38 | 93.3% | 2.1% | 199707 |
| segment history+control | $770 | 45.7% | 55.4% | 7.9% | $0.26 | 78.4% | 45.1% | 1990962 |
| segment history | $819 | 48.6% | 58.9% | 7.9% | $0.25 | 87.5% | 45.4% | 2434571 |
| tuple history+control | $466 | 27.7% | 33.5% | 6.2% | $0.17 | 69.6% | 42.3% | 9547516 |
| tuple history | $466 | 27.7% | 33.5% | 6.5% | $0.18 | 68.5% | 45.4% | 9552560 |
| random | $309 | 18.3% | 22.2% | 3.9% | $0.08 | 94.3% | 10.7% | 75623 |

When dsp02 starts bidding on European video at t=480s, and dsp05 stops bidding on US/Indian mobile banners:

| policy | new segment caught, first minute | minutes 1-4 | minutes 4-8 | time to 80% of oracle | wasted sends/s to the dead segment |
|---|---:|---:|---:|---:|---:|
| clairvoyant | 100.0% | 100.0% | 100.0% | 30s | 11.0 |
| oracle | 99.2% | 99.5% | 99.8% | 30s | 33.8 |
| bidsieve | 55.0% | 96.3% | 99.8% | 80s | 105.9 |
| segment history+control | 0.6% | 38.7% | 51.8% | never | 76.3 |
| segment history | 0.0% | 0.0% | 0.0% | never | 137.3 |
| tuple history+control | 0.6% | 1.6% | 3.7% | never | 55.6 |
| tuple history | 0.0% | 0.0% | 0.0% | never | 71.6 |
| random | 14.4% | 14.9% | 16.6% | never | 39.2 |

When 60 new publishers go live at t=720s (share of the bids buyers would make on them that actually happened):

| policy | first minute | first four minutes |
|---|---:|---:|
| clairvoyant | 100.0% | 100.0% |
| oracle | 62.2% | 62.9% |
| bidsieve | 60.6% | 59.8% |
| segment history+control | 26.5% | 26.9% |
| segment history | 33.4% | 39.0% |
| tuple history+control | 1.0% | 1.0% |
| tuple history | 0.0% | 0.0% |
| random | 17.2% | 16.8% |

| policy | simulated requests per second of wall time |
|---|---:|
| clairvoyant | 498325 |
| oracle | 485237 |
| bidsieve | 179052 |
| segment history+control | 382722 |
| segment history | 389623 |
| tuple history+control | 332584 |
| tuple history | 332037 |
| random | 461468 |

## Scenario: single-path

1200 seconds, 1500 impressions/s, 400 publishers (+60 new at t=720s), 10 buyers, traffic doubles for 60s at t=960s. Impressions arrive once.

| policy | revenue | of ceiling | of oracle | bid rate | value per 1k sent | cap used | sent to a buyer that had it | over-cap refusals |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| clairvoyant | $1621 | 99.5% | 120.1% | 100.0% | $2.14 | 21.2% | 0.0% | 4930 |
| oracle | $1349 | 82.8% | 100.0% | 14.5% | $0.39 | 93.3% | 0.0% | 130364 |
| bidsieve | $1296 | 79.6% | 96.1% | 14.0% | $0.37 | 94.0% | 0.0% | 103507 |
| segment history+control | $1101 | 67.6% | 81.6% | 13.3% | $0.34 | 87.7% | 0.0% | 842857 |
| segment history | $1097 | 67.4% | 81.3% | 12.8% | $0.33 | 89.7% | 0.0% | 966989 |
| tuple history+control | $717 | 44.0% | 53.2% | 10.9% | $0.25 | 77.7% | 0.0% | 3544282 |
| tuple history | $705 | 43.3% | 52.2% | 11.3% | $0.26 | 74.2% | 0.0% | 3531195 |
| random | $342 | 21.0% | 25.4% | 4.2% | $0.09 | 94.5% | 0.0% | 51494 |

When dsp02 starts bidding on European video at t=480s, and dsp05 stops bidding on US/Indian mobile banners:

| policy | new segment caught, first minute | minutes 1-4 | minutes 4-8 | time to 80% of oracle | wasted sends/s to the dead segment |
|---|---:|---:|---:|---:|---:|
| clairvoyant | 100.0% | 100.0% | 100.0% | 30s | 10.6 |
| oracle | 99.3% | 98.9% | 99.4% | 30s | 32.4 |
| bidsieve | 21.3% | 74.8% | 99.5% | 150s | 115.8 |
| segment history+control | 1.7% | 60.1% | 54.1% | never | 103.0 |
| segment history | 0.0% | 0.0% | 0.0% | never | 144.6 |
| tuple history+control | 1.5% | 2.2% | 5.0% | never | 118.9 |
| tuple history | 0.0% | 0.0% | 0.0% | never | 118.0 |
| random | 22.9% | 27.2% | 27.7% | never | 38.5 |

When 60 new publishers go live at t=720s (share of the bids buyers would make on them that actually happened):

| policy | first minute | first four minutes |
|---|---:|---:|
| clairvoyant | 100.0% | 100.0% |
| oracle | 65.6% | 65.0% |
| bidsieve | 61.1% | 60.3% |
| segment history+control | 56.3% | 55.5% |
| segment history | 50.7% | 57.6% |
| tuple history+control | 4.8% | 4.6% |
| tuple history | 4.1% | 3.7% |
| random | 19.2% | 19.3% |

| policy | simulated requests per second of wall time |
|---|---:|
| clairvoyant | 259967 |
| oracle | 251857 |
| bidsieve | 114072 |
| segment history+control | 213400 |
| segment history | 213730 |
| tuple history+control | 181463 |
| tuple history | 179387 |
| random | 243657 |


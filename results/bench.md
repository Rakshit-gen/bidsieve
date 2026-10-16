## Decision cost

Machine: Apple M4, 10 cores, go1.25.4. The model was warmed on 600 simulated seconds of the standard scenario and holds 86158 cells (20.5 MB heap in use, about 238 bytes per cell including maps). Each Decide covers all 10 buyers.

| | p50 | p90 | p99 | p99.9 | max |
|---|---:|---:|---:|---:|---:|
| Decide, one goroutine (200000 calls) | 3.08 µs | 3.88 µs | 6.83 µs | 11.08 µs | 82.04 µs |

Observe, one outcome across six levels: 313 ns.

Many goroutines sharing one shaper, each replaying its own slice of the requests:

| goroutines | decisions/s | p50 | p99 |
|---|---:|---:|---:|
| 1 | 297635 | 3.12 µs | 6.54 µs |
| 2 | 470625 | 4.08 µs | 6.08 µs |
| 4 | 779612 | 4.83 µs | 11.29 µs |
| 8 | 654764 | 11.42 µs | 17.33 µs |

Timings include `time.Now()` around each call, which costs a few tens of nanoseconds on this machine.


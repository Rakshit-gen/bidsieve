## Decision cost

Machine: Apple M4, 10 cores, go1.25.4. The model was warmed on 600 simulated seconds of the standard scenario and holds 86158 cells (20.5 MB heap in use, about 238 bytes per cell including maps). Each Decide covers all 10 buyers.

| | p50 | p90 | p99 | p99.9 | max |
|---|---:|---:|---:|---:|---:|
| Decide, one goroutine (200000 calls) | 2.83 µs | 3.42 µs | 5.12 µs | 9.12 µs | 418.67 µs |

Observe, one outcome across six levels: 303 ns.

Many goroutines sharing one shaper, each replaying its own slice of the requests:

| goroutines | decisions/s | p50 | p99 |
|---|---:|---:|---:|
| 1 | 307730 | 3.12 µs | 5.00 µs |
| 2 | 473755 | 4.12 µs | 5.17 µs |
| 4 | 814976 | 4.79 µs | 6.00 µs |
| 8 | 729714 | 10.46 µs | 15.17 µs |

Timings include `time.Now()` around each call, which costs a few tens of nanoseconds on this machine.


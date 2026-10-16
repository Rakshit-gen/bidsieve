## Duplicate detection (standard scenario)

A copy is a duplicate if its fingerprint (publisher, page, slot, device) was seen within the window, or its shared tid was seen in the last 10 seconds. Precision counts reloads wrongly skipped; recall counts copies that still got through.

| window | revenue | requests sent | copies sent to a buyer that had it | real impressions skipped | precision | recall |
|---|---:|---:|---:|---:|---:|---:|
| off | $1093 | 3712708 | 1440806 | 0 | 0.0% | 0.0% |
| 25ms | $1151 | 3748245 | 1191281 | 121 | 100.0% | 43.6% |
| 100ms | $1259 | 3852576 | 697801 | 1249 | 100.0% | 79.0% |
| 300ms | $1328 | 3861175 | 82628 | 10667 | 99.8% | 98.1% |
| 600ms | $1331 | 3861598 | 109 | 51308 | 98.9% | 100.0% |
| 1s | $1326 | 3861880 | 148 | 110758 | 97.6% | 100.0% |
| 2s | $1309 | 3860320 | 110 | 258538 | 94.6% | 100.0% |


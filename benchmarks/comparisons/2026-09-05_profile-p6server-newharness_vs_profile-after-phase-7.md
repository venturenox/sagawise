# profile-p6server-newharness vs profile-after-phase-7

| | A: profile-p6server-newharness | B: profile-after-phase-7 |
|---|---|---|
| run | `2026-09-05_0821_a89cf09_profile-p6server-newharness` | `2026-09-05_0818_a89cf09_profile-after-phase-7` |
| commit | `a89cf09` | `a89cf09` |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Profile: knee

| | A | B | change |
|---|---|---|---|
| knee sagas/s | 2277 | 2277 | +0% |
| breach reason | achieved 2772 < 90% of target 3415 (load generator or server saturated) | achieved 2968 < 90% of target 3415 (load generator or server saturated) | |

## Profile: ramp at common rates (consume p99 ms, redis cpu %)

| rate | A p99 | B p99 | change | A redis cpu | B redis cpu |
|---|---|---|---|---|---|
| 200 | 1.2 | 1.2 | +0% | 26 | 24 |
| 300 | 1.4 | 1.4 | -3% | 33 | 32 |
| 450 | 1.5 | 1.4 | -3% | 43 | 41 |
| 675 | 1.7 | 1.8 | +2% | 55 | 52 |
| 1012 | 2.0 | 2.5 | +26% | 66 | 60 |
| 1518 | 2.5 | 3.1 | +23% | 75 | 70 |
| 2277 | 4.5 | 4.4 | -2% | 82 | 76 |
| 3415 | 6.2 | 4.7 | -24% | 89 | 86 |

## Profile: Redis round-trips per request

| endpoint | A | B | change |
|---|---|---|---|
| start | 2.0 | 2.0 | -1% |
| publish | 7.0 | 6.0 | -14% |
| consume | 6.0 | 5.0 | -17% |
| consume(final) | 13.1 | 11.1 | -15% |

## Profile: instances in Redis (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 0 | 0.9 / 1.2 | 0.9 / 1.3 | +2% |
| 10000 | 0.9 / 1.2 | 0.9 / 1.2 | +1% |
| 100000 | 0.9 / 1.2 | 0.9 / 1.1 | -2% |

## Profile: tasks per workflow (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 2 | 0.9 / 1.2 | 0.9 / 1.2 | -4% |
| 10 | 0.9 / 1.2 | 0.9 / 1.2 | +2% |
| 50 | 1.0 / 1.4 | 0.9 / 1.2 | -6% |

## Profile: payload size (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 100B | 0.9 / 1.3 | 0.9 / 1.3 | -5% |
| 10000B | 0.9 / 1.2 | 0.9 / 1.2 | -5% |
| 500000B | 1.4 / 1.8 | 1.2 / 1.6 | -14% |

## Profile: simultaneous timeouts (max lag ms)

| tasks | A | B | change |
|---|---|---|---|
| 100 | 398 | 312 | -21% |
| 500 | 687 | 515 | -25% |
| 2000 | 2295 | 1248 | -46% |

## Profile: contention (same-instance p50 ms)

| A | B | change |
|---|---|---|
| 4.1 | 2.8 | -30% |

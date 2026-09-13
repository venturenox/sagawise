# profile-after-phase-6 vs profile-after-phase-7

| | A: profile-after-phase-6 | B: profile-after-phase-7 |
|---|---|---|
| run | `2026-09-05_0741_84161db_profile-after-phase-6` | `2026-09-05_0818_a89cf09_profile-after-phase-7` |
| commit | `84161db` | `a89cf09` |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Profile: knee

| | A | B | change |
|---|---|---|---|
| knee sagas/s | 1518 | 2277 | +50% |
| breach reason | achieved 1815 < 90% of target 2277 (load generator or server saturated) | achieved 2968 < 90% of target 3415 (load generator or server saturated) | |

## Profile: ramp at common rates (consume p99 ms, redis cpu %)

| rate | A p99 | B p99 | change | A redis cpu | B redis cpu |
|---|---|---|---|---|---|
| 200 | 1.3 | 1.2 | -8% | 29 | 24 |
| 300 | 1.5 | 1.4 | -6% | 37 | 32 |
| 450 | 2.0 | 1.4 | -28% | 48 | 41 |
| 675 | 2.3 | 1.8 | -24% | 61 | 52 |
| 1012 | 2.8 | 2.5 | -10% | 76 | 60 |
| 1518 | 6.1 | 3.1 | -49% | 83 | 70 |
| 2277 | 15.1 | 4.4 | -70% | 89 | 76 |

## Profile: Redis round-trips per request

| endpoint | A | B | change |
|---|---|---|---|
| start | 6.5 | 2.0 | -69% |
| publish | 11.7 | 6.0 | -48% |
| consume | 10.6 | 5.0 | -53% |
| consume(final) | 13.1 | 11.1 | -15% |

## Profile: instances in Redis (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 0 | 1.0 / 1.5 | 0.9 / 1.3 | -2% |
| 10000 | 1.0 / 1.4 | 0.9 / 1.2 | -9% |
| 100000 | 0.9 / 1.3 | 0.9 / 1.1 | -2% |

## Profile: tasks per workflow (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 2 | 1.0 / 1.3 | 0.9 / 1.2 | -7% |
| 10 | 1.0 / 1.5 | 0.9 / 1.2 | -11% |
| 50 | 1.3 / 2.1 | 0.9 / 1.2 | -30% |

## Profile: payload size (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 100B | 0.9 / 1.3 | 0.9 / 1.3 | -6% |
| 10000B | 1.0 / 1.3 | 0.9 / 1.2 | -10% |
| 500000B | 1.5 / 2.1 | 1.2 / 1.6 | -21% |

## Profile: simultaneous timeouts (max lag ms)

| tasks | A | B | change |
|---|---|---|---|
| 100 | 428 | 312 | -27% |
| 500 | 893 | 515 | -42% |
| 2000 | 1001 | 1248 | +25% |

## Profile: contention (same-instance p50 ms)

| A | B | change |
|---|---|---|
| 6.9 | 2.8 | -59% |

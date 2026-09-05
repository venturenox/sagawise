# profile-after-phase-6 vs profile-after-phase-7

| | A: profile-after-phase-6 | B: profile-after-phase-7 |
|---|---|---|
| run | `2026-09-05_0741_84161db_profile-after-phase-6` | `2026-09-05_0806_d13f921_profile-after-phase-7` |
| commit | `84161db` | `d13f921` |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Profile: knee

| | A | B | change |
|---|---|---|---|
| knee sagas/s | 1518 | 2277 | +50% |
| breach reason | achieved 1815 < 90% of target 2277 (load generator or server saturated) | achieved 2922 < 90% of target 3415 (load generator or server saturated) | |

## Profile: ramp at common rates (consume p99 ms, redis cpu %)

| rate | A p99 | B p99 | change | A redis cpu | B redis cpu |
|---|---|---|---|---|---|
| 200 | 1.3 | 1.3 | -6% | 29 | 24 |
| 300 | 1.5 | 1.3 | -13% | 37 | 31 |
| 450 | 2.0 | 1.5 | -26% | 48 | 41 |
| 675 | 2.3 | 1.7 | -28% | 61 | 52 |
| 1012 | 2.8 | 2.2 | -19% | 76 | 62 |
| 1518 | 6.1 | 2.7 | -55% | 83 | 69 |
| 2277 | 15.1 | 3.8 | -75% | 89 | 75 |

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
| 0 | 1.0 / 1.5 | 0.9 / 1.2 | -9% |
| 10000 | 1.0 / 1.4 | 0.9 / 1.2 | -11% |
| 100000 | 0.9 / 1.3 | 0.9 / 1.2 | -2% |

## Profile: tasks per workflow (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 2 | 1.0 / 1.3 | 0.9 / 1.3 | -8% |
| 10 | 1.0 / 1.5 | 0.9 / 1.3 | -14% |
| 50 | 1.3 / 2.1 | 0.9 / 1.3 | -31% |

## Profile: payload size (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 100B | 0.9 / 1.3 | 0.9 / 1.2 | -4% |
| 10000B | 1.0 / 1.3 | 0.9 / 1.3 | -10% |
| 500000B | 1.5 / 2.1 | 1.1 / 1.6 | -24% |

## Profile: simultaneous timeouts (max lag ms)

| tasks | A | B | change |
|---|---|---|---|
| 100 | 428 | 531 | +24% |
| 500 | 893 | 114 | -87% |
| 2000 | 1001 | 1057 | +6% |

## Profile: contention (same-instance p50 ms)

| A | B | change |
|---|---|---|
| 6.9 | 3.2 | -55% |

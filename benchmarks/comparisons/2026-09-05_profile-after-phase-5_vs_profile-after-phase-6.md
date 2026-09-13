# profile-after-phase-5 vs profile-after-phase-6

| | A: profile-after-phase-5 | B: profile-after-phase-6 |
|---|---|---|
| run | `2026-09-05_0546_2637bcc_profile-after-phase-5` | `2026-09-05_0741_84161db_profile-after-phase-6` |
| commit | `2637bcc` | `84161db` |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Profile: knee

| | A | B | change |
|---|---|---|---|
| knee sagas/s | 1518 | 1518 | +0% |
| breach reason | p99 412 ms > 50 ms | achieved 1815 < 90% of target 2277 (load generator or server saturated) | |

## Profile: ramp at common rates (consume p99 ms, redis cpu %)

| rate | A p99 | B p99 | change | A redis cpu | B redis cpu |
|---|---|---|---|---|---|
| 200 | 4.2 | 1.3 | -68% | 44 | 29 |
| 300 | 5.3 | 1.5 | -72% | 53 | 37 |
| 450 | 5.6 | 2.0 | -65% | 64 | 48 |
| 675 | 7.1 | 2.3 | -67% | 71 | 61 |
| 1012 | 9.7 | 2.8 | -71% | 79 | 76 |
| 1518 | 15.0 | 6.1 | -60% | 90 | 83 |
| 2277 | 411.6 | 15.1 | -96% | 97 | 89 |

## Profile: Redis round-trips per request

| endpoint | A | B | change |
|---|---|---|---|
| start | 2.0 | 6.5 | +225% |
| publish | 8.0 | 11.7 | +46% |
| consume | 7.0 | 10.6 | +51% |
| consume(final) | 11.0 | 13.1 | +19% |

## Profile: instances in Redis (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 0 | 2.7 / 3.6 | 1.0 / 1.5 | -65% |
| 10000 | 2.7 / 3.5 | 1.0 / 1.4 | -64% |
| 100000 | 2.8 / 3.7 | 0.9 / 1.3 | -67% |

## Profile: tasks per workflow (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 2 | 2.7 / 4.1 | 1.0 / 1.3 | -65% |
| 10 | 2.5 / 4.1 | 1.0 / 1.5 | -59% |
| 50 | 4.1 / 6.6 | 1.3 / 2.1 | -68% |

## Profile: payload size (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 100B | 2.8 / 3.5 | 0.9 / 1.3 | -67% |
| 10000B | 2.8 / 3.5 | 1.0 / 1.3 | -64% |
| 500000B | 3.6 / 4.5 | 1.5 / 2.1 | -58% |

## Profile: simultaneous timeouts (max lag ms)

| tasks | A | B | change |
|---|---|---|---|
| 100 | 839 | 428 | -49% |
| 500 | 1080 | 893 | -17% |
| 2000 | 1357 | 1001 | -26% |

## Profile: contention (same-instance p50 ms)

| A | B | change |
|---|---|---|
| 12.0 | 6.9 | -42% |

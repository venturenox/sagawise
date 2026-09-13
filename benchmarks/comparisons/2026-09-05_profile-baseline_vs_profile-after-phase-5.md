# profile-baseline vs profile-after-phase-5

| | A: profile-baseline | B: profile-after-phase-5 |
|---|---|---|
| run | `2026-09-05_0521_8f8e27c_profile-baseline` | `2026-09-05_0546_2637bcc_profile-after-phase-5` |
| commit | `8f8e27c` | `2637bcc` |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Profile: knee

| | A | B | change |
|---|---|---|---|
| knee sagas/s | 1518 | 1518 | +0% |
| breach reason | p99 526 ms > 50 ms | p99 412 ms > 50 ms | |

## Profile: ramp at common rates (consume p99 ms, redis cpu %)

| rate | A p99 | B p99 | change | A redis cpu | B redis cpu |
|---|---|---|---|---|---|
| 200 | 4.3 | 4.2 | -2% | 44 | 44 |
| 300 | 5.4 | 5.3 | -2% | 55 | 53 |
| 450 | 6.4 | 5.6 | -12% | 63 | 64 |
| 675 | 8.5 | 7.1 | -17% | 69 | 71 |
| 1012 | 10.9 | 9.7 | -11% | 77 | 79 |
| 1518 | 18.8 | 15.0 | -20% | 90 | 90 |
| 2277 | 526.1 | 411.6 | -22% | 97 | 97 |

## Profile: Redis round-trips per request

| endpoint | A | B | change |
|---|---|---|---|
| start | 2.0 | 2.0 | +0% |
| publish | 8.0 | 8.0 | -0% |
| consume | 7.0 | 7.0 | +0% |
| consume(final) | 11.0 | 11.0 | +0% |

## Profile: instances in Redis (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 0 | 2.6 / 3.5 | 2.7 / 3.6 | +4% |
| 10000 | 2.7 / 3.7 | 2.7 / 3.5 | +2% |
| 100000 | 2.7 / 3.8 | 2.8 / 3.7 | +2% |

## Profile: tasks per workflow (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 2 | 2.8 / 4.1 | 2.7 / 4.1 | -5% |
| 10 | 2.6 / 4.3 | 2.5 / 4.1 | -6% |
| 50 | 4.6 / 7.3 | 4.1 / 6.6 | -12% |

## Profile: payload size (consume p50 / p99 ms)

| level | A | B | p50 change |
|---|---|---|---|
| 100B | 2.7 / 3.6 | 2.8 / 3.5 | +3% |
| 10000B | 2.8 / 3.7 | 2.8 / 3.5 | +0% |
| 500000B | 3.6 / 4.7 | 3.6 / 4.5 | -1% |

## Profile: simultaneous timeouts (max lag ms)

| tasks | A | B | change |
|---|---|---|---|
| 100 | 783 | 839 | +7% |
| 500 | 1098 | 1080 | -2% |
| 2000 | 1536 | 1357 | -12% |

## Profile: contention (same-instance p50 ms)

| A | B | change |
|---|---|---|
| 13.1 | 12.0 | -8% |

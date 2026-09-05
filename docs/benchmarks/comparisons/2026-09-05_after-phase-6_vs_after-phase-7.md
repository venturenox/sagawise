# after-phase-6 vs after-phase-7

| | A: after-phase-6 | B: after-phase-7 |
|---|---|---|
| run | `2026-09-05_0738_84161db_after-phase-6` | `2026-09-05_0811_b974cf0_after-phase-7` |
| commit | `84161db` | `b974cf0` |
| date | 2026-09-05T07:38:09+05:00 | 2026-09-05T08:11:35+05:00 |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Load (A → B; negative latency change is better)

| rate | metric | A | B | change |
|---|---|---|---|---|
| 50 | achieved sagas/s | 50.0 | 50.0 | -0% |
| 50 | error rate % | 0.00 | 0.00 | n/a |
| 50 | start p50 ms | 1.0 | 0.9 | -4% |
| 50 | start p99 ms | 1.5 | 1.4 | -8% |
| 50 | publish p50 ms | 0.9 | 0.9 | -5% |
| 50 | publish p99 ms | 1.4 | 1.3 | -9% |
| 50 | consume p50 ms | 0.9 | 0.9 | -5% |
| 50 | consume p99 ms | 1.4 | 1.3 | -7% |
| 50 | redis cmds/saga | 37.2 | 32.1 | -14% |
| 50 | archive missing | 0 | 0 | |
| 100 | achieved sagas/s | 100.0 | 100.0 | +0% |
| 100 | error rate % | 0.00 | 0.00 | n/a |
| 100 | start p50 ms | 0.9 | 0.9 | -1% |
| 100 | start p99 ms | 1.3 | 1.3 | -1% |
| 100 | publish p50 ms | 0.9 | 0.9 | -5% |
| 100 | publish p99 ms | 1.3 | 1.2 | -7% |
| 100 | consume p50 ms | 0.9 | 0.9 | -5% |
| 100 | consume p99 ms | 1.3 | 1.2 | -9% |
| 100 | redis cmds/saga | 37.1 | 32.1 | -14% |
| 100 | archive missing | 0 | 0 | |
| 200 | achieved sagas/s | 200.0 | 199.9 | -0% |
| 200 | error rate % | 0.00 | 0.00 | n/a |
| 200 | start p50 ms | 0.9 | 0.9 | -3% |
| 200 | start p99 ms | 1.2 | 1.3 | +5% |
| 200 | publish p50 ms | 0.9 | 0.9 | -4% |
| 200 | publish p99 ms | 1.3 | 1.3 | -1% |
| 200 | consume p50 ms | 0.9 | 0.9 | -4% |
| 200 | consume p99 ms | 1.3 | 1.3 | -1% |
| 200 | redis cmds/saga | 37.0 | 32.0 | -14% |
| 200 | archive missing | 0 | 0 | |

## Reaper lag

| metric | A | B | change |
|---|---|---|---|
| received / tasks | 200 / 200 | 200 / 200 | |
| p50 ms | 189 | 985 | +421% |
| p99 ms | 1001 | 993 | -1% |
| max ms | 3115 | 993 | -68% |

## Go micro-benchmarks (benchstat)

benchstat not installed (`go install golang.org/x/perf/cmd/benchstat@latest`); raw files: `go-bench.txt` in each run directory.

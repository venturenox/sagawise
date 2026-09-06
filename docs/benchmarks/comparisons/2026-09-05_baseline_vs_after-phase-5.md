# baseline vs after-phase-5

| | A: baseline | B: after-phase-5 |
|---|---|---|
| run | `2026-09-05_0517_8f8e27c_baseline` | `2026-09-05_0543_2637bcc_after-phase-5` |
| commit | `8f8e27c` | `2637bcc` |
| date | 2026-09-05T05:17:17+05:00 | 2026-09-05T05:43:24+05:00 |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Load (A → B; negative latency change is better)

| rate | metric | A | B | change |
|---|---|---|---|---|
| 50 | achieved sagas/s | 50.0 | 50.0 | -0% |
| 50 | error rate % | 0.00 | 0.00 | n/a |
| 50 | start p50 ms | 0.9 | 0.9 | -3% |
| 50 | start p99 ms | 1.3 | 1.3 | -2% |
| 50 | publish p50 ms | 2.4 | 2.4 | -0% |
| 50 | publish p99 ms | 3.1 | 2.9 | -5% |
| 50 | consume p50 ms | 2.7 | 2.7 | +1% |
| 50 | consume p99 ms | 3.6 | 3.5 | -2% |
| 50 | redis cmds/saga | 36.0 | 36.0 | -0% |
| 50 | archive missing | 0 | 0 | |
| 100 | achieved sagas/s | 99.9 | 99.9 | -0% |
| 100 | error rate % | 0.00 | 0.00 | n/a |
| 100 | start p50 ms | 0.9 | 0.9 | +2% |
| 100 | start p99 ms | 1.5 | 1.3 | -15% |
| 100 | publish p50 ms | 2.4 | 2.4 | +0% |
| 100 | publish p99 ms | 3.2 | 2.9 | -8% |
| 100 | consume p50 ms | 2.7 | 2.7 | +1% |
| 100 | consume p99 ms | 3.8 | 3.5 | -7% |
| 100 | redis cmds/saga | 36.0 | 36.0 | -0% |
| 100 | archive missing | 0 | 0 | |
| 200 | achieved sagas/s | 199.9 | 199.9 | +0% |
| 200 | error rate % | 0.00 | 0.00 | n/a |
| 200 | start p50 ms | 1.0 | 1.0 | -0% |
| 200 | start p99 ms | 1.5 | 1.4 | -5% |
| 200 | publish p50 ms | 2.7 | 2.7 | -1% |
| 200 | publish p99 ms | 3.5 | 3.4 | -3% |
| 200 | consume p50 ms | 2.9 | 2.9 | +1% |
| 200 | consume p99 ms | 4.2 | 4.1 | -4% |
| 200 | redis cmds/saga | 36.0 | 36.0 | -0% |
| 200 | archive missing | 0 | 0 | |

## Reaper lag

| metric | A | B | change |
|---|---|---|---|
| received / tasks | 200 / 200 | 200 / 200 | |
| p50 ms | 995 | 1010 | +1% |
| p99 ms | 1000 | 1035 | +3% |
| max ms | 1001 | 1035 | +3% |

## Go micro-benchmarks (benchstat)

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
                         │ ../docs/benchmarks/runs/2026-09-05_0517_8f8e27c_baseline/go-bench.txt │ ../docs/benchmarks/runs/2026-09-05_0543_2637bcc_after-phase-5/go-bench.txt │
                         │                                sec/op                                 │                        sec/op                         vs base              │
StartInstance-12                                                                    533.7µ ± ∞ ¹                                           555.0µ ± ∞ ¹  +4.00% (p=0.029 n=4)
Publish-12                                                                          2.015m ± ∞ ¹                                           2.085m ± ∞ ¹       ~ (p=0.200 n=4)
Consume-12                                                                          1.733m ± ∞ ¹                                           1.806m ± ∞ ¹  +4.18% (p=0.029 n=4)
Saga-12                                                                             9.671m ± ∞ ¹                                           9.290m ± ∞ ¹  -3.94% (p=0.029 n=4)
ReaperTick/overdue=10-12                                                            33.97m ± ∞ ¹                                           33.37m ± ∞ ¹  -1.78% (p=0.029 n=4)
ReaperTick/overdue=50-12                                                            166.2m ± ∞ ¹                                           166.6m ± ∞ ¹       ~ (p=0.686 n=4)
geomean                                                                             6.833m                                                 6.899m        +0.97%
¹ need >= 6 samples for confidence interval at level 0.95

                         │ ../docs/benchmarks/runs/2026-09-05_0517_8f8e27c_baseline/go-bench.txt │ ../docs/benchmarks/runs/2026-09-05_0543_2637bcc_after-phase-5/go-bench.txt │
                         │                                 B/op                                  │                         B/op                          vs base              │
StartInstance-12                                                                   16.35Ki ± ∞ ¹                                          16.35Ki ± ∞ ¹       ~ (p=0.771 n=4)
Publish-12                                                                         28.88Ki ± ∞ ¹                                          28.94Ki ± ∞ ¹       ~ (p=1.000 n=4)
Consume-12                                                                         30.17Ki ± ∞ ¹                                          30.18Ki ± ∞ ¹       ~ (p=0.686 n=4)
Saga-12                                                                            154.2Ki ± ∞ ¹                                          155.4Ki ± ∞ ¹  +0.78% (p=0.029 n=4)
ReaperTick/overdue=10-12                                                           545.9Ki ± ∞ ¹                                          523.1Ki ± ∞ ¹  -4.19% (p=0.029 n=4)
ReaperTick/overdue=50-12                                                           2.463Mi ± ∞ ¹                                          2.430Mi ± ∞ ¹       ~ (p=0.343 n=4)
geomean                                                                            120.3Ki                                                119.3Ki        -0.77%
¹ need >= 6 samples for confidence interval at level 0.95

                         │ ../docs/benchmarks/runs/2026-09-05_0517_8f8e27c_baseline/go-bench.txt │ ../docs/benchmarks/runs/2026-09-05_0543_2637bcc_after-phase-5/go-bench.txt │
                         │                               allocs/op                               │                      allocs/op                        vs base              │
StartInstance-12                                                                     199.0 ± ∞ ¹                                            199.0 ± ∞ ¹       ~ (p=1.000 n=4)
Publish-12                                                                           303.0 ± ∞ ¹                                            304.0 ± ∞ ¹  +0.33% (p=0.029 n=4)
Consume-12                                                                           345.0 ± ∞ ¹                                            346.0 ± ∞ ¹       ~ (p=0.114 n=4)
Saga-12                                                                             1.754k ± ∞ ¹                                           1.763k ± ∞ ¹  +0.51% (p=0.029 n=4)
ReaperTick/overdue=10-12                                                            6.719k ± ∞ ¹                                           6.663k ± ∞ ¹  -0.83% (p=0.029 n=4)
ReaperTick/overdue=50-12                                                            32.95k ± ∞ ¹                                           32.89k ± ∞ ¹       ~ (p=0.200 n=4)
geomean                                                                             1.417k                                                 1.417k        +0.02%
¹ need >= 6 samples for confidence interval at level 0.95
```

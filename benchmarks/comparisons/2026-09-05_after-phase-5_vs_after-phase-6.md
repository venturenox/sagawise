# after-phase-5 vs after-phase-6

| | A: after-phase-5 | B: after-phase-6 |
|---|---|---|
| run | `2026-09-05_0543_2637bcc_after-phase-5` | `2026-09-05_0738_84161db_after-phase-6` |
| commit | `2637bcc` | `84161db` |
| date | 2026-09-05T05:43:24+05:00 | 2026-09-05T07:38:09+05:00 |
| machine | AMD Ryzen 9 9900X 12-Core Processor | AMD Ryzen 9 9900X 12-Core Processor |

## Load (A → B; negative latency change is better)

| rate | metric | A | B | change |
|---|---|---|---|---|
| 50 | achieved sagas/s | 50.0 | 50.0 | +0% |
| 50 | error rate % | 0.00 | 0.00 | n/a |
| 50 | start p50 ms | 0.9 | 1.0 | +7% |
| 50 | start p99 ms | 1.3 | 1.5 | +16% |
| 50 | publish p50 ms | 2.4 | 0.9 | -61% |
| 50 | publish p99 ms | 2.9 | 1.4 | -52% |
| 50 | consume p50 ms | 2.7 | 0.9 | -66% |
| 50 | consume p99 ms | 3.5 | 1.4 | -60% |
| 50 | redis cmds/saga | 36.0 | 37.2 | +3% |
| 50 | archive missing | 0 | 0 | |
| 100 | achieved sagas/s | 99.9 | 100.0 | +0% |
| 100 | error rate % | 0.00 | 0.00 | n/a |
| 100 | start p50 ms | 0.9 | 0.9 | -1% |
| 100 | start p99 ms | 1.3 | 1.3 | +3% |
| 100 | publish p50 ms | 2.4 | 0.9 | -62% |
| 100 | publish p99 ms | 2.9 | 1.3 | -54% |
| 100 | consume p50 ms | 2.7 | 0.9 | -66% |
| 100 | consume p99 ms | 3.5 | 1.3 | -62% |
| 100 | redis cmds/saga | 36.0 | 37.1 | +3% |
| 100 | archive missing | 0 | 0 | |
| 200 | achieved sagas/s | 199.9 | 200.0 | +0% |
| 200 | error rate % | 0.00 | 0.00 | n/a |
| 200 | start p50 ms | 1.0 | 0.9 | -8% |
| 200 | start p99 ms | 1.4 | 1.2 | -14% |
| 200 | publish p50 ms | 2.7 | 0.9 | -65% |
| 200 | publish p99 ms | 3.4 | 1.3 | -61% |
| 200 | consume p50 ms | 2.9 | 0.9 | -69% |
| 200 | consume p99 ms | 4.1 | 1.3 | -68% |
| 200 | redis cmds/saga | 36.0 | 37.0 | +3% |
| 200 | archive missing | 0 | 0 | |

## Reaper lag

| metric | A | B | change |
|---|---|---|---|
| received / tasks | 200 / 200 | 200 / 200 | |
| p50 ms | 1010 | 189 | -81% |
| p99 ms | 1035 | 1001 | -3% |
| max ms | 1035 | 3115 | +201% |

## Go micro-benchmarks (benchstat)

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
                         │ ../docs/benchmarks/runs/2026-09-05_0543_2637bcc_after-phase-5/go-bench.txt │ ../docs/benchmarks/runs/2026-09-05_0738_84161db_after-phase-6/go-bench.txt │
                         │                                   sec/op                                   │                       sec/op                         vs base               │
StartInstance-12                                                                         555.0µ ± ∞ ¹                                          615.1µ ± ∞ ¹  +10.84% (p=0.029 n=4)
Publish-12                                                                               2.085m ± ∞ ¹                                          1.250m ± ∞ ¹  -40.04% (p=0.029 n=4)
Consume-12                                                                               1.806m ± ∞ ¹                                          1.172m ± ∞ ¹  -35.10% (p=0.029 n=4)
Saga-12                                                                                  9.290m ± ∞ ¹                                          8.509m ± ∞ ¹   -8.41% (p=0.029 n=4)
ReaperTick/overdue=10-12                                                                 33.37m ± ∞ ¹                                          36.86m ± ∞ ¹  +10.49% (p=0.029 n=4)
ReaperTick/overdue=50-12                                                                 166.6m ± ∞ ¹                                          181.0m ± ∞ ¹   +8.66% (p=0.029 n=4)
geomean                                                                                  6.899m                                                6.093m        -11.69%
¹ need >= 6 samples for confidence interval at level 0.95

                         │ ../docs/benchmarks/runs/2026-09-05_0543_2637bcc_after-phase-5/go-bench.txt │ ../docs/benchmarks/runs/2026-09-05_0738_84161db_after-phase-6/go-bench.txt │
                         │                                    B/op                                    │                        B/op                          vs base               │
StartInstance-12                                                                        16.35Ki ± ∞ ¹                                         15.57Ki ± ∞ ¹   -4.78% (p=0.029 n=4)
Publish-12                                                                              28.94Ki ± ∞ ¹                                         21.09Ki ± ∞ ¹  -27.13% (p=0.029 n=4)
Consume-12                                                                              30.18Ki ± ∞ ¹                                         21.27Ki ± ∞ ¹  -29.51% (p=0.029 n=4)
Saga-12                                                                                 155.4Ki ± ∞ ¹                                         112.6Ki ± ∞ ¹  -27.55% (p=0.029 n=4)
ReaperTick/overdue=10-12                                                                523.1Ki ± ∞ ¹                                         507.9Ki ± ∞ ¹        ~ (p=0.343 n=4)
ReaperTick/overdue=50-12                                                                2.430Mi ± ∞ ¹                                         1.961Mi ± ∞ ¹  -19.29% (p=0.029 n=4)
geomean                                                                                 119.3Ki                                               96.39Ki        -19.23%
¹ need >= 6 samples for confidence interval at level 0.95

                         │ ../docs/benchmarks/runs/2026-09-05_0543_2637bcc_after-phase-5/go-bench.txt │ ../docs/benchmarks/runs/2026-09-05_0738_84161db_after-phase-6/go-bench.txt │
                         │                                 allocs/op                                  │                      allocs/op                       vs base               │
StartInstance-12                                                                          199.0 ± ∞ ¹                                           161.0 ± ∞ ¹  -19.10% (p=0.029 n=4)
Publish-12                                                                                304.0 ± ∞ ¹                                           206.0 ± ∞ ¹  -32.24% (p=0.029 n=4)
Consume-12                                                                                346.0 ± ∞ ¹                                           214.0 ± ∞ ¹  -38.15% (p=0.029 n=4)
Saga-12                                                                                  1.763k ± ∞ ¹                                          1.124k ± ∞ ¹  -36.25% (p=0.029 n=4)
ReaperTick/overdue=10-12                                                                 6.663k ± ∞ ¹                                          4.173k ± ∞ ¹  -37.37% (p=0.029 n=4)
ReaperTick/overdue=50-12                                                                 32.89k ± ∞ ¹                                          19.71k ± ∞ ¹  -40.07% (p=0.029 n=4)
geomean                                                                                  1.417k                                                 932.2        -34.20%
¹ need >= 6 samples for confidence interval at level 0.95
```

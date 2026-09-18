# Benchmark: after-phase-7

- **Date:** 2026-09-05T08:11:35+05:00
- **Commit:** `b974cf0`
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 0.9 / 1.4 | 0.9 / 1.3 | 0.9 / 1.3 | 32.1 | 1000 / 1000 |
| 100 | 100.0 | 10000 | 0 (0.00%) | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 / 1.2 | 32.1 | 2000 / 2000 |
| 200 | 199.9 | 20000 | 0 (0.00%) | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.3 | 32.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 0.9 | 1.2 | 1.4 | 1.6 |
| 50 | publish | 2000 | 0.9 | 1.1 | 1.3 | 4.9 |
| 50 | consume | 2000 | 0.9 | 1.0 | 1.3 | 4.1 |
| 100 | start | 2000 | 0.9 | 1.1 | 1.3 | 1.8 |
| 100 | publish | 4000 | 0.9 | 1.0 | 1.2 | 5.5 |
| 100 | consume | 4000 | 0.9 | 1.0 | 1.2 | 3.0 |
| 200 | start | 4000 | 0.9 | 1.1 | 1.3 | 3.4 |
| 200 | publish | 8000 | 0.9 | 1.1 | 1.3 | 4.1 |
| 200 | consume | 8000 | 0.9 | 1.1 | 1.3 | 3.6 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | -28 | 985 | 993 | 993 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    552758 ns/op	   15820 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    549075 ns/op	   15942 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    545271 ns/op	   15808 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    559225 ns/op	   15955 B/op	     161 allocs/op
BenchmarkPublish-12          	      50	   1094326 ns/op	   21626 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1097260 ns/op	   21628 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1089577 ns/op	   21625 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1096598 ns/op	   21628 B/op	     206 allocs/op
BenchmarkConsume-12          	      50	   1099113 ns/op	   21816 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1110899 ns/op	   21816 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1077602 ns/op	   21816 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1056979 ns/op	   21816 B/op	     214 allocs/op
BenchmarkSaga-12             	      50	   8106895 ns/op	  114989 B/op	    1123 allocs/op
BenchmarkSaga-12             	      50	   8283262 ns/op	  115352 B/op	    1124 allocs/op
BenchmarkSaga-12             	      50	   8228410 ns/op	  115582 B/op	    1125 allocs/op
BenchmarkSaga-12             	      50	   8320752 ns/op	  115101 B/op	    1123 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33461200 ns/op	  526590 B/op	    3703 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33421809 ns/op	  486450 B/op	    3670 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33931685 ns/op	  454600 B/op	    3676 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33558949 ns/op	  444931 B/op	    3655 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 159576608 ns/op	 1900604 B/op	   17251 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 160877593 ns/op	 1892073 B/op	   17364 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 159759037 ns/op	 1842677 B/op	   17278 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 160564266 ns/op	 1870023 B/op	   17337 allocs/op
```

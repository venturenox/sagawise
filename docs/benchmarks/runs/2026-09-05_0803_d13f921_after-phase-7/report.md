# Benchmark: after-phase-7

- **Date:** 2026-09-05T08:03:55+05:00
- **Commit:** `d13f921`
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 0.9 / 1.4 | 0.9 / 1.3 | 0.9 / 1.2 | 32.1 | 1000 / 1000 |
| 100 | 100.0 | 10000 | 0 (0.00%) | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.2 | 32.1 | 2000 / 2000 |
| 200 | 200.0 | 20000 | 0 (0.00%) | 0.8 / 1.2 | 0.9 / 1.3 | 0.9 / 1.3 | 32.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 0.9 | 1.2 | 1.4 | 2.2 |
| 50 | publish | 2000 | 0.9 | 1.1 | 1.3 | 5.4 |
| 50 | consume | 2000 | 0.9 | 1.0 | 1.2 | 1.6 |
| 100 | start | 2000 | 0.9 | 1.1 | 1.2 | 2.8 |
| 100 | publish | 4000 | 0.9 | 1.0 | 1.2 | 3.4 |
| 100 | consume | 4000 | 0.9 | 1.0 | 1.2 | 1.9 |
| 200 | start | 4000 | 0.8 | 1.1 | 1.2 | 4.6 |
| 200 | publish | 8000 | 0.9 | 1.1 | 1.3 | 4.4 |
| 200 | consume | 8000 | 0.9 | 1.1 | 1.3 | 4.0 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | 12 | 183 | 1002 | 1007 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    557280 ns/op	   15944 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    556536 ns/op	   15942 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    565511 ns/op	   15944 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    560711 ns/op	   16079 B/op	     162 allocs/op
BenchmarkPublish-12          	      50	   1137390 ns/op	   21625 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1082126 ns/op	   21625 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1107539 ns/op	   21626 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1082380 ns/op	   21629 B/op	     206 allocs/op
BenchmarkConsume-12          	      50	   1099112 ns/op	   21816 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1079237 ns/op	   21816 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1069124 ns/op	   21816 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1101152 ns/op	   21816 B/op	     214 allocs/op
BenchmarkSaga-12             	      50	   8239542 ns/op	  115562 B/op	    1125 allocs/op
BenchmarkSaga-12             	      50	   8262885 ns/op	  115186 B/op	    1123 allocs/op
BenchmarkSaga-12             	      50	   7834775 ns/op	  115413 B/op	    1125 allocs/op
BenchmarkSaga-12             	      50	   8105204 ns/op	  115109 B/op	    1123 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  34175678 ns/op	  524827 B/op	    3711 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33612435 ns/op	  497769 B/op	    3700 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33999497 ns/op	  451465 B/op	    3667 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33488818 ns/op	  455275 B/op	    3673 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 157512148 ns/op	 1919045 B/op	   17279 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 160257096 ns/op	 1889964 B/op	   17321 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 160836368 ns/op	 1873348 B/op	   17323 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 160082008 ns/op	 1826572 B/op	   17211 allocs/op
```

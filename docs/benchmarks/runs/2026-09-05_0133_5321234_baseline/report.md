# Benchmark: baseline

- **Date:** 2026-09-05T01:33:29+05:00
- **Commit:** `5321234` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 1.0 / 1.4 | 2.5 / 3.0 | 2.8 / 3.6 | 36.0 | 1000 / 1000 |
| 100 | 99.9 | 10000 | 0 (0.00%) | 0.9 / 1.3 | 2.6 / 3.1 | 2.9 / 3.7 | 36.0 | 2000 / 2000 |
| 200 | 199.9 | 20000 | 0 (0.00%) | 1.0 / 1.6 | 2.9 / 3.7 | 3.1 / 4.4 | 36.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 1.0 | 1.3 | 1.4 | 2.1 |
| 50 | publish | 2000 | 2.5 | 2.8 | 3.0 | 4.0 |
| 50 | consume | 2000 | 2.8 | 3.3 | 3.6 | 4.2 |
| 100 | start | 2000 | 0.9 | 1.1 | 1.3 | 5.2 |
| 100 | publish | 4000 | 2.6 | 2.9 | 3.1 | 7.3 |
| 100 | consume | 4000 | 2.9 | 3.5 | 3.7 | 7.9 |
| 200 | start | 4000 | 1.0 | 1.3 | 1.6 | 6.0 |
| 200 | publish | 8000 | 2.9 | 3.3 | 3.7 | 7.8 |
| 200 | consume | 8000 | 3.1 | 4.0 | 4.4 | 8.6 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | 192 | 1015 | 1040 | 1041 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    605384 ns/op	   16743 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    598537 ns/op	   16744 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    597742 ns/op	   16870 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    622963 ns/op	   16744 B/op	     199 allocs/op
BenchmarkPublish-12          	      50	   2231839 ns/op	   29575 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2233475 ns/op	   29577 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2249602 ns/op	   29574 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2227182 ns/op	   29573 B/op	     303 allocs/op
BenchmarkConsume-12          	      50	   1883493 ns/op	   30728 B/op	     344 allocs/op
BenchmarkConsume-12          	      50	   1910243 ns/op	   30570 B/op	     344 allocs/op
BenchmarkConsume-12          	      50	   1918703 ns/op	   30846 B/op	     345 allocs/op
BenchmarkConsume-12          	      50	   1910235 ns/op	   30940 B/op	     345 allocs/op
BenchmarkSaga-12             	      50	   9861495 ns/op	  158462 B/op	    1758 allocs/op
BenchmarkSaga-12             	      50	   9888117 ns/op	  158401 B/op	    1756 allocs/op
BenchmarkSaga-12             	      50	   9858917 ns/op	  158318 B/op	    1757 allocs/op
BenchmarkSaga-12             	      50	   9859131 ns/op	  158259 B/op	    1757 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35181936 ns/op	  563098 B/op	    6720 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35022915 ns/op	  553563 B/op	    6708 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35166860 ns/op	  562513 B/op	    6727 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35331524 ns/op	  564624 B/op	    6723 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 176638088 ns/op	 2585890 B/op	   32969 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 176087054 ns/op	 2579717 B/op	   32957 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 177123566 ns/op	 2585044 B/op	   33011 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 177692442 ns/op	 2582086 B/op	   32962 allocs/op
```

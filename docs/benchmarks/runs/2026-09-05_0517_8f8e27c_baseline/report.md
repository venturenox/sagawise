# Benchmark: baseline

- **Date:** 2026-09-05T05:17:17+05:00
- **Commit:** `8f8e27c` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 0.9 / 1.3 | 2.4 / 3.1 | 2.7 / 3.6 | 36.0 | 1000 / 1000 |
| 100 | 99.9 | 10000 | 0 (0.00%) | 0.9 / 1.5 | 2.4 / 3.2 | 2.7 / 3.8 | 36.0 | 2000 / 2000 |
| 200 | 199.9 | 20000 | 0 (0.00%) | 1.0 / 1.5 | 2.7 / 3.5 | 2.9 / 4.2 | 36.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 0.9 | 1.2 | 1.3 | 3.4 |
| 50 | publish | 2000 | 2.4 | 2.7 | 3.1 | 5.9 |
| 50 | consume | 2000 | 2.7 | 3.3 | 3.6 | 4.1 |
| 100 | start | 2000 | 0.9 | 1.1 | 1.5 | 4.7 |
| 100 | publish | 4000 | 2.4 | 2.8 | 3.2 | 7.4 |
| 100 | consume | 4000 | 2.7 | 3.3 | 3.8 | 7.3 |
| 200 | start | 4000 | 1.0 | 1.2 | 1.5 | 6.2 |
| 200 | publish | 8000 | 2.7 | 3.2 | 3.5 | 7.3 |
| 200 | consume | 8000 | 2.9 | 3.8 | 4.2 | 9.8 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | 164 | 995 | 1000 | 1001 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    540817 ns/op	   16742 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    539737 ns/op	   16744 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    517250 ns/op	   16745 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    527576 ns/op	   17160 B/op	     200 allocs/op
BenchmarkPublish-12          	      50	   2089299 ns/op	   29574 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2029728 ns/op	   29573 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2000929 ns/op	   29573 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   1982725 ns/op	   29573 B/op	     303 allocs/op
BenchmarkConsume-12          	      50	   1702155 ns/op	   30732 B/op	     344 allocs/op
BenchmarkConsume-12          	      50	   1745093 ns/op	   30941 B/op	     345 allocs/op
BenchmarkConsume-12          	      50	   1721460 ns/op	   30845 B/op	     345 allocs/op
BenchmarkConsume-12          	      50	   1763311 ns/op	   30942 B/op	     345 allocs/op
BenchmarkSaga-12             	      50	   9763972 ns/op	  157852 B/op	    1754 allocs/op
BenchmarkSaga-12             	      50	   9729315 ns/op	  158313 B/op	    1757 allocs/op
BenchmarkSaga-12             	      50	   9569568 ns/op	  157864 B/op	    1754 allocs/op
BenchmarkSaga-12             	      50	   9613002 ns/op	  157859 B/op	    1754 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33699975 ns/op	  561923 B/op	    6722 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33695937 ns/op	  556165 B/op	    6716 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  34243318 ns/op	  552525 B/op	    6701 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  34561364 ns/op	  566225 B/op	    6726 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 165955496 ns/op	 2593239 B/op	   32979 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 166465462 ns/op	 2579792 B/op	   32947 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 167196618 ns/op	 2585551 B/op	   32952 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 163546892 ns/op	 2573137 B/op	   32957 allocs/op
```

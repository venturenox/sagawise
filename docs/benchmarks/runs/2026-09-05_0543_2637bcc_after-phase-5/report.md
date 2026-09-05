# Benchmark: after-phase-5

- **Date:** 2026-09-05T05:43:24+05:00
- **Commit:** `2637bcc`
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 0.9 / 1.3 | 2.4 / 2.9 | 2.7 / 3.5 | 36.0 | 1000 / 1000 |
| 100 | 99.9 | 10000 | 0 (0.00%) | 0.9 / 1.3 | 2.4 / 2.9 | 2.7 / 3.5 | 36.0 | 2000 / 2000 |
| 200 | 199.9 | 20000 | 0 (0.00%) | 1.0 / 1.4 | 2.7 / 3.4 | 2.9 / 4.1 | 36.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 0.9 | 1.1 | 1.3 | 1.5 |
| 50 | publish | 2000 | 2.4 | 2.7 | 2.9 | 5.7 |
| 50 | consume | 2000 | 2.7 | 3.3 | 3.5 | 7.0 |
| 100 | start | 2000 | 0.9 | 1.1 | 1.3 | 2.2 |
| 100 | publish | 4000 | 2.4 | 2.7 | 2.9 | 5.1 |
| 100 | consume | 4000 | 2.7 | 3.3 | 3.5 | 4.4 |
| 200 | start | 4000 | 1.0 | 1.2 | 1.4 | 4.3 |
| 200 | publish | 8000 | 2.7 | 3.1 | 3.4 | 6.4 |
| 200 | consume | 8000 | 2.9 | 3.7 | 4.1 | 6.4 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | 211 | 1010 | 1035 | 1035 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    551920 ns/op	   16743 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    558059 ns/op	   16743 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    549958 ns/op	   16744 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    562757 ns/op	   16886 B/op	     199 allocs/op
BenchmarkPublish-12          	      50	   2089135 ns/op	   29553 B/op	     304 allocs/op
BenchmarkPublish-12          	      50	   2089778 ns/op	   29553 B/op	     304 allocs/op
BenchmarkPublish-12          	      50	   2069233 ns/op	   29821 B/op	     304 allocs/op
BenchmarkPublish-12          	      50	   2080249 ns/op	   29709 B/op	     304 allocs/op
BenchmarkConsume-12          	      50	   1792924 ns/op	   30854 B/op	     346 allocs/op
BenchmarkConsume-12          	      50	   1818624 ns/op	   30960 B/op	     346 allocs/op
BenchmarkConsume-12          	      50	   1790604 ns/op	   30953 B/op	     346 allocs/op
BenchmarkConsume-12          	      50	   1929157 ns/op	   30731 B/op	     345 allocs/op
BenchmarkSaga-12             	      50	   9293811 ns/op	  159028 B/op	    1763 allocs/op
BenchmarkSaga-12             	      50	   9286305 ns/op	  159427 B/op	    1764 allocs/op
BenchmarkSaga-12             	      50	   9286472 ns/op	  158525 B/op	    1760 allocs/op
BenchmarkSaga-12             	      50	   9565660 ns/op	  159154 B/op	    1763 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33362933 ns/op	  538600 B/op	    6662 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33474994 ns/op	  534441 B/op	    6667 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  32538598 ns/op	  535393 B/op	    6661 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  33368797 ns/op	  535882 B/op	    6664 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 166808478 ns/op	 2600437 B/op	   32963 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 167549647 ns/op	 2541337 B/op	   32890 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 166312572 ns/op	 2546454 B/op	   32896 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 165526344 ns/op	 2549694 B/op	   32881 allocs/op
```

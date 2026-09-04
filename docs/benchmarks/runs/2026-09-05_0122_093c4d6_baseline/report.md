# Benchmark: baseline

- **Date:** 2026-09-05T01:22:17+05:00
- **Commit:** `093c4d6`
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 1.0 / 1.5 | 2.5 / 3.2 | 3.0 / 3.8 | 36.0 | 1000 / 1000 |
| 100 | 99.9 | 10000 | 0 (0.00%) | 0.9 / 1.4 | 2.6 / 3.3 | 2.9 / 3.9 | 36.0 | 2000 / 2000 |
| 200 | 199.9 | 20000 | 0 (0.00%) | 1.0 / 1.6 | 2.9 / 3.8 | 3.2 / 4.5 | 36.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 1.0 | 1.3 | 1.5 | 2.4 |
| 50 | publish | 2000 | 2.5 | 2.9 | 3.2 | 5.0 |
| 50 | consume | 2000 | 3.0 | 3.5 | 3.8 | 7.7 |
| 100 | start | 2000 | 0.9 | 1.2 | 1.4 | 2.2 |
| 100 | publish | 4000 | 2.6 | 3.0 | 3.3 | 6.1 |
| 100 | consume | 4000 | 2.9 | 3.6 | 3.9 | 8.1 |
| 200 | start | 4000 | 1.0 | 1.3 | 1.6 | 4.6 |
| 200 | publish | 8000 | 2.9 | 3.5 | 3.8 | 7.6 |
| 200 | consume | 8000 | 3.2 | 4.1 | 4.5 | 7.9 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | 193 | 1006 | 1029 | 1030 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    601093 ns/op	   16743 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    639500 ns/op	   16747 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    582032 ns/op	   16743 B/op	     199 allocs/op
BenchmarkStartInstance-12    	      50	    604147 ns/op	   17300 B/op	     201 allocs/op
BenchmarkPublish-12          	      50	   2202269 ns/op	   29573 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2236585 ns/op	   29576 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2242966 ns/op	   29577 B/op	     303 allocs/op
BenchmarkPublish-12          	      50	   2259544 ns/op	   29685 B/op	     303 allocs/op
BenchmarkConsume-12          	      50	   1964950 ns/op	   30716 B/op	     344 allocs/op
BenchmarkConsume-12          	      50	   1938690 ns/op	   30943 B/op	     345 allocs/op
BenchmarkConsume-12          	      50	   1938671 ns/op	   31044 B/op	     345 allocs/op
BenchmarkConsume-12          	      50	   1923581 ns/op	   30570 B/op	     344 allocs/op
BenchmarkSaga-12             	      50	  10012023 ns/op	  158638 B/op	    1757 allocs/op
BenchmarkSaga-12             	      50	  10061659 ns/op	  158006 B/op	    1755 allocs/op
BenchmarkSaga-12             	      50	  10113883 ns/op	  158253 B/op	    1756 allocs/op
BenchmarkSaga-12             	      50	   9985444 ns/op	  159163 B/op	    1760 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35824279 ns/op	  568458 B/op	    6731 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35707656 ns/op	  572420 B/op	    6738 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35826860 ns/op	  561365 B/op	    6719 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35623441 ns/op	  570050 B/op	    6728 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 177557396 ns/op	 2591859 B/op	   33026 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 180275974 ns/op	 2582108 B/op	   32963 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 181779217 ns/op	 2595870 B/op	   33019 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 179978390 ns/op	 2580328 B/op	   33007 allocs/op
```

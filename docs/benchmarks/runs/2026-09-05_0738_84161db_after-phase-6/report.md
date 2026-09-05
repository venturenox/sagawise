# Benchmark: after-phase-6

- **Date:** 2026-09-05T07:38:09+05:00
- **Commit:** `84161db`
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7 at localhost:6379, Postgres PostgreSQL 18.6 at localhost:5432
- **Config:** rates 50,100,200 sagas/s, 20s per rate, 200 reaper-lag tasks, go-bench count 4 × 50x

Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.

## Load

| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |
|---|---|---|---|---|---|---|---|---|
| 50 | 50.0 | 5000 | 0 (0.00%) | 1.0 / 1.5 | 0.9 / 1.4 | 0.9 / 1.4 | 37.2 | 1000 / 1000 |
| 100 | 100.0 | 10000 | 0 (0.00%) | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.3 | 37.1 | 2000 / 2000 |
| 200 | 200.0 | 20000 | 0 (0.00%) | 0.9 / 1.2 | 0.9 / 1.3 | 0.9 / 1.3 | 37.0 | 4000 / 4000 |

Full percentiles (ms):

| rate | endpoint | n | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 | start | 1000 | 1.0 | 1.3 | 1.5 | 5.2 |
| 50 | publish | 2000 | 0.9 | 1.2 | 1.4 | 24.1 |
| 50 | consume | 2000 | 0.9 | 1.2 | 1.4 | 1.8 |
| 100 | start | 2000 | 0.9 | 1.1 | 1.3 | 1.9 |
| 100 | publish | 4000 | 0.9 | 1.1 | 1.3 | 2.9 |
| 100 | consume | 4000 | 0.9 | 1.1 | 1.3 | 3.4 |
| 200 | start | 4000 | 0.9 | 1.1 | 1.2 | 1.9 |
| 200 | publish | 8000 | 0.9 | 1.1 | 1.3 | 2.2 |
| 200 | consume | 8000 | 0.9 | 1.1 | 1.3 | 3.1 |

## Reaper lag (deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 200 | 200 | 0 | 59 | 189 | 1001 | 3115 |

The reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.

## Go micro-benchmarks

Handlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).

```
goos: linux
goarch: amd64
pkg: wtfsaga/instance_engine
cpu: AMD Ryzen 9 9900X 12-Core Processor            
BenchmarkStartInstance-12    	      50	    630777 ns/op	   15809 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    611352 ns/op	   15944 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    606610 ns/op	   15942 B/op	     161 allocs/op
BenchmarkStartInstance-12    	      50	    618927 ns/op	   16079 B/op	     162 allocs/op
BenchmarkPublish-12          	      50	   1259534 ns/op	   21716 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1291493 ns/op	   21593 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1239558 ns/op	   21593 B/op	     206 allocs/op
BenchmarkPublish-12          	      50	   1240571 ns/op	   21592 B/op	     206 allocs/op
BenchmarkConsume-12          	      50	   1201194 ns/op	   21784 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1181835 ns/op	   21788 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1150410 ns/op	   21784 B/op	     214 allocs/op
BenchmarkConsume-12          	      50	   1162019 ns/op	   21784 B/op	     214 allocs/op
BenchmarkSaga-12             	      50	   8542260 ns/op	  115122 B/op	    1124 allocs/op
BenchmarkSaga-12             	      50	   8610681 ns/op	  115274 B/op	    1124 allocs/op
BenchmarkSaga-12             	      50	   8411982 ns/op	  115564 B/op	    1125 allocs/op
BenchmarkSaga-12             	      50	   8474920 ns/op	  115260 B/op	    1124 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  39683922 ns/op	  580139 B/op	    4196 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  38354340 ns/op	  530322 B/op	    4173 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35287244 ns/op	  509755 B/op	    4161 allocs/op
BenchmarkReaperTick/overdue=10-12         	      50	  35375018 ns/op	  496855 B/op	    4173 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 185445318 ns/op	 2085325 B/op	   19711 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 171313559 ns/op	 2047693 B/op	   19706 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 179563554 ns/op	 2065349 B/op	   19800 allocs/op
BenchmarkReaperTick/overdue=50-12         	      50	 182418990 ns/op	 2016158 B/op	   19712 allocs/op
```

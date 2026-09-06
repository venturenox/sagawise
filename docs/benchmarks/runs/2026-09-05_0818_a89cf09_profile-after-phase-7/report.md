# Profile: profile-after-phase-7

- **Date:** 2026-09-05T08:18:16+05:00
- **Commit:** `a89cf09`
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 2277 sagas/s (~11385 requests/s). The next step, 3415 sagas/s, breached: achieved 2968 < 90% of target 3415 (load generator or server saturated).
- Redis is near saturation at the knee: 86% of one core, ping p99 3.39 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis commands per request: start 2.0, publish 6.0, consume 5.0, final consume 11.1 (INFO commandstats; commands run inside the transition script count too, so this is Redis work, not client round-trips, which are 2 per report since phase 6).
- Most expensive Redis work per request: evalsha on publish (1.0 calls × 107 µs).
- JSON.SET costs 49 µs against 15 µs for JSON.GET (3×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$.tasks[*].topic/from/to` and the instance stamps), so write count, not read count, is what Redis CPU pays for. A final consume does 0 writes.
- Instances already in Redis: consume p50 0.9 → 0.9 ms from 0 to 100000 instances (-7%). List endpoint p50 1.8 → 3.5 ms.
- Tasks per workflow: consume p50 0.9 → 0.9 ms from 2 to 50 tasks (+5%) at a constant ~1000 req/s. Growth here is document size: the script reads every task's state and each JSON.SET re-indexes the document.
- Payload size: consume p50 0.9 → 1.2 ms from 100B to 500000B (+38%). The payload travels with the publish and is written by the script's JSON.SET; a consume never reads it.
- Simultaneous timeouts: max lag 312 ms at 100 → 1248 ms at 2000, i.e. ~0.5 ms per overdue task. The reaper runs one script call per overdue member, sequentially; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 2.8 ms vs on separate instances 2.8 ms (+2%). Redis runs one transition script at a time, so reports on one instance queue behind each other; this is the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 33.36%, instance_engine.(*Engine).jsonGet 19.58%, instance_engine.(*Engine).readTaskIdentity 15.81%, instance_engine.(*Engine).transition 13.86%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 2277 sagas/s.** Breach: achieved 2968 < 90% of target 3415 (load generator or server saturated).

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 999 | 0 (0.00%) | 0.8 / 1.4 | 0.9 / 1.3 | 0.9 / 1.2 | 24 | 0.25 / 0.61 | ok |
| 300 | 300 | 1499 | 0 (0.00%) | 0.9 / 1.5 | 0.9 / 1.4 | 0.9 / 1.4 | 32 | 0.27 / 0.56 | ok |
| 450 | 450 | 2248 | 0 (0.00%) | 0.9 / 1.5 | 0.9 / 1.4 | 0.9 / 1.4 | 41 | 0.28 / 0.60 | ok |
| 675 | 674 | 3369 | 0 (0.00%) | 1.0 / 2.3 | 1.1 / 1.8 | 1.0 / 1.8 | 52 | 0.33 / 0.76 | ok |
| 1012 | 1006 | 5030 | 0 (0.00%) | 1.2 / 2.7 | 1.2 / 2.4 | 1.2 / 2.5 | 60 | 0.38 / 1.86 | ok |
| 1518 | 1495 | 7474 | 0 (0.00%) | 1.3 / 3.4 | 1.3 / 3.0 | 1.3 / 3.1 | 70 | 0.44 / 1.17 | ok |
| 2277 | 2153 | 10757 | 0 (0.00%) | 1.6 / 5.2 | 1.6 / 4.6 | 1.6 / 4.4 | 76 | 0.55 / 2.30 | ok |
| 3415 | 2968 | 14824 | 0 (0.00%) | 2.1 / 4.8 | 2.2 / 4.8 | 2.2 / 4.7 | 86 | 0.74 / 3.39 | **breach** |

## 2. Server profiles at the knee (2277 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: a3a37cc40ef89e2ed37b5cfda2a2228f5e7ca713
Type: cpu
Time: 2026-09-05 08:19:23 PKT
Duration: 10s, Total samples = 26.56s (265.58%)
Showing nodes accounting for 15.47s, 58.25% of 26.56s total
Dropped 825 nodes (cum <= 0.13s)
Showing top 25 nodes out of 239
      flat  flat%   sum%        cum   cum%
     6.54s 24.62% 24.62%      6.54s 24.62%  internal/runtime/syscall/linux.Syscall6
     4.57s 17.21% 41.83%      4.57s 17.21%  runtime.futex
     0.45s  1.69% 43.52%      0.45s  1.69%  runtime.procyieldAsm
     0.42s  1.58% 45.11%      0.42s  1.58%  runtime.memmove
     0.35s  1.32% 46.42%      0.35s  1.32%  runtime.usleep
     0.33s  1.24% 47.67%      0.38s  1.43%  runtime.step
     0.29s  1.09% 48.76%      0.40s  1.51%  runtime.scanObject
     0.26s  0.98% 49.74%      0.26s  0.98%  runtime.nextFreeFast (inline)
     0.21s  0.79% 50.53%      0.21s  0.79%  runtime.write1
     0.20s  0.75% 51.28%      0.68s  2.56%  runtime.pcvalue
     0.18s  0.68% 51.96%      0.19s  0.72%  runtime.mallocgcSmallScanNoHeaderSC2
     0.17s  0.64% 52.60%      0.38s  1.43%  runtime.(*unwinder).resolveInternal
     0.17s  0.64% 53.24%      0.17s  0.64%  runtime.nanotime (inline)
     0.16s   0.6% 53.84%      1.05s  3.95%  runtime.lock2
     0.16s   0.6% 54.44%      0.16s   0.6%  runtime.osyield
     0.12s  0.45% 54.89%      1.68s  6.33%  runtime.mallocgc
     0.12s  0.45% 55.35%      0.14s  0.53%  runtime.mallocgcTinySC2
     0.12s  0.45% 55.80%      1.57s  5.91%  runtime.unlock2
     0.11s  0.41% 56.21%      6.33s 23.83%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     0.11s  0.41% 56.63%      6.79s 25.56%  runtime.findRunnable
     0.10s  0.38% 57.00%      0.64s  2.41%  runtime.growslice
     0.10s  0.38% 57.38%      0.58s  2.18%  runtime.stealWork
     0.09s  0.34% 57.72%      1.49s  5.61%  internal/poll.(*FD).Read
     0.07s  0.26% 57.98%     13.61s 51.24%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
     0.07s  0.26% 58.25%      0.73s  2.75%  go.opentelemetry.io/otel/sdk/trace.(*tracer).Start
```

### cpu-cumulative

```
File: sagawise
Build ID: a3a37cc40ef89e2ed37b5cfda2a2228f5e7ca713
Type: cpu
Time: 2026-09-05 08:19:23 PKT
Duration: 10s, Total samples = 26.56s (265.58%)
Showing nodes accounting for 11.86s, 44.65% of 26.56s total
Dropped 825 nodes (cum <= 0.13s)
Showing top 40 nodes out of 239
      flat  flat%   sum%        cum   cum%
     0.03s  0.11%  0.11%     17.78s 66.94%  net/http.(*conn).serve
     0.02s 0.075%  0.19%     13.64s 51.36%  net/http.serverHandler.ServeHTTP
     0.01s 0.038%  0.23%     13.62s 51.28%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%  0.23%     13.62s 51.28%  net/http.HandlerFunc.ServeHTTP
     0.07s  0.26%  0.49%     13.61s 51.24%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.49%     11.98s 45.11%  net/http.(*ServeMux).ServeHTTP
         0     0%  0.49%     11.91s 44.84%  main.httpTracing.func1
     0.07s  0.26%  0.75%      8.86s 33.36%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.04s  0.15%   0.9%      8.54s 32.15%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   0.9%      8.54s 32.15%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   0.9%      8.54s 32.15%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.02s 0.075%  0.98%      7.26s 27.33%  runtime.mcall
     0.01s 0.038%  1.02%      7.18s 27.03%  runtime.park_m
     0.02s 0.075%  1.09%      7.18s 27.03%  runtime.schedule
     0.11s  0.41%  1.51%      6.79s 25.56%  runtime.findRunnable
     6.54s 24.62% 26.13%      6.54s 24.62%  internal/runtime/syscall/linux.Syscall6
     0.11s  0.41% 26.54%      6.33s 23.83%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     0.01s 0.038% 26.58%      6.20s 23.34%  internal/poll.ignoringEINTRIO (inline)
     0.01s 0.038% 26.62%      6.20s 23.34%  syscall.RawSyscall6
     0.01s 0.038% 26.66%      6.20s 23.34%  syscall.Syscall
     0.02s 0.075% 26.73%      5.60s 21.08%  github.com/redis/go-redis/v9.(*baseClient).withConn
     0.02s 0.075% 26.81%      5.58s 21.01%  github.com/redis/go-redis/v9.(*baseClient).process
     0.02s 0.075% 26.88%      5.55s 20.90%  github.com/redis/go-redis/v9.(*baseClient).processCommand
     0.02s 0.075% 26.96%      5.53s 20.82%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 26.96%      5.53s 20.82%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 26.96%      5.20s 19.58%  wtfsaga/instance_engine.(*Engine).jsonGet
     0.01s 0.038% 27.00%      4.97s 18.71%  internal/poll.(*FD).Write
         0     0% 27.00%      4.91s 18.49%  syscall.Write (inline)
         0     0% 27.00%      4.91s 18.49%  syscall.write
     0.01s 0.038% 27.03%      4.72s 17.77%  bufio.(*Writer).Flush
     0.02s 0.075% 27.11%      4.59s 17.28%  github.com/redis/go-redis/v9.cmdable.JSONGet
     4.57s 17.21% 44.31%      4.57s 17.21%  runtime.futex
     0.01s 0.038% 44.35%      4.52s 17.02%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
         0     0% 44.35%      4.47s 16.83%  net.(*conn).Write
     0.01s 0.038% 44.39%      4.47s 16.83%  net.(*netFD).Write
         0     0% 44.39%      4.20s 15.81%  wtfsaga/instance_engine.(*Engine).readTaskIdentity
     0.05s  0.19% 44.58%      3.68s 13.86%  wtfsaga/instance_engine.(*Engine).transition
         0     0% 44.58%      3.54s 13.33%  runtime.futexwakeup
     0.02s 0.075% 44.65%      3.30s 12.42%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 44.65%      3.30s 12.42%  github.com/redis/go-redis/v9.(*Script).Run
```

### heap

```
File: sagawise
Build ID: a3a37cc40ef89e2ed37b5cfda2a2228f5e7ca713
Type: inuse_space
Time: 2026-09-05 08:19:33 PKT
Showing nodes accounting for 13046.88kB, 100% of 13046.88kB total
Showing top 25 nodes out of 63
      flat  flat%   sum%        cum   cum%
 4753.50kB 36.43% 36.43%  4753.50kB 36.43%  bufio.NewReaderSize (inline)
    3169kB 24.29% 60.72%     3169kB 24.29%  bufio.NewWriterSize (inline)
 2049.25kB 15.71% 76.43%  2049.25kB 15.71%  go.opentelemetry.io/otel/sdk/log.newRing
    1539kB 11.80% 88.23%     1539kB 11.80%  runtime.mallocgc
  512.06kB  3.92% 92.15%   512.06kB  3.92%  net.newFD
  512.05kB  3.92% 96.08%   512.05kB  3.92%  time.NewTicker
  512.01kB  3.92%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9/internal/proto.(*Reader).readStringReply
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*Cmd).readReply
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*Script).Run
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient)._process.func1.3
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.cmdable.EvalSha
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9.cmdable.eval
         0     0%   100%   512.01kB  3.92%  github.com/redis/go-redis/v9/internal/pool.(*Conn).WithReader
         0     0%   100%  7922.50kB 60.72%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
```

### block

```
File: sagawise
Build ID: a3a37cc40ef89e2ed37b5cfda2a2228f5e7ca713
Type: delay
Time: 2026-09-05 08:19:33 PKT
Showing nodes accounting for 318.85s, 99.59% of 320.17s total
Dropped 134 nodes (cum <= 1.60s)
      flat  flat%   sum%        cum   cum%
   246.73s 77.06% 77.06%    246.73s 77.06%  runtime.selectgo
    62.55s 19.54% 96.60%     62.55s 19.54%  runtime.chansend1
     6.69s  2.09% 98.69%      6.69s  2.09%  sync.(*WaitGroup).Wait
     2.88s   0.9% 99.59%      2.88s   0.9%  sync.(*Mutex).Lock (inline)
         0     0% 99.59%      2.95s  0.92%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0% 99.59%      2.95s  0.92%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0% 99.59%     76.58s 23.92%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
         0     0% 99.59%      2.04s  0.64%  log.(*Logger).output
         0     0% 99.59%      2.04s  0.64%  log.Printf
         0     0% 99.59%      2.90s  0.91%  main.httpTracing.func1
         0     0% 99.59%     13.41s  4.19%  net/http.(*ServeMux).ServeHTTP
         0     0% 99.59%     14.83s  4.63%  net/http.(*Server).Serve.gowrap3
         0     0% 99.59%     14.83s  4.63%  net/http.(*conn).serve
         0     0% 99.59%     13.47s  4.21%  net/http.HandlerFunc.ServeHTTP
         0     0% 99.59%     13.47s  4.21%  net/http.serverHandler.ServeHTTP
         0     0% 99.59%     10.51s  3.28%  net/http/pprof.Profile
         0     0% 99.59%     10.51s  3.28%  net/http/pprof.sleep
         0     0% 99.59%     76.52s 23.90%  wtfsaga/instance_engine.(*Engine).StartDeadlineReaper.func1
         0     0% 99.59%    152.16s 47.53%  wtfsaga/instance_engine.(*Worker).Start.func1
         0     0% 99.59%     69.24s 21.63%  wtfsaga/instance_engine.(*Worker).tick
```

### mutex

```
File: sagawise
Build ID: a3a37cc40ef89e2ed37b5cfda2a2228f5e7ca713
Type: delay
Time: 2026-09-05 08:19:33 PKT
Showing nodes accounting for 146.22s, 100% of 146.22s total
Dropped 431 nodes (cum <= 0.73s)
Showing top 25 nodes out of 69
      flat  flat%   sum%        cum   cum%
   135.71s 92.81% 92.81%    135.71s 92.81%  runtime.unlock (partial-inline)
     7.21s  4.93% 97.74%      7.21s  4.93%  runtime._LostContendedRuntimeLock
     3.30s  2.26%   100%      3.43s  2.34%  sync.(*Mutex).Unlock (partial-inline)
         0     0%   100%      4.21s  2.88%  _.goready.func1
         0     0%   100%      1.56s  1.07%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%      1.60s  1.10%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%      1.60s  1.10%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      1.56s  1.07%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      1.56s  1.07%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      1.56s  1.07%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%      1.56s  1.07%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%      1.60s  1.10%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%      1.60s  1.10%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%      0.82s  0.56%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      4.90s  3.35%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%      4.90s  3.35%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%      2.67s  1.82%  internal/poll.(*FD).SetReadDeadline (inline)
         0     0%   100%      2.78s  1.90%  internal/poll.runtime_pollSetDeadline
         0     0%   100%      2.78s  1.90%  internal/poll.setDeadlineImpl
         0     0%   100%      2.60s  1.78%  log.(*Logger).output
         0     0%   100%      2.60s  1.78%  log.Printf
         0     0%   100%      4.79s  3.28%  main.httpTracing.func1
         0     0%   100%      2.67s  1.82%  net.(*conn).SetReadDeadline
         0     0%   100%      2.67s  1.82%  net.(*netFD).SetReadDeadline (inline)
         0     0%   100%      4.79s  3.28%  net/http.(*ServeMux).ServeHTTP
```

### goroutine

```
File: sagawise
Build ID: a3a37cc40ef89e2ed37b5cfda2a2228f5e7ca713
Type: goroutine
Time: 2026-09-05 08:19:33 PKT
Showing nodes accounting for 55, 98.21% of 56 total
Showing top 25 nodes out of 93
      flat  flat%   sum%        cum   cum%
        53 94.64% 94.64%         53 94.64%  runtime.gopark
         1  1.79% 96.43%          1  1.79%  runtime.goroutineProfileWithLabels
         1  1.79% 98.21%          1  1.79%  runtime.notetsleepg
         0     0% 98.21%         31 55.36%  bufio.(*Reader).Peek
         0     0% 98.21%         31 55.36%  bufio.(*Reader).fill
         0     0% 98.21%          1  1.79%  github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck
         0     0% 98.21%          1  1.79%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func5
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessPipelineHook.func1
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessPipelineHook.func1
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/v9.(*Pipeline).Exec
         0     0% 98.21%          5  8.93%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 98.21%          5  8.93%  github.com/redis/go-redis/v9.(*Script).Run
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/v9.(*baseClient)._process.func1.3
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/v9.(*baseClient).generalProcessPipeline
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/v9.(*baseClient).generalProcessPipeline.func1
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/v9.(*baseClient).pipelineProcessCmds
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/v9.(*baseClient).pipelineProcessCmds.func2
         0     0% 98.21%          1  1.79%  github.com/redis/go-redis/v9.(*baseClient).pipelineReadCmds
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 98.21%         13 23.21%  github.com/redis/go-redis/v9.(*baseClient).processCommand
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 49 | 49 |
| start | json.get | 1.00 | 9 | 9 |
| publish | evalsha | 1.01 | 107 | 108 |
| publish | json.set | 1.00 | 41 | 41 |
| publish | json.get | 2.00 | 15 | 30 |
| publish | json.merge | 1.00 | 16 | 16 |
| publish | zadd | 1.00 | 3 | 3 |
| publish | zrangebyscore | 0.01 | 4 | 0 |
| consume | evalsha | 1.00 | 87 | 87 |
| consume | json.merge | 1.00 | 40 | 40 |
| consume | json.get | 2.00 | 15 | 30 |
| consume | zrem | 1.00 | 2 | 2 |
| consume(final) | evalsha | 1.04 | 99 | 103 |
| consume(final) | json.merge | 2.00 | 26 | 52 |
| consume(final) | json.get | 3.00 | 13 | 39 |
| consume(final) | zrem | 2.00 | 3 | 6 |
| consume(final) | zadd | 2.00 | 2 | 4 |
| consume(final) | hdel | 1.00 | 0 | 0 |
| consume(final) | zrangebyscore | 0.04 | 9 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 2.0 | 58 |
| publish | 6.0 | 199 |
| consume | 5.0 | 158 |
| consume(final) | 11.1 | 204 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 1.0 / 1.4 | 1.0 / 1.3 | 0.9 / 1.3 | 1.8 | 2.4 | 0.6 | 0.0 |
| 10000 | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 / 1.2 | 1.8 | 2.0 | 0.5 | 1431.5 |
| 100000 | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.1 | 3.5 | 3.9 | 0.5 | 1255.7 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.2 | 199.9 | 0.0 |
| 10 | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 / 1.2 | 47.5 | 0.0 |
| 50 | 1.0 / 1.5 | 1.0 / 1.3 | 0.9 / 1.2 | 9.8 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 0.9 / 1.4 | 0.9 / 1.2 | 0.9 / 1.3 | 0.0 |
| 10000B | 1.0 / 1.5 | 1.0 / 1.4 | 0.9 / 1.2 | 0.0 |
| 500000B | 1.0 / 1.4 | 2.2 / 3.0 | 1.2 / 1.6 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 298 | 305 | 312 | 312 |
| 500 | 500 | 0 | 453 | 484 | 513 | 515 |
| 2000 | 2000 | 0 | 175 | 305 | 1239 | 1248 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 2.8 | 4.4 | 5.0 | 5.2 |
| separate instances | 400 | 2.8 | 4.0 | 5.0 | 5.3 |

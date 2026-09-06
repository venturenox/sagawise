# Profile: profile-p6server-newharness

- **Date:** 2026-09-05T08:21:42+05:00
- **Commit:** `a89cf09` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 2277 sagas/s (~11385 requests/s). The next step, 3415 sagas/s, breached: achieved 2772 < 90% of target 3415 (load generator or server saturated).
- Redis is near saturation at the knee: 89% of one core, ping p99 2.81 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis commands per request: start 2.0, publish 7.0, consume 6.0, final consume 13.1 (INFO commandstats; commands run inside the transition script count too, so this is Redis work, not client round-trips, which are 2 per report since phase 6).
- Most expensive Redis work per request: evalsha on consume(final) (1.0 calls × 117 µs).
- JSON.SET costs 49 µs against 14 µs for JSON.GET (3×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$.tasks[*].topic/from/to` and the instance stamps), so write count, not read count, is what Redis CPU pays for. A final consume does 4 writes.
- Instances already in Redis: consume p50 0.9 → 0.9 ms from 0 to 100000 instances (-3%). List endpoint p50 0.9 → 3.0 ms.
- Tasks per workflow: consume p50 0.9 → 1.0 ms from 2 to 50 tasks (+7%) at a constant ~1000 req/s. Growth here is document size: the script reads every task's state and each JSON.SET re-indexes the document.
- Payload size: consume p50 0.9 → 1.4 ms from 100B to 500000B (+52%). The payload travels with the publish and is written by the script's JSON.SET; a consume never reads it.
- Simultaneous timeouts: max lag 398 ms at 100 → 2295 ms at 2000, i.e. ~1.0 ms per overdue task. The reaper runs one script call per overdue member, sequentially; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 4.1 ms vs on separate instances 3.8 ms (+8%). Redis runs one transition script at a time, so reports on one instance queue behind each other; this is the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 33.64%, instance_engine.(*Engine).jsonGet 17.95%, instance_engine.(*Engine).transition 15.50%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 2277 sagas/s.** Breach: achieved 2772 < 90% of target 3415 (load generator or server saturated).

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 999 | 0 (0.00%) | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.2 | 26 | 0.26 / 0.52 | ok |
| 300 | 300 | 1498 | 0 (0.00%) | 0.9 / 1.6 | 0.9 / 1.4 | 0.9 / 1.4 | 33 | 0.29 / 0.50 | ok |
| 450 | 449 | 2247 | 0 (0.00%) | 1.0 / 1.4 | 1.0 / 1.4 | 1.0 / 1.5 | 43 | 0.30 / 0.61 | ok |
| 675 | 674 | 3371 | 0 (0.00%) | 1.1 / 1.7 | 1.1 / 1.7 | 1.1 / 1.7 | 55 | 0.35 / 0.79 | ok |
| 1012 | 1011 | 5054 | 0 (0.00%) | 1.2 / 2.0 | 1.2 / 2.0 | 1.2 / 2.0 | 66 | 0.40 / 0.89 | ok |
| 1518 | 1506 | 7524 | 0 (0.00%) | 1.4 / 2.5 | 1.4 / 2.5 | 1.4 / 2.5 | 75 | 0.47 / 1.17 | ok |
| 2277 | 2133 | 10657 | 0 (0.00%) | 1.9 / 5.1 | 2.0 / 4.5 | 2.0 / 4.5 | 82 | 0.67 / 1.82 | ok |
| 3415 | 2772 | 13846 | 0 (0.00%) | 2.8 / 6.2 | 2.8 / 6.1 | 2.8 / 6.2 | 89 | 1.03 / 2.81 | **breach** |

## 2. Server profiles at the knee (2277 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: 526e7258f719cfce4b7b9945c1271d9dd63a2e7a
Type: cpu
Time: 2026-09-05 08:22:48 PKT
Duration: 10s, Total samples = 26.07s (260.69%)
Showing nodes accounting for 14.49s, 55.58% of 26.07s total
Dropped 835 nodes (cum <= 0.13s)
Showing top 25 nodes out of 246
      flat  flat%   sum%        cum   cum%
     6.07s 23.28% 23.28%      6.07s 23.28%  internal/runtime/syscall/linux.Syscall6
     4.28s 16.42% 39.70%      4.28s 16.42%  runtime.futex
     0.33s  1.27% 40.97%      0.35s  1.34%  runtime.step
     0.31s  1.19% 42.16%      0.31s  1.19%  runtime.procyieldAsm
     0.30s  1.15% 43.31%      0.30s  1.15%  runtime.usleep
     0.26s     1% 44.30%      0.26s     1%  runtime.memmove
     0.26s     1% 45.30%      0.26s     1%  runtime.nextFreeFast (inline)
     0.22s  0.84% 46.14%      0.71s  2.72%  runtime.pcvalue
     0.20s  0.77% 46.91%      6.01s 23.05%  runtime.findRunnable
     0.20s  0.77% 47.68%      0.91s  3.49%  runtime.lock2
     0.19s  0.73% 48.41%      1.72s  6.60%  runtime.mallocgc
     0.18s  0.69% 49.10%      0.21s  0.81%  runtime.scanObject
     0.18s  0.69% 49.79%      0.18s  0.69%  runtime.write1
     0.17s  0.65% 50.44%      0.39s  1.50%  runtime.(*unwinder).resolveInternal
     0.17s  0.65% 51.09%      0.17s  0.65%  runtime.nanotime (inline)
     0.16s  0.61% 51.71%      0.16s  0.61%  time.runtimeNow
     0.15s  0.58% 52.28%      0.19s  0.73%  runtime.findfunc
     0.14s  0.54% 52.82%      0.14s  0.54%  runtime.osyield
     0.12s  0.46% 53.28%      0.66s  2.53%  internal/poll.runtime_pollSetDeadline
     0.11s  0.42% 53.70%      0.15s  0.58%  runtime.mallocgcTinySC2
     0.11s  0.42% 54.12%      1.26s  4.83%  runtime.unlock2
     0.10s  0.38% 54.51%      0.51s  1.96%  runtime.growslice
     0.10s  0.38% 54.89%      0.41s  1.57%  runtime.runqgrab
     0.09s  0.35% 55.24%      0.52s  1.99%  runtime.stealWork
     0.09s  0.35% 55.58%      8.77s 33.64%  wtfsaga/instance_engine.(*Engine).UpdateInstance
```

### cpu-cumulative

```
File: sagawise
Build ID: 526e7258f719cfce4b7b9945c1271d9dd63a2e7a
Type: cpu
Time: 2026-09-05 08:22:48 PKT
Duration: 10s, Total samples = 26.07s (260.69%)
Showing nodes accounting for 11.22s, 43.04% of 26.07s total
Dropped 835 nodes (cum <= 0.13s)
Showing top 40 nodes out of 246
      flat  flat%   sum%        cum   cum%
     0.05s  0.19%  0.19%     18.15s 69.62%  net/http.(*conn).serve
     0.01s 0.038%  0.23%     14.02s 53.78%  net/http.serverHandler.ServeHTTP
     0.03s  0.12%  0.35%     14.01s 53.74%  net/http.HandlerFunc.ServeHTTP
     0.04s  0.15%   0.5%     13.99s 53.66%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   0.5%     13.99s 53.66%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
     0.02s 0.077%  0.58%     12.42s 47.64%  net/http.(*ServeMux).ServeHTTP
     0.03s  0.12%  0.69%     12.33s 47.30%  main.httpTracing.func1
     0.09s  0.35%  1.04%      8.77s 33.64%  wtfsaga/instance_engine.(*Engine).UpdateInstance
         0     0%  1.04%      8.58s 32.91%  github.com/redis/go-redis/v9.(*Client).Process
     0.02s 0.077%  1.11%      8.58s 32.91%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.06s  0.23%  1.34%      8.56s 32.83%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.01s 0.038%  1.38%      6.53s 25.05%  runtime.mcall
     0.04s  0.15%  1.53%      6.44s 24.70%  runtime.park_m
     0.01s 0.038%  1.57%      6.36s 24.40%  runtime.schedule
     0.03s  0.12%  1.69%      6.15s 23.59%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     6.07s 23.28% 24.97%      6.07s 23.28%  internal/runtime/syscall/linux.Syscall6
     0.20s  0.77% 25.74%      6.01s 23.05%  runtime.findRunnable
     0.02s 0.077% 25.82%      5.89s 22.59%  syscall.Syscall
         0     0% 25.82%      5.84s 22.40%  internal/poll.ignoringEINTRIO (inline)
     0.01s 0.038% 25.85%      5.74s 22.02%  syscall.RawSyscall6
     0.02s 0.077% 25.93%      5.50s 21.10%  github.com/redis/go-redis/v9.(*baseClient).process
     0.02s 0.077% 26.01%      5.49s 21.06%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 26.01%      5.45s 20.91%  github.com/redis/go-redis/v9.(*baseClient).processCommand
     0.02s 0.077% 26.08%      5.45s 20.91%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 26.08%      5.43s 20.83%  github.com/redis/go-redis/v9.(*baseClient)._process
     0.02s 0.077% 26.16%      4.68s 17.95%  internal/poll.(*FD).Write
         0     0% 26.16%      4.68s 17.95%  wtfsaga/instance_engine.(*Engine).jsonGet
         0     0% 26.16%      4.62s 17.72%  syscall.Write (inline)
         0     0% 26.16%      4.62s 17.72%  syscall.write
     0.02s 0.077% 26.24%      4.36s 16.72%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
     0.01s 0.038% 26.28%      4.35s 16.69%  bufio.(*Writer).Flush
     4.28s 16.42% 42.69%      4.28s 16.42%  runtime.futex
     0.05s  0.19% 42.88%      4.04s 15.50%  wtfsaga/instance_engine.(*Engine).transition
         0     0% 42.88%      3.97s 15.23%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0% 42.88%      3.92s 15.04%  net.(*conn).Write
     0.01s 0.038% 42.92%      3.92s 15.04%  net.(*netFD).Write
     0.02s 0.077% 43.00%      3.82s 14.65%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 43.00%      3.82s 14.65%  github.com/redis/go-redis/v9.(*Script).Run
     0.01s 0.038% 43.04%      3.80s 14.58%  github.com/redis/go-redis/v9.cmdable.EvalSha
         0     0% 43.04%      3.79s 14.54%  github.com/redis/go-redis/v9.cmdable.eval
```

### heap

```
File: sagawise
Build ID: 526e7258f719cfce4b7b9945c1271d9dd63a2e7a
Type: inuse_space
Time: 2026-09-05 08:22:59 PKT
Showing nodes accounting for 15299.42kB, 100% of 15299.42kB total
Showing top 25 nodes out of 60
      flat  flat%   sum%        cum   cum%
 5809.83kB 37.97% 37.97%  5809.83kB 37.97%  bufio.NewWriterSize (inline)
    3169kB 20.71% 58.69%     3169kB 20.71%  bufio.NewReaderSize (inline)
    2565kB 16.77% 75.45%     2565kB 16.77%  runtime.mallocgc
 1536.94kB 10.05% 85.50%  1536.94kB 10.05%  go.opentelemetry.io/otel/sdk/log.newRing (inline)
  669.43kB  4.38% 89.87%   669.43kB  4.38%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
  521.05kB  3.41% 93.28%   521.05kB  3.41%  encoding/xml.map.init.0
  516.01kB  3.37% 96.65%   516.01kB  3.37%  hash/crc32.slicingMakeTable
  512.16kB  3.35%   100%   512.16kB  3.35%  net/http.readRequestLimit
         0     0%   100%   516.01kB  3.37%  compress/gzip.(*Writer).Write
         0     0%   100%   521.05kB  3.41%  encoding/xml.init
         0     0%   100%  8978.83kB 58.69%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100%  8978.83kB 58.69%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100%  8978.83kB 58.69%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100%  8978.83kB 58.69%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
         0     0%   100%     3169kB 20.71%  github.com/redis/go-redis/v9/internal/proto.NewReaderSize (inline)
         0     0%   100%  1536.94kB 10.05%  go.opentelemetry.io/otel/sdk/log.NewBatchProcessor
         0     0%   100%  1536.94kB 10.05%  go.opentelemetry.io/otel/sdk/log.newQueue (inline)
         0     0%   100%   516.01kB  3.37%  hash/crc32.Update (inline)
         0     0%   100%   516.01kB  3.37%  hash/crc32.archInitIEEE (inline)
         0     0%   100%   516.01kB  3.37%  hash/crc32.init.func2
         0     0%   100%   516.01kB  3.37%  hash/crc32.update
         0     0%   100%  1536.94kB 10.05%  main.main
         0     0%   100%  1536.94kB 10.05%  main.run
         0     0%   100%   512.16kB  3.35%  net/http.(*conn).readRequest
         0     0%   100%   512.16kB  3.35%  net/http.(*conn).serve
```

### block

```
File: sagawise
Build ID: 526e7258f719cfce4b7b9945c1271d9dd63a2e7a
Type: delay
Time: 2026-09-05 08:22:59 PKT
Showing nodes accounting for 303.24s, 100% of 303.25s total
Dropped 138 nodes (cum <= 1.52s)
      flat  flat%   sum%        cum   cum%
   232.89s 76.80% 76.80%    232.89s 76.80%  runtime.selectgo
    60.24s 19.86% 96.66%     60.24s 19.86%  runtime.chansend1
     5.40s  1.78% 98.44%      5.40s  1.78%  sync.(*WaitGroup).Wait
     3.20s  1.05% 99.50%      3.20s  1.05%  sync.(*Mutex).Lock (inline)
     1.52s   0.5%   100%      1.52s   0.5%  sync.(*Cond).Wait
         0     0%   100%      3.22s  1.06%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%      3.22s  1.06%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%     72.32s 23.85%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
         0     0%   100%      2.22s  0.73%  log.(*Logger).output
         0     0%   100%      2.22s  0.73%  log.Printf
         0     0%   100%      3.15s  1.04%  main.httpTracing.func1
         0     0%   100%     13.05s  4.30%  net/http.(*ServeMux).ServeHTTP
         0     0%   100%     14.77s  4.87%  net/http.(*Server).Serve.gowrap3
         0     0%   100%     14.77s  4.87%  net/http.(*conn).serve
         0     0%   100%      1.52s   0.5%  net/http.(*connReader).abortPendingRead
         0     0%   100%      1.55s  0.51%  net/http.(*response).finishRequest
         0     0%   100%     13.13s  4.33%  net/http.HandlerFunc.ServeHTTP
         0     0%   100%     13.13s  4.33%  net/http.serverHandler.ServeHTTP
         0     0%   100%      9.90s  3.27%  net/http/pprof.Profile
         0     0%   100%      9.90s  3.26%  net/http/pprof.sleep
         0     0%   100%     72.27s 23.83%  wtfsaga/instance_engine.(*Engine).StartDeadlineReaper.func1
         0     0%   100%    143.82s 47.43%  wtfsaga/instance_engine.(*Worker).Start.func1
         0     0%   100%     65.63s 21.64%  wtfsaga/instance_engine.(*Worker).tick
```

### mutex

```
File: sagawise
Build ID: 526e7258f719cfce4b7b9945c1271d9dd63a2e7a
Type: delay
Time: 2026-09-05 08:22:59 PKT
Showing nodes accounting for 129.60s, 100% of 129.60s total
Dropped 406 nodes (cum <= 0.65s)
Showing top 25 nodes out of 75
      flat  flat%   sum%        cum   cum%
   119.52s 92.22% 92.22%    119.52s 92.22%  runtime.unlock (partial-inline)
     6.04s  4.66% 96.88%      6.04s  4.66%  runtime._LostContendedRuntimeLock
     4.04s  3.12%   100%      4.16s  3.21%  sync.(*Mutex).Unlock (partial-inline)
         0     0%   100%      3.63s  2.80%  _.goready.func1
         0     0%   100%      1.61s  1.24%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%      1.67s  1.29%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%      1.67s  1.29%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      0.69s  0.53%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0%   100%      0.69s  0.53%  github.com/redis/go-redis/v9.(*Script).Run
         0     0%   100%      1.61s  1.24%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      1.61s  1.24%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      1.61s  1.24%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%      1.61s  1.24%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%      1.63s  1.26%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%      1.67s  1.29%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%      0.69s  0.53%  github.com/redis/go-redis/v9.cmdable.EvalSha
         0     0%   100%      0.82s  0.63%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      0.69s  0.53%  github.com/redis/go-redis/v9.cmdable.eval
         0     0%   100%      5.34s  4.12%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%      5.34s  4.12%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%      2.29s  1.77%  internal/poll.(*FD).SetReadDeadline
         0     0%   100%      0.75s  0.58%  internal/poll.ignoringEINTRIO (inline)
         0     0%   100%      2.39s  1.84%  internal/poll.runtime_pollSetDeadline
         0     0%   100%      2.39s  1.84%  internal/poll.setDeadlineImpl
         0     0%   100%      3.06s  2.36%  log.(*Logger).output
```

### goroutine

```
File: sagawise
Build ID: 526e7258f719cfce4b7b9945c1271d9dd63a2e7a
Type: goroutine
Time: 2026-09-05 08:22:59 PKT
Showing nodes accounting for 69, 100% of 69 total
Showing top 25 nodes out of 101
      flat  flat%   sum%        cum   cum%
        66 95.65% 95.65%         66 95.65%  runtime.gopark
         1  1.45% 97.10%          1  1.45%  runtime.goroutineProfileWithLabels
         1  1.45% 98.55%          1  1.45%  runtime.notetsleepg
         1  1.45%   100%          1  1.45%  syscall.Syscall
         0     0%   100%         36 52.17%  bufio.(*Reader).Peek
         0     0%   100%         36 52.17%  bufio.(*Reader).fill
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5.(*Conn).execPrepared
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn.(*PgConn).ExecStatement
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn.(*PgConn).execExtendedSuffix
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn.(*PgConn).peekMessage
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn.(*PgConn).receiveMessage
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).readUntilRowDescription
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).receiveMessage
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).Read
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgproto3.(*Frontend).Receive
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgproto3.(*chunkReader).Next
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgxpool.(*Conn).Exec
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck
         0     0%   100%          1  1.45%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func5
         0     0%   100%         20 28.99%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%         20 28.99%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%         20 28.99%  github.com/redis/go-redis/v9.(*Client).Process
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 49 | 49 |
| start | json.get | 1.00 | 9 | 9 |
| start | evalsha | 0.01 | 16 | 0 |
| start | zrangebyscore | 0.01 | 4 | 0 |
| start | zrange | 0.01 | 2 | 0 |
| publish | evalsha | 1.00 | 106 | 106 |
| publish | json.set | 3.00 | 21 | 62 |
| publish | json.get | 2.00 | 13 | 27 |
| publish | zadd | 1.00 | 3 | 3 |
| consume | evalsha | 1.00 | 94 | 94 |
| consume | json.set | 2.00 | 26 | 51 |
| consume | json.get | 2.00 | 14 | 29 |
| consume | zrem | 1.00 | 2 | 2 |
| consume(final) | evalsha | 1.03 | 117 | 120 |
| consume(final) | json.set | 4.00 | 18 | 71 |
| consume(final) | json.get | 3.00 | 13 | 40 |
| consume(final) | zrem | 2.00 | 3 | 6 |
| consume(final) | zadd | 2.00 | 2 | 4 |
| consume(final) | hdel | 1.00 | 0 | 0 |
| consume(final) | zrangebyscore | 0.03 | 7 | 0 |
| consume(final) | zrange | 0.01 | 4 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 2.0 | 58 |
| publish | 7.0 | 198 |
| consume | 6.0 | 175 |
| consume(final) | 13.1 | 241 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 | 1.2 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.4 | 0.9 / 1.2 | 0.9 / 1.2 | 1.1 | 1.5 | 0.5 | 1456.3 |
| 100000 | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.2 | 3.0 | 3.3 | 0.5 | 1259.1 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.2 | 199.9 | 0.0 |
| 10 | 0.9 / 1.2 | 0.9 / 1.3 | 0.9 / 1.2 | 47.5 | 0.0 |
| 50 | 1.1 / 1.6 | 1.0 / 1.5 | 1.0 / 1.4 | 9.8 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 0.9 / 1.6 | 0.9 / 1.2 | 0.9 / 1.3 | 0.0 |
| 10000B | 1.0 / 1.4 | 1.0 / 1.4 | 0.9 / 1.2 | 0.0 |
| 500000B | 1.0 / 1.4 | 2.1 / 2.7 | 1.4 / 1.8 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 357 | 377 | 397 | 398 |
| 500 | 500 | 0 | 499 | 592 | 684 | 687 |
| 2000 | 2000 | 0 | 993 | 1357 | 2287 | 2295 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 4.1 | 6.1 | 6.9 | 7.9 |
| separate instances | 400 | 3.8 | 5.5 | 6.2 | 7.0 |

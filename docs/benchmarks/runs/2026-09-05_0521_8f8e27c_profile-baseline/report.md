# Profile: profile-baseline

- **Date:** 2026-09-05T05:21:04+05:00
- **Commit:** `8f8e27c` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 1518 sagas/s (~7590 requests/s). The next step, 2277 sagas/s, breached: p99 526 ms > 50 ms.
- Redis is near saturation at the knee: 97% of one core, ping p99 4.36 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis round-trips per request: start 2.0, publish 8.0, consume 7.0, final consume 11.0. Each round-trip is a sequential network hop, so per-request latency is roughly round-trips × RTT.
- Most expensive Redis work per request: json.set on consume(final) (4.0 calls × 63 µs).
- JSON.SET costs 73 µs against 12 µs for JSON.GET (6×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$..topic/from/to/failedAt`), so write count, not read count, is what Redis CPU pays for. A final consume does 4 writes.
- Instances already in Redis: consume p50 2.6 → 2.7 ms from 0 to 100000 instances (+2%). List endpoint p50 2.2 → 3.1 ms.
- Tasks per workflow: consume p50 2.8 → 4.6 ms from 2 to 50 tasks (+63%) at a constant ~1000 req/s. Growth here is the recursive-descent JSONPath and whole-document reads scaling with document size.
- Payload size: consume p50 2.7 → 3.6 ms from 100B to 500000B (+32%). Payloads live inside the document every JSONPath query scans; a consume never needs the payload.
- Simultaneous timeouts: max lag 783 ms at 100 → 1536 ms at 2000, i.e. ~0.4 ms per overdue task. The reaper is sequential; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 13.1 ms vs on separate instances 11.9 ms (+10%). Redis serializes writes to one key; the phase 6 Lua-per-transition design will make this the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 51.21%, instance_engine.(*Engine).handleConsumeOrFail 22.38%, instance_engine.(*Engine).handlePublish 20.75%, instance_engine.jsonMatches[go.shape.string] 18.68%, instance_engine.jsonFirstMatch[go.shape.string] 16.40%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 1518 sagas/s.** Breach: p99 526 ms > 50 ms.

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 998 | 0 (0.00%) | 1.0 / 1.5 | 2.8 / 3.7 | 2.9 / 4.3 | 44 | 0.29 / 0.61 | ok |
| 300 | 299 | 1496 | 0 (0.00%) | 1.1 / 1.9 | 3.2 / 4.7 | 3.4 / 5.4 | 55 | 0.32 / 0.91 | ok |
| 450 | 449 | 2243 | 0 (0.00%) | 1.1 / 2.6 | 3.6 / 5.8 | 3.8 / 6.4 | 63 | 0.36 / 0.88 | ok |
| 675 | 672 | 3361 | 0 (0.00%) | 1.3 / 3.5 | 4.4 / 7.8 | 4.7 / 8.5 | 69 | 0.43 / 1.20 | ok |
| 1012 | 1002 | 5006 | 0 (0.00%) | 1.7 / 4.5 | 6.0 / 9.7 | 6.4 / 10.9 | 77 | 0.57 / 2.36 | ok |
| 1518 | 1414 | 7068 | 0 (0.00%) | 2.5 / 6.3 | 9.1 / 16.7 | 9.5 / 18.8 | 90 | 0.93 / 2.67 | ok |
| 2277 | 1498 | 7489 | 0 (0.00%) | 46.7 / 110.5 | 200.1 / 425.9 | 219.3 / 526.1 | 97 | 1.94 / 4.36 | **breach** |

## 2. Server profiles at the knee (1518 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: 65adb4297a7b486ee8e408dfae1d358f27d422b2
Type: cpu
Time: 2026-09-05 05:21:55 PKT
Duration: 10s, Total samples = 28.96s (289.60%)
Showing nodes accounting for 15.73s, 54.32% of 28.96s total
Dropped 837 nodes (cum <= 0.14s)
Showing top 25 nodes out of 276
      flat  flat%   sum%        cum   cum%
     6.89s 23.79% 23.79%      6.89s 23.79%  internal/runtime/syscall/linux.Syscall6
     3.99s 13.78% 37.57%      3.99s 13.78%  runtime.futex
     0.50s  1.73% 39.30%      1.21s  4.18%  runtime.pcvalue
     0.45s  1.55% 40.85%      0.45s  1.55%  runtime.procyieldAsm
     0.37s  1.28% 42.13%      0.51s  1.76%  runtime.step
     0.29s  1.00% 43.13%      0.29s  1.00%  runtime.usleep
     0.27s  0.93% 44.06%      0.27s  0.93%  time.runtimeNow
     0.26s   0.9% 44.96%      0.71s  2.45%  runtime.(*unwinder).resolveInternal
     0.25s  0.86% 45.82%      0.25s  0.86%  runtime.nextFreeFast (inline)
     0.23s  0.79% 46.62%      0.32s  1.10%  runtime.scanObject
     0.21s  0.73% 47.34%      0.21s  0.73%  runtime.memmove
     0.20s  0.69% 48.03%      1.91s  6.60%  runtime.mallocgc
     0.19s  0.66% 48.69%      0.19s  0.66%  sync/atomic.(*Int32).Add (inline)
     0.18s  0.62% 49.31%     13.93s 48.10%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.17s  0.59% 49.90%      0.20s  0.69%  runtime.findfunc
     0.17s  0.59% 50.48%      0.24s  0.83%  runtime.mallocgcSmallScanNoHeaderSC2
     0.15s  0.52% 51.00%      0.15s  0.52%  indexbytebody
     0.15s  0.52% 51.52%      0.46s  1.59%  internal/poll.runtime_pollSetDeadline
     0.15s  0.52% 52.04%      0.15s  0.52%  runtime.memclrNoHeapPointers
     0.13s  0.45% 52.49%      0.24s  0.83%  runtime.mallocgcTinySC2
     0.12s  0.41% 52.90%      9.91s 34.22%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     0.12s  0.41% 53.31%      0.15s  0.52%  runtime.mallocgcSmallScanNoHeaderSC3
     0.11s  0.38% 53.69%      1.03s  3.56%  runtime.lock2
     0.09s  0.31% 54.01%      1.72s  5.94%  internal/poll.(*FD).Read
     0.09s  0.31% 54.32%      0.39s  1.35%  runtime.runqgrab
```

### cpu-cumulative

```
File: sagawise
Build ID: 65adb4297a7b486ee8e408dfae1d358f27d422b2
Type: cpu
Time: 2026-09-05 05:21:55 PKT
Duration: 10s, Total samples = 28.96s (289.60%)
Showing nodes accounting for 7.74s, 26.73% of 28.96s total
Dropped 837 nodes (cum <= 0.14s)
Showing top 40 nodes out of 276
      flat  flat%   sum%        cum   cum%
     0.01s 0.035% 0.035%     20.24s 69.89%  net/http.(*conn).serve
     0.07s  0.24%  0.28%     17.90s 61.81%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.28%     17.90s 61.81%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
     0.01s 0.035%  0.31%     17.90s 61.81%  net/http.HandlerFunc.ServeHTTP
         0     0%  0.31%     17.90s 61.81%  net/http.serverHandler.ServeHTTP
     0.01s 0.035%  0.35%     16.91s 58.39%  net/http.(*ServeMux).ServeHTTP
     0.01s 0.035%  0.38%     16.85s 58.18%  main.httpTracing.func1
     0.01s 0.035%  0.41%     14.83s 51.21%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.01s 0.035%  0.45%     13.94s 48.14%  github.com/redis/go-redis/v9.(*Client).Process
     0.18s  0.62%  1.07%     13.93s 48.10%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%  1.07%     13.93s 48.10%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.12s  0.41%  1.48%      9.91s 34.22%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%  1.48%      8.67s 29.94%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%  1.48%      8.64s 29.83%  github.com/redis/go-redis/v9.(*baseClient).processCommand
     0.05s  0.17%  1.66%      8.60s 29.70%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%  1.66%      8.55s 29.52%  github.com/redis/go-redis/v9.(*baseClient)._process
     0.01s 0.035%  1.69%      8.55s 29.52%  github.com/redis/go-redis/v9.(*baseClient).withConn
     0.02s 0.069%  1.76%      7.33s 25.31%  github.com/redis/go-redis/v9.cmdable.JSONGet
     6.89s 23.79% 25.55%      6.89s 23.79%  internal/runtime/syscall/linux.Syscall6
         0     0% 25.55%      6.58s 22.72%  internal/poll.ignoringEINTRIO (inline)
     0.05s  0.17% 25.73%      6.58s 22.72%  syscall.Syscall
     0.07s  0.24% 25.97%      6.52s 22.51%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
     0.01s 0.035% 26.00%      6.48s 22.38%  syscall.RawSyscall6
         0     0% 26.00%      6.48s 22.38%  wtfsaga/instance_engine.(*Engine).handleConsumeOrFail
         0     0% 26.00%      6.36s 21.96%  runtime.mcall
     0.04s  0.14% 26.14%      6.28s 21.69%  runtime.schedule
         0     0% 26.14%      6.10s 21.06%  runtime.park_m
     0.03s   0.1% 26.24%      6.01s 20.75%  wtfsaga/instance_engine.(*Engine).handlePublish
     0.07s  0.24% 26.48%         6s 20.72%  runtime.findRunnable
         0     0% 26.48%      5.60s 19.34%  github.com/redis/go-redis/v9.cmdable.JSONSet (inline)
         0     0% 26.48%      5.60s 19.34%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
     0.02s 0.069% 26.55%      5.60s 19.34%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0% 26.55%      5.41s 18.68%  wtfsaga/instance_engine.jsonMatches[go.shape.string]
     0.01s 0.035% 26.59%      5.19s 17.92%  internal/poll.(*FD).Write
         0     0% 26.59%      5.15s 17.78%  syscall.Write (inline)
         0     0% 26.59%      5.15s 17.78%  syscall.write
     0.02s 0.069% 26.66%      4.82s 16.64%  bufio.(*Writer).Flush
         0     0% 26.66%      4.75s 16.40%  wtfsaga/instance_engine.jsonFirstMatch[go.shape.string]
     0.01s 0.035% 26.69%      4.69s 16.19%  net.(*conn).Write
     0.01s 0.035% 26.73%      4.68s 16.16%  net.(*netFD).Write
```

### heap

```
File: sagawise
Build ID: 65adb4297a7b486ee8e408dfae1d358f27d422b2
Type: inuse_space
Time: 2026-09-05 05:22:05 PKT
Showing nodes accounting for 17921.74kB, 100% of 17921.74kB total
Showing top 25 nodes out of 73
      flat  flat%   sum%        cum   cum%
 5549.31kB 30.96% 30.96%  5549.31kB 30.96%  bufio.NewReaderSize (inline)
 4100.17kB 22.88% 53.84%  4100.17kB 22.88%  runtime.mallocgc
 3436.64kB 19.18% 73.02%  3436.64kB 19.18%  bufio.NewWriterSize (inline)
 1050.86kB  5.86% 78.88%  1050.86kB  5.86%  github.com/jackc/pgx/v5/internal/stmtcache.NewLRUCache (inline)
  669.43kB  3.74% 82.62%   669.43kB  3.74%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
  532.26kB  2.97% 85.59%   532.26kB  2.97%  github.com/redis/go-redis/v9/maintnotifications.newHandoffWorkerManager
  525.43kB  2.93% 88.52%   525.43kB  2.93%  github.com/jackc/pgx/v5/pgtype.(*Map).buildReflectTypeToType (inline)
  521.05kB  2.91% 91.43%   521.05kB  2.91%  encoding/xml.map.init.0
  512.31kB  2.86% 94.28%   512.31kB  2.86%  go.opentelemetry.io/otel/sdk/log.newRing
  512.25kB  2.86% 97.14%   512.25kB  2.86%  go.opentelemetry.io/otel/attribute.computeDataFixed
  512.03kB  2.86%   100%   512.03kB  2.86%  github.com/redis/go-redis/v9/internal/proto.NewWriter (inline)
         0     0%   100%      514kB  2.87%  bufio.NewReader
         0     0%   100%   521.05kB  2.91%  encoding/xml.init
         0     0%   100%  1576.28kB  8.80%  github.com/jackc/pgx/v5.ConnectConfig
         0     0%   100%  1576.28kB  8.80%  github.com/jackc/pgx/v5.connect
         0     0%   100%   525.43kB  2.93%  github.com/jackc/pgx/v5/pgtype.NewMap
         0     0%   100%   525.43kB  2.93%  github.com/jackc/pgx/v5/pgtype.initDefaultMap
         0     0%   100%  1576.28kB  8.80%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func3
         0     0%   100%  1576.28kB  8.80%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).initResourceValue.func1
         0     0%   100%   532.26kB  2.97%  github.com/redis/go-redis/v9.(*baseClient).enableMaintNotificationsUpgrades
         0     0%   100%   532.26kB  2.97%  github.com/redis/go-redis/v9.NewClient
         0     0%   100%  6850.03kB 38.22%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100%  6850.03kB 38.22%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100%  6850.03kB 38.22%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100%  6850.03kB 38.22%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
```

### block

```
File: sagawise
Build ID: 65adb4297a7b486ee8e408dfae1d358f27d422b2
Type: delay
Time: 2026-09-05 05:22:05 PKT
Showing nodes accounting for 2.86hrs, 99.93% of 2.86hrs total
Dropped 133 nodes (cum <= 0.01hrs)
Showing top 25 nodes out of 62
      flat  flat%   sum%        cum   cum%
   2.84hrs 99.34% 99.34%    2.84hrs 99.34%  runtime.selectgo
   0.02hrs   0.6% 99.93%    0.02hrs   0.6%  sync.(*Cond).Wait
         0     0% 99.93%    0.05hrs  1.71%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Acquire
         0     0% 99.93%    0.05hrs  1.71%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0% 99.93%    0.05hrs  1.71%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).Acquire
         0     0% 99.93%    0.05hrs  1.71%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).acquire
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0% 99.93%    2.76hrs 96.36%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 99.93%    2.76hrs 96.36%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 99.93%    2.76hrs 96.40%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0% 99.93%    1.45hrs 50.87%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0% 99.93%    0.99hrs 34.70%  github.com/redis/go-redis/v9.cmdable.JSONSet
         0     0% 99.93%    0.99hrs 34.70%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0% 99.93%    0.99hrs 34.70%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0% 99.93%    0.15hrs  5.41%  github.com/redis/go-redis/v9.cmdable.ZAdd
         0     0% 99.93%    0.15hrs  5.41%  github.com/redis/go-redis/v9.cmdable.ZAddArgs
```

### mutex

```
File: sagawise
Build ID: 65adb4297a7b486ee8e408dfae1d358f27d422b2
Type: delay
Time: 2026-09-05 05:22:05 PKT
Showing nodes accounting for 238.74s, 100% of 238.74s total
Dropped 377 nodes (cum <= 1.19s)
Showing top 25 nodes out of 112
      flat  flat%   sum%        cum   cum%
   219.92s 92.12% 92.12%    219.92s 92.12%  runtime.unlock (partial-inline)
    11.19s  4.69% 96.80%     11.19s  4.69%  runtime._LostContendedRuntimeLock
     7.63s  3.20%   100%      7.95s  3.33%  sync.(*Mutex).Unlock (inline)
         0     0%   100%      6.41s  2.68%  _.goready.func1
         0     0%   100%     18.42s  7.72%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%     18.49s  7.75%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%     18.49s  7.75%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      5.06s  2.12%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0%   100%      8.29s  3.47%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0%   100%     18.39s  7.70%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      8.29s  3.47%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0%   100%     18.39s  7.70%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      5.06s  2.12%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0%   100%     18.39s  7.70%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%     18.39s  7.70%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%      9.41s  3.94%  github.com/redis/go-redis/v9.(*baseClient).releaseConn
         0     0%   100%      9.41s  3.94%  github.com/redis/go-redis/v9.(*baseClient).releaseConnToPool
         0     0%   100%     18.39s  7.70%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%      9.41s  3.94%  github.com/redis/go-redis/v9.(*baseClient).withConn.func1
         0     0%   100%     18.49s  7.75%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%     10.01s  4.19%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      6.78s  2.84%  github.com/redis/go-redis/v9.cmdable.JSONSet
         0     0%   100%      6.78s  2.84%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0%   100%      6.78s  2.84%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0%   100%      4.39s  1.84%  github.com/redis/go-redis/v9/internal.(*FastSemaphore).Acquire
```

### goroutine

```
File: sagawise
Build ID: 65adb4297a7b486ee8e408dfae1d358f27d422b2
Type: goroutine
Time: 2026-09-05 05:22:05 PKT
Showing nodes accounting for 137, 98.56% of 139 total
Showing top 25 nodes out of 147
      flat  flat%   sum%        cum   cum%
       128 92.09% 92.09%        128 92.09%  runtime.gopark
         1  0.72% 92.81%          1  0.72%  internal/poll.setDeadlineImpl
         1  0.72% 93.53%          2  1.44%  runtime.(*Frames).Next
         1  0.72% 94.24%          1  0.72%  runtime.funcfile
         1  0.72% 94.96%          1  0.72%  runtime.goroutineProfileWithLabels
         1  0.72% 95.68%          1  0.72%  runtime.mallocgc
         1  0.72% 96.40%          1  0.72%  runtime.notetsleepg
         1  0.72% 97.12%          1  0.72%  strings.ToLower
         1  0.72% 97.84%          1  0.72%  syscall.Syscall
         1  0.72% 98.56%          1  0.72%  syscall.Syscall6
         0     0% 98.56%         64 46.04%  bufio.(*Reader).Peek
         0     0% 98.56%          1  0.72%  bufio.(*Reader).ReadByte
         0     0% 98.56%         65 46.76%  bufio.(*Reader).fill
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5.(*Conn).execPrepared
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn.(*PgConn).ExecStatement
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn.(*PgConn).execExtendedSuffix
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn.(*PgConn).peekMessage
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn.(*PgConn).receiveMessage
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).readUntilRowDescription
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).receiveMessage
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).Read
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgproto3.(*Frontend).Receive
         0     0% 98.56%          4  2.88%  github.com/jackc/pgx/v5/pgproto3.(*chunkReader).Next
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 65 | 65 |
| start | json.get | 1.00 | 10 | 10 |
| publish | json.set | 3.00 | 65 | 196 |
| publish | json.get | 4.00 | 12 | 47 |
| publish | zadd | 1.00 | 6 | 6 |
| publish | zrange | 0.01 | 4 | 0 |
| consume | json.set | 2.00 | 73 | 145 |
| consume | json.get | 4.00 | 12 | 49 |
| consume | zrem | 1.00 | 4 | 4 |
| consume(final) | json.set | 4.00 | 63 | 250 |
| consume(final) | json.get | 6.00 | 11 | 67 |
| consume(final) | zrem | 1.00 | 4 | 4 |
| consume(final) | zrange | 0.01 | 9 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 2.0 | 74 |
| publish | 8.0 | 248 |
| consume | 7.0 | 198 |
| consume(final) | 11.0 | 321 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.2 | 2.4 / 3.0 | 2.6 / 3.5 | 2.2 | 2.4 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.4 | 2.4 / 3.1 | 2.7 / 3.7 | 2.5 | 3.0 | 0.5 | -1612.6 |
| 100000 | 0.9 / 1.4 | 2.4 / 3.1 | 2.7 / 3.8 | 3.1 | 3.5 | 0.5 | 880.0 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 1.0 / 1.4 | 2.7 / 3.5 | 2.8 / 4.1 | 199.7 | 0.0 |
| 10 | 1.0 / 1.5 | 2.9 / 3.9 | 2.6 / 4.3 | 47.3 | 0.0 |
| 50 | 1.7 / 2.6 | 5.2 / 7.9 | 4.6 / 7.3 | 9.4 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 0.9 / 1.4 | 2.4 / 3.0 | 2.7 / 3.6 | 0.0 |
| 10000B | 1.0 / 1.5 | 2.5 / 3.2 | 2.8 / 3.7 | 0.0 |
| 500000B | 1.1 / 2.1 | 3.3 / 4.4 | 3.6 / 4.7 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 778 | 780 | 783 | 783 |
| 500 | 500 | 0 | 631 | 1023 | 1097 | 1098 |
| 2000 | 2000 | 0 | 918 | 1412 | 1534 | 1536 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 13.1 | 14.7 | 16.8 | 17.5 |
| separate instances | 400 | 11.9 | 15.5 | 16.2 | 16.9 |

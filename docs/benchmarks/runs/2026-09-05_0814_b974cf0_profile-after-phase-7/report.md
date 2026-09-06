# Profile: profile-after-phase-7

- **Date:** 2026-09-05T08:14:39+05:00
- **Commit:** `b974cf0` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 2277 sagas/s (~11385 requests/s). The next step, 3415 sagas/s, breached: achieved 2951 < 90% of target 3415 (load generator or server saturated).
- Redis is near saturation at the knee: 85% of one core, ping p99 2.07 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis commands per request: start 3.1, publish 6.0, consume 5.0, final consume 11.1 (INFO commandstats; commands run inside the transition script count too, so this is Redis work, not client round-trips, which are 2 per report since phase 6).
- Most expensive Redis work per request: evalsha on consume(final) (1.0 calls × 90 µs).
- JSON.SET costs 39 µs against 13 µs for JSON.GET (3×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$.tasks[*].topic/from/to` and the instance stamps), so write count, not read count, is what Redis CPU pays for. A final consume does 0 writes.
- Instances already in Redis: consume p50 0.9 → 0.9 ms from 0 to 100000 instances (+6%). List endpoint p50 1.2 → 2.5 ms.
- Tasks per workflow: consume p50 0.8 → 0.9 ms from 2 to 50 tasks (+11%) at a constant ~1000 req/s. Growth here is document size: the script reads every task's state and each JSON.SET re-indexes the document.
- Payload size: consume p50 0.9 → 1.1 ms from 100B to 500000B (+26%). The payload travels with the publish and is written by the script's JSON.SET; a consume never reads it.
- Simultaneous timeouts: max lag 652 ms at 100 → 130 ms at 2000, i.e. ~-0.3 ms per overdue task. The reaper runs one script call per overdue member, sequentially; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 3.4 ms vs on separate instances 3.3 ms (+5%). Redis runs one transition script at a time, so reports on one instance queue behind each other; this is the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 31.33%, instance_engine.(*Engine).jsonGet 17.61%, instance_engine.(*Engine).transition 13.60%, instance_engine.(*Engine).readTaskIdentity 13.42%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 2277 sagas/s.** Breach: achieved 2951 < 90% of target 3415 (load generator or server saturated).

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 999 | 0 (0.00%) | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.2 | 25 | 0.27 / 0.46 | ok |
| 300 | 300 | 1498 | 0 (0.00%) | 0.9 / 1.4 | 0.9 / 1.3 | 0.9 / 1.3 | 32 | 0.29 / 0.53 | ok |
| 450 | 450 | 2248 | 0 (0.00%) | 1.0 / 1.4 | 1.0 / 1.4 | 1.0 / 1.4 | 41 | 0.30 / 0.54 | ok |
| 675 | 674 | 3371 | 0 (0.00%) | 1.0 / 1.6 | 1.0 / 1.6 | 1.0 / 1.6 | 54 | 0.33 / 0.67 | ok |
| 1012 | 1010 | 5048 | 0 (0.00%) | 1.2 / 1.9 | 1.2 / 1.9 | 1.2 / 1.9 | 62 | 0.39 / 0.74 | ok |
| 1518 | 1509 | 7541 | 0 (0.00%) | 1.4 / 2.5 | 1.4 / 2.4 | 1.4 / 2.4 | 71 | 0.46 / 1.12 | ok |
| 2277 | 2167 | 10827 | 0 (0.00%) | 1.7 / 3.7 | 1.7 / 3.5 | 1.7 / 3.4 | 75 | 0.58 / 1.66 | ok |
| 3415 | 2951 | 14739 | 0 (0.00%) | 2.1 / 5.0 | 2.2 / 5.0 | 2.2 / 4.9 | 85 | 0.73 / 2.07 | **breach** |

## 2. Server profiles at the knee (2277 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: b246a40b6e92940ee886f294e413a970092b1224
Type: cpu
Time: 2026-09-05 08:15:47 PKT
Duration: 10s, Total samples = 27.42s (274.17%)
Showing nodes accounting for 15.40s, 56.16% of 27.42s total
Dropped 836 nodes (cum <= 0.14s)
Showing top 25 nodes out of 254
      flat  flat%   sum%        cum   cum%
     6.40s 23.34% 23.34%      6.40s 23.34%  internal/runtime/syscall/linux.Syscall6
     4.56s 16.63% 39.97%      4.56s 16.63%  runtime.futex
     0.44s  1.60% 41.58%      0.44s  1.60%  runtime.usleep
     0.43s  1.57% 43.14%      0.43s  1.57%  runtime.procyieldAsm
     0.38s  1.39% 44.53%      0.43s  1.57%  runtime.step
     0.30s  1.09% 45.62%      0.39s  1.42%  runtime.scanObject
     0.28s  1.02% 46.64%      0.28s  1.02%  runtime.memmove
     0.21s  0.77% 47.41%      1.13s  4.12%  runtime.lock2
     0.20s  0.73% 48.14%      0.20s  0.73%  time.runtimeNow
     0.19s  0.69% 48.83%      0.82s  2.99%  runtime.pcvalue
     0.19s  0.69% 49.53%      0.19s  0.69%  runtime.write1
     0.18s  0.66% 50.18%      0.46s  1.68%  runtime.(*unwinder).resolveInternal
     0.16s  0.58% 50.77%      0.16s  0.58%  runtime.nextFreeFast (inline)
     0.16s  0.58% 51.35%      0.16s  0.58%  runtime.osyield
     0.14s  0.51% 51.86%      0.15s  0.55%  runtime.casgstatus
     0.14s  0.51% 52.37%      0.14s  0.51%  runtime.memclrNoHeapPointers
     0.13s  0.47% 52.84%      0.43s  1.57%  internal/poll.runtime_pollSetDeadline
     0.13s  0.47% 53.32%      0.58s  2.12%  runtime.runqgrab
     0.12s  0.44% 53.76%      0.14s  0.51%  runtime.findfunc
     0.12s  0.44% 54.19%      1.44s  5.25%  runtime.mallocgc
     0.12s  0.44% 54.63%      0.18s  0.66%  runtime.mallocgcSmallScanNoHeaderSC2
     0.12s  0.44% 55.07%      0.79s  2.88%  runtime.stealWork
     0.11s   0.4% 55.47%      7.27s 26.51%  runtime.findRunnable
     0.10s  0.36% 55.84%      0.91s  3.32%  runtime.tracebackPCs
     0.09s  0.33% 56.16%     13.62s 49.67%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
```

### cpu-cumulative

```
File: sagawise
Build ID: b246a40b6e92940ee886f294e413a970092b1224
Type: cpu
Time: 2026-09-05 08:15:47 PKT
Duration: 10s, Total samples = 27.42s (274.17%)
Showing nodes accounting for 11.62s, 42.38% of 27.42s total
Dropped 836 nodes (cum <= 0.14s)
Showing top 40 nodes out of 254
      flat  flat%   sum%        cum   cum%
     0.04s  0.15%  0.15%     17.86s 65.13%  net/http.(*conn).serve
     0.01s 0.036%  0.18%     13.63s 49.71%  net/http.HandlerFunc.ServeHTTP
         0     0%  0.18%     13.63s 49.71%  net/http.serverHandler.ServeHTTP
     0.09s  0.33%  0.51%     13.62s 49.67%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.51%     13.62s 49.67%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
     0.02s 0.073%  0.58%     12.23s 44.60%  net/http.(*ServeMux).ServeHTTP
     0.04s  0.15%  0.73%     12.09s 44.09%  main.httpTracing.func1
     0.07s  0.26%  0.98%      8.59s 31.33%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.01s 0.036%  1.02%      8.41s 30.67%  github.com/redis/go-redis/v9.(*Client).Process
     0.03s  0.11%  1.13%      8.40s 30.63%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.03s  0.11%  1.24%      8.35s 30.45%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.01s 0.036%  1.28%      7.74s 28.23%  runtime.mcall
     0.01s 0.036%  1.31%      7.74s 28.23%  runtime.schedule
         0     0%  1.31%      7.66s 27.94%  runtime.park_m
     0.11s   0.4%  1.71%      7.27s 26.51%  runtime.findRunnable
     6.40s 23.34% 25.05%      6.40s 23.34%  internal/runtime/syscall/linux.Syscall6
     0.02s 0.073% 25.13%      6.12s 22.32%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 25.13%      5.98s 21.81%  syscall.Syscall
     0.02s 0.073% 25.20%      5.96s 21.74%  syscall.RawSyscall6
         0     0% 25.20%      5.92s 21.59%  internal/poll.ignoringEINTRIO (inline)
         0     0% 25.20%      5.53s 20.17%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 25.20%      5.53s 20.17%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 25.20%      5.51s 20.09%  github.com/redis/go-redis/v9.(*baseClient).processCommand
     0.01s 0.036% 25.24%      5.48s 19.99%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 25.24%      5.48s 19.99%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
     0.02s 0.073% 25.31%      4.99s 18.20%  internal/poll.(*FD).Write
         0     0% 25.31%      4.92s 17.94%  syscall.Write (inline)
         0     0% 25.31%      4.92s 17.94%  syscall.write
         0     0% 25.31%      4.83s 17.61%  wtfsaga/instance_engine.(*Engine).jsonGet
     0.01s 0.036% 25.35%      4.77s 17.40%  bufio.(*Writer).Flush
     4.56s 16.63% 41.98%      4.56s 16.63%  runtime.futex
     0.06s  0.22% 42.20%      4.42s 16.12%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
     0.02s 0.073% 42.27%      4.28s 15.61%  net.(*conn).Write
         0     0% 42.27%      4.26s 15.54%  net.(*netFD).Write
         0     0% 42.27%      4.20s 15.32%  github.com/redis/go-redis/v9.cmdable.JSONGet
     0.01s 0.036% 42.30%      3.73s 13.60%  wtfsaga/instance_engine.(*Engine).transition
         0     0% 42.30%      3.68s 13.42%  wtfsaga/instance_engine.(*Engine).readTaskIdentity
         0     0% 42.30%      3.49s 12.73%  github.com/redis/go-redis/v9.(*Script).Run
     0.02s 0.073% 42.38%      3.48s 12.69%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 42.38%      3.46s 12.62%  github.com/redis/go-redis/v9.cmdable.EvalSha
```

### heap

```
File: sagawise
Build ID: b246a40b6e92940ee886f294e413a970092b1224
Type: inuse_space
Time: 2026-09-05 08:15:57 PKT
Showing nodes accounting for 7386.37kB, 100% of 7386.37kB total
Showing top 25 nodes out of 47
      flat  flat%   sum%        cum   cum%
 3586.99kB 48.56% 48.56%  3586.99kB 48.56%  runtime.mallocgc
 1056.33kB 14.30% 62.86%  1056.33kB 14.30%  bufio.NewWriterSize (inline)
  669.43kB  9.06% 71.93%   669.43kB  9.06%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
  528.17kB  7.15% 79.08%   528.17kB  7.15%  bufio.NewReaderSize (inline)
  521.05kB  7.05% 86.13%   521.05kB  7.05%  encoding/xml.map.init.0
  512.38kB  6.94% 93.07%   512.38kB  6.94%  github.com/jackc/pgx/v5/pgproto3.(*Query).Encode
  512.02kB  6.93%   100%   512.02kB  6.93%  context.(*cancelCtx).propagateCancel
         0     0%   100%   512.02kB  6.93%  context.WithCancel
         0     0%   100%   512.02kB  6.93%  context.withCancel (inline)
         0     0%   100%   521.05kB  7.05%  encoding/xml.init
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5.(*Conn).execSimpleProtocol
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5/pgconn.(*PgConn).Exec
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5/pgproto3.(*Frontend).SendQuery
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5/pgxpool.(*Conn).Exec
         0     0%   100%   512.38kB  6.94%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0%   100%  1584.50kB 21.45%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100%  1584.50kB 21.45%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100%  1584.50kB 21.45%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100%  1584.50kB 21.45%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
         0     0%   100%   528.17kB  7.15%  github.com/redis/go-redis/v9/internal/proto.NewReaderSize (inline)
         0     0%   100%   512.38kB  6.94%  main.main
         0     0%   100%   512.38kB  6.94%  main.run
         0     0%   100%   512.02kB  6.93%  net/http.(*conn).readRequest
```

### block

```
File: sagawise
Build ID: b246a40b6e92940ee886f294e413a970092b1224
Type: delay
Time: 2026-09-05 08:15:57 PKT
Showing nodes accounting for 310.25s, 99.51% of 311.78s total
Dropped 132 nodes (cum <= 1.56s)
      flat  flat%   sum%        cum   cum%
   239.15s 76.71% 76.71%    239.15s 76.71%  runtime.selectgo
    62.14s 19.93% 96.64%     62.14s 19.93%  runtime.chansend1
     5.51s  1.77% 98.40%      5.51s  1.77%  sync.(*WaitGroup).Wait
     3.45s  1.11% 99.51%      3.45s  1.11%  sync.(*Mutex).Lock (inline)
         0     0% 99.51%      3.34s  1.07%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0% 99.51%      3.34s  1.07%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0% 99.51%     74.30s 23.83%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
         0     0% 99.51%      2.37s  0.76%  log.(*Logger).output
         0     0% 99.51%      2.37s  0.76%  log.Printf
         0     0% 99.51%      3.27s  1.05%  main.httpTracing.func1
         0     0% 99.51%     13.40s  4.30%  net/http.(*ServeMux).ServeHTTP
         0     0% 99.51%     15.11s  4.85%  net/http.(*Server).Serve.gowrap3
         0     0% 99.51%     15.11s  4.85%  net/http.(*conn).serve
         0     0% 99.51%     13.47s  4.32%  net/http.HandlerFunc.ServeHTTP
         0     0% 99.51%     13.47s  4.32%  net/http.serverHandler.ServeHTTP
         0     0% 99.51%     10.13s  3.25%  net/http/pprof.Profile
         0     0% 99.51%     10.12s  3.25%  net/http/pprof.sleep
         0     0% 99.51%     74.24s 23.81%  wtfsaga/instance_engine.(*Engine).StartDeadlineReaper.func1
         0     0% 99.51%    148.05s 47.48%  wtfsaga/instance_engine.(*Worker).Start.func1
         0     0% 99.51%     67.65s 21.70%  wtfsaga/instance_engine.(*Worker).tick
```

### mutex

```
File: sagawise
Build ID: b246a40b6e92940ee886f294e413a970092b1224
Type: delay
Time: 2026-09-05 08:15:57 PKT
Showing nodes accounting for 136.83s, 100% of 136.83s total
Dropped 385 nodes (cum <= 0.68s)
Showing top 25 nodes out of 81
      flat  flat%   sum%        cum   cum%
   127.08s 92.87% 92.87%    127.08s 92.87%  runtime.unlock (partial-inline)
     5.80s  4.24% 97.12%      5.80s  4.24%  runtime._LostContendedRuntimeLock
     3.95s  2.88%   100%      4.06s  2.97%  sync.(*Mutex).Unlock (partial-inline)
         0     0%   100%      3.76s  2.75%  _.goready.func1
         0     0%   100%      1.71s  1.25%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%      1.73s  1.26%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%      1.73s  1.26%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      0.71s  0.52%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0%   100%      0.71s  0.52%  github.com/redis/go-redis/v9.(*Script).Run
         0     0%   100%      1.70s  1.24%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      1.70s  1.24%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      1.70s  1.24%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%      1.70s  1.24%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%      1.72s  1.26%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%      1.73s  1.26%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%      0.71s  0.52%  github.com/redis/go-redis/v9.cmdable.EvalSha
         0     0%   100%      0.82s   0.6%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      0.71s  0.52%  github.com/redis/go-redis/v9.cmdable.eval
         0     0%   100%      5.70s  4.17%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%      5.70s  4.17%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%      2.63s  1.93%  internal/poll.(*FD).SetReadDeadline
         0     0%   100%      0.83s   0.6%  internal/poll.ignoringEINTRIO (inline)
         0     0%   100%      2.75s  2.01%  internal/poll.runtime_pollSetDeadline
         0     0%   100%      2.75s  2.01%  internal/poll.setDeadlineImpl
         0     0%   100%      3.08s  2.25%  log.(*Logger).output
```

### goroutine

```
File: sagawise
Build ID: b246a40b6e92940ee886f294e413a970092b1224
Type: goroutine
Time: 2026-09-05 08:15:57 PKT
Showing nodes accounting for 58, 93.55% of 62 total
Showing top 25 nodes out of 134
      flat  flat%   sum%        cum   cum%
        50 80.65% 80.65%         50 80.65%  runtime.gopark
         3  4.84% 85.48%          3  4.84%  syscall.Syscall
         1  1.61% 87.10%          1  1.61%  encoding/json/jsontext.(*decoderState).reset
         1  1.61% 88.71%          1  1.61%  go.opentelemetry.io/otel/sdk/trace.(*recordingSpan).IsRecording
         1  1.61% 90.32%          1  1.61%  runtime.goroutineProfileWithLabels
         1  1.61% 91.94%          1  1.61%  runtime.notetsleepg
         1  1.61% 93.55%          1  1.61%  syscall.Syscall6
         0     0% 93.55%         33 53.23%  bufio.(*Reader).Peek
         0     0% 93.55%         33 53.23%  bufio.(*Reader).fill
         0     0% 93.55%          1  1.61%  bufio.(*Writer).Flush
         0     0% 93.55%          1  1.61%  encoding/json.Unmarshal
         0     0% 93.55%          1  1.61%  encoding/json/jsontext.export.GetBufferedDecoder (inline)
         0     0% 93.55%          1  1.61%  encoding/json/jsontext.getBufferedDecoder
         0     0% 93.55%          1  1.61%  encoding/json/v2.Unmarshal
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5.(*Conn).execPrepared
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn.(*PgConn).ExecStatement
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn.(*PgConn).execExtendedSuffix
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn.(*PgConn).peekMessage
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn.(*PgConn).receiveMessage
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).readUntilRowDescription
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).receiveMessage
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).Read
         0     0% 93.55%          1  1.61%  github.com/jackc/pgx/v5/pgproto3.(*Frontend).Receive
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 39 | 39 |
| start | json.get | 1.38 | 6 | 9 |
| start | zrem | 0.38 | 1 | 0 |
| start | hdel | 0.38 | 0 | 0 |
| publish | evalsha | 1.00 | 89 | 89 |
| publish | json.set | 1.00 | 35 | 35 |
| publish | json.get | 2.00 | 13 | 26 |
| publish | json.merge | 1.00 | 14 | 14 |
| publish | zadd | 1.00 | 3 | 3 |
| consume | evalsha | 1.00 | 76 | 76 |
| consume | json.merge | 1.00 | 38 | 38 |
| consume | json.get | 2.00 | 12 | 25 |
| consume | zrem | 1.00 | 2 | 2 |
| consume(final) | evalsha | 1.02 | 90 | 92 |
| consume(final) | json.merge | 2.00 | 22 | 44 |
| consume(final) | json.get | 3.00 | 12 | 35 |
| consume(final) | zrem | 2.00 | 3 | 5 |
| consume(final) | zadd | 2.00 | 2 | 3 |
| consume(final) | hdel | 1.00 | 0 | 0 |
| consume(final) | zrangebyscore | 0.03 | 10 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 3.1 | 48 |
| publish | 6.0 | 166 |
| consume | 5.0 | 141 |
| consume(final) | 11.1 | 181 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.2 | 0.9 / 1.2 | 0.9 / 1.2 | 1.2 | 1.4 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 / 1.2 | 1.5 | 1.8 | 0.5 | 1430.2 |
| 100000 | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.2 | 2.5 | 2.8 | 0.5 | 1213.5 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 0.8 / 1.1 | 0.9 / 1.2 | 0.8 / 1.2 | 199.9 | 0.0 |
| 10 | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 / 1.2 | 47.5 | 0.0 |
| 50 | 1.1 / 1.5 | 1.0 / 1.4 | 0.9 / 1.3 | 9.8 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 1.0 / 1.4 | 0.9 / 1.4 | 0.9 / 1.3 | 0.0 |
| 10000B | 1.0 / 1.4 | 1.0 / 1.3 | 0.9 / 1.2 | 0.0 |
| 500000B | 1.0 / 1.4 | 2.2 / 2.9 | 1.1 / 1.5 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 644 | 648 | 652 | 652 |
| 500 | 500 | 0 | 460 | 480 | 501 | 501 |
| 2000 | 2000 | 0 | -959 | -876 | 128 | 130 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 3.4 | 5.4 | 6.2 | 7.8 |
| separate instances | 400 | 3.3 | 5.5 | 6.2 | 6.6 |

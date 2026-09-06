# Profile: profile-after-phase-7

- **Date:** 2026-09-05T08:06:58+05:00
- **Commit:** `d13f921` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 2277 sagas/s (~11385 requests/s). The next step, 3415 sagas/s, breached: achieved 2922 < 90% of target 3415 (load generator or server saturated).
- Redis is near saturation at the knee: 84% of one core, ping p99 3.41 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis commands per request: start 2.0, publish 6.0, consume 5.0, final consume 11.1 (INFO commandstats; commands run inside the transition script count too, so this is Redis work, not client round-trips, which are 2 per report since phase 6).
- Most expensive Redis work per request: evalsha on publish (1.0 calls × 99 µs).
- JSON.SET costs 48 µs against 14 µs for JSON.GET (3×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$.tasks[*].topic/from/to` and the instance stamps), so write count, not read count, is what Redis CPU pays for. A final consume does 0 writes.
- Instances already in Redis: consume p50 0.9 → 0.9 ms from 0 to 100000 instances (-0%). List endpoint p50 1.8 → 2.4 ms.
- Tasks per workflow: consume p50 0.9 → 0.9 ms from 2 to 50 tasks (+3%) at a constant ~1000 req/s. Growth here is document size: the script reads every task's state and each JSON.SET re-indexes the document.
- Payload size: consume p50 0.9 → 1.1 ms from 100B to 500000B (+31%). The payload travels with the publish and is written by the script's JSON.SET; a consume never reads it.
- Simultaneous timeouts: max lag 531 ms at 100 → 1057 ms at 2000, i.e. ~0.3 ms per overdue task. The reaper runs one script call per overdue member, sequentially; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 3.2 ms vs on separate instances 3.0 ms (+4%). Redis runs one transition script at a time, so reports on one instance queue behind each other; this is the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 33.66%, instance_engine.(*Engine).jsonGet 19.30%, instance_engine.(*Engine).readTaskIdentity 15.58%, instance_engine.(*Engine).transition 13.94%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 2277 sagas/s.** Breach: achieved 2922 < 90% of target 3415 (load generator or server saturated).

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 999 | 0 (0.00%) | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.3 | 24 | 0.25 / 0.44 | ok |
| 300 | 300 | 1498 | 0 (0.00%) | 0.9 / 1.5 | 0.9 / 1.3 | 0.9 / 1.3 | 31 | 0.27 / 0.54 | ok |
| 450 | 450 | 2248 | 0 (0.00%) | 0.9 / 1.5 | 0.9 / 1.4 | 0.9 / 1.5 | 41 | 0.28 / 0.56 | ok |
| 675 | 674 | 3369 | 0 (0.00%) | 1.0 / 1.8 | 1.0 / 1.7 | 1.0 / 1.7 | 52 | 0.33 / 0.69 | ok |
| 1012 | 1009 | 5041 | 0 (0.00%) | 1.2 / 2.8 | 1.2 / 2.2 | 1.2 / 2.2 | 62 | 0.37 / 0.87 | ok |
| 1518 | 1495 | 7471 | 0 (0.00%) | 1.3 / 3.1 | 1.3 / 2.8 | 1.3 / 2.7 | 69 | 0.45 / 1.11 | ok |
| 2277 | 2149 | 10735 | 0 (0.00%) | 1.7 / 4.0 | 1.7 / 3.8 | 1.7 / 3.8 | 75 | 0.58 / 1.97 | ok |
| 3415 | 2922 | 14594 | 0 (0.00%) | 2.1 / 9.3 | 2.1 / 7.0 | 2.1 / 7.1 | 84 | 0.72 / 3.41 | **breach** |

## 2. Server profiles at the knee (2277 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: 398df6886c2dd9dead2ef85ad93a84731c071d6d
Type: cpu
Time: 2026-09-05 08:08:05 PKT
Duration: 10s, Total samples = 26.32s (263.23%)
Showing nodes accounting for 14.98s, 56.91% of 26.32s total
Dropped 852 nodes (cum <= 0.13s)
Showing top 25 nodes out of 238
      flat  flat%   sum%        cum   cum%
     6.46s 24.54% 24.54%      6.46s 24.54%  internal/runtime/syscall/linux.Syscall6
     4.63s 17.59% 42.14%      4.63s 17.59%  runtime.futex
     0.41s  1.56% 43.69%      0.41s  1.56%  runtime.procyieldAsm
     0.36s  1.37% 45.06%      0.36s  1.37%  runtime.usleep
     0.27s  1.03% 46.09%      0.66s  2.51%  runtime.pcvalue
     0.26s  0.99% 47.07%      0.26s  0.99%  runtime.memmove
     0.22s  0.84% 47.91%      0.30s  1.14%  runtime.step
     0.21s   0.8% 48.71%      0.21s   0.8%  runtime.memclrNoHeapPointers
     0.19s  0.72% 49.43%      0.19s  0.72%  runtime.write1
     0.18s  0.68% 50.11%      1.65s  6.27%  runtime.mallocgc
     0.18s  0.68% 50.80%      0.27s  1.03%  runtime.scanObject
     0.17s  0.65% 51.44%      0.17s  0.65%  runtime.nextFreeFast (inline)
     0.16s  0.61% 52.05%      0.16s  0.61%  time.runtimeNow
     0.15s  0.57% 52.62%      0.15s  0.57%  runtime.findfunc
     0.14s  0.53% 53.15%      0.38s  1.44%  runtime.(*unwinder).resolveInternal
     0.14s  0.53% 53.69%      0.22s  0.84%  runtime.mallocgcSmallScanNoHeaderSC2
     0.13s  0.49% 54.18%      6.64s 25.23%  runtime.findRunnable
     0.12s  0.46% 54.64%      0.54s  2.05%  internal/poll.runtime_pollSetDeadline
     0.10s  0.38% 55.02%      0.94s  3.57%  runtime.lock2
     0.10s  0.38% 55.40%      0.22s  0.84%  runtime.mapaccess2_faststr
     0.09s  0.34% 55.74%      0.53s  2.01%  runtime.growslice
     0.09s  0.34% 56.08%      1.46s  5.55%  runtime.unlock2
     0.08s   0.3% 56.38%      0.46s  1.75%  runtime.runqgrab
     0.07s  0.27% 56.65%      6.21s 23.59%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     0.07s  0.27% 56.91%      0.66s  2.51%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).getConn
```

### cpu-cumulative

```
File: sagawise
Build ID: 398df6886c2dd9dead2ef85ad93a84731c071d6d
Type: cpu
Time: 2026-09-05 08:08:05 PKT
Duration: 10s, Total samples = 26.32s (263.23%)
Showing nodes accounting for 11.84s, 44.98% of 26.32s total
Dropped 852 nodes (cum <= 0.13s)
Showing top 40 nodes out of 238
      flat  flat%   sum%        cum   cum%
     0.05s  0.19%  0.19%     17.62s 66.95%  net/http.(*conn).serve
     0.02s 0.076%  0.27%     13.60s 51.67%  net/http.HandlerFunc.ServeHTTP
         0     0%  0.27%     13.60s 51.67%  net/http.serverHandler.ServeHTTP
     0.05s  0.19%  0.46%     13.58s 51.60%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.46%     13.58s 51.60%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
     0.01s 0.038%  0.49%     12.12s 46.05%  net/http.(*ServeMux).ServeHTTP
     0.01s 0.038%  0.53%     12.02s 45.67%  main.httpTracing.func1
     0.03s  0.11%  0.65%      8.86s 33.66%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.01s 0.038%  0.68%      8.63s 32.79%  github.com/redis/go-redis/v9.(*Client).Process
     0.04s  0.15%  0.84%      8.62s 32.75%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%  0.84%      8.62s 32.75%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.02s 0.076%  0.91%      7.32s 27.81%  runtime.mcall
     0.03s  0.11%  1.03%      7.19s 27.32%  runtime.park_m
     0.06s  0.23%  1.25%      7.18s 27.28%  runtime.schedule
     0.13s  0.49%  1.75%      6.64s 25.23%  runtime.findRunnable
     6.46s 24.54% 26.29%      6.46s 24.54%  internal/runtime/syscall/linux.Syscall6
     0.07s  0.27% 26.56%      6.21s 23.59%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 26.56%      6.18s 23.48%  syscall.Syscall
         0     0% 26.56%      6.09s 23.14%  internal/poll.ignoringEINTRIO (inline)
     0.04s  0.15% 26.71%      6.07s 23.06%  syscall.RawSyscall6
     0.02s 0.076% 26.79%      5.54s 21.05%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 26.79%      5.52s 20.97%  github.com/redis/go-redis/v9.(*baseClient).process
     0.02s 0.076% 26.86%      5.51s 20.93%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0% 26.86%      5.49s 20.86%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 26.86%      5.49s 20.86%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 26.86%      5.08s 19.30%  wtfsaga/instance_engine.(*Engine).jsonGet
     0.02s 0.076% 26.94%      4.96s 18.84%  internal/poll.(*FD).Write
         0     0% 26.94%      4.93s 18.73%  syscall.Write (inline)
         0     0% 26.94%      4.93s 18.73%  syscall.write
     0.02s 0.076% 27.01%      4.68s 17.78%  bufio.(*Writer).Flush
     4.63s 17.59% 44.60%      4.63s 17.59%  runtime.futex
     0.02s 0.076% 44.68%      4.57s 17.36%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
     0.01s 0.038% 44.72%      4.50s 17.10%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0% 44.72%      4.36s 16.57%  net.(*conn).Write
     0.01s 0.038% 44.76%      4.36s 16.57%  net.(*netFD).Write
         0     0% 44.76%      4.10s 15.58%  wtfsaga/instance_engine.(*Engine).readTaskIdentity
     0.02s 0.076% 44.83%      3.67s 13.94%  wtfsaga/instance_engine.(*Engine).transition
         0     0% 44.83%      3.60s 13.68%  runtime.futexwakeup
     0.04s  0.15% 44.98%      3.35s 12.73%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 44.98%      3.35s 12.73%  github.com/redis/go-redis/v9.(*Script).Run
```

### heap

```
File: sagawise
Build ID: 398df6886c2dd9dead2ef85ad93a84731c071d6d
Type: inuse_space
Time: 2026-09-05 08:08:16 PKT
Showing nodes accounting for 12625.61kB, 100% of 12625.61kB total
Showing top 25 nodes out of 47
      flat  flat%   sum%        cum   cum%
 3697.17kB 29.28% 29.28%  3697.17kB 29.28%  bufio.NewReaderSize (inline)
 2563.29kB 20.30% 49.59%  2563.29kB 20.30%  runtime.mallocgc
 2561.56kB 20.29% 69.87%  2561.56kB 20.29%  go.opentelemetry.io/otel/sdk/log.newRing (inline)
 1584.50kB 12.55% 82.42%  1584.50kB 12.55%  bufio.NewWriterSize (inline)
  669.43kB  5.30% 87.73%   669.43kB  5.30%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
  525.43kB  4.16% 91.89%   525.43kB  4.16%  github.com/jackc/pgx/v5/internal/stmtcache.NewLRUCache (inline)
  512.17kB  4.06% 95.94%  5793.84kB 45.89%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
  512.06kB  4.06%   100%   512.06kB  4.06%  github.com/felixge/httpsnoop.Wrap
         0     0%   100%   525.43kB  4.16%  github.com/jackc/pgx/v5.ConnectConfig
         0     0%   100%   525.43kB  4.16%  github.com/jackc/pgx/v5.connect
         0     0%   100%   525.43kB  4.16%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func3
         0     0%   100%   525.43kB  4.16%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).initResourceValue.func1
         0     0%   100%  5793.84kB 45.89%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100%  5793.84kB 45.89%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100%  5793.84kB 45.89%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100%  3697.17kB 29.28%  github.com/redis/go-redis/v9/internal/proto.NewReaderSize (inline)
         0     0%   100%   512.06kB  4.06%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%   512.06kB  4.06%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%  2561.56kB 20.29%  go.opentelemetry.io/otel/sdk/log.NewBatchProcessor
         0     0%   100%  2561.56kB 20.29%  go.opentelemetry.io/otel/sdk/log.newQueue (inline)
         0     0%   100%  2561.56kB 20.29%  main.main
         0     0%   100%  2561.56kB 20.29%  main.run
         0     0%   100%   512.06kB  4.06%  net/http.(*conn).serve
         0     0%   100%   512.06kB  4.06%  net/http.HandlerFunc.ServeHTTP
         0     0%   100%   512.06kB  4.06%  net/http.serverHandler.ServeHTTP
```

### block

```
File: sagawise
Build ID: 398df6886c2dd9dead2ef85ad93a84731c071d6d
Type: delay
Time: 2026-09-05 08:08:16 PKT
Showing nodes accounting for 309.36s, 99.61% of 310.59s total
Dropped 126 nodes (cum <= 1.55s)
Showing top 25 nodes out of 31
      flat  flat%   sum%        cum   cum%
   239.47s 77.10% 77.10%    239.47s 77.10%  runtime.selectgo
    60.50s 19.48% 96.58%     60.50s 19.48%  runtime.chansend1
     6.07s  1.96% 98.54%      6.07s  1.96%  sync.(*WaitGroup).Wait
     3.32s  1.07% 99.61%      3.32s  1.07%  sync.(*Mutex).Lock (inline)
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 99.61%      1.89s  0.61%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 99.61%      1.88s   0.6%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0% 99.61%      4.51s  1.45%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0% 99.61%      4.51s  1.45%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0% 99.61%     73.93s 23.80%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
         0     0% 99.61%      2.33s  0.75%  log.(*Logger).output
         0     0% 99.61%      2.33s  0.75%  log.Printf
         0     0% 99.61%      4.46s  1.44%  main.httpTracing.func1
         0     0% 99.61%     14.41s  4.64%  net/http.(*ServeMux).ServeHTTP
         0     0% 99.61%     15.78s  5.08%  net/http.(*Server).Serve.gowrap3
         0     0% 99.61%     15.78s  5.08%  net/http.(*conn).serve
         0     0% 99.61%     14.46s  4.66%  net/http.HandlerFunc.ServeHTTP
```

### mutex

```
File: sagawise
Build ID: 398df6886c2dd9dead2ef85ad93a84731c071d6d
Type: delay
Time: 2026-09-05 08:08:16 PKT
Showing nodes accounting for 140.64s, 100% of 140.64s total
Dropped 456 nodes (cum <= 0.70s)
Showing top 25 nodes out of 66
      flat  flat%   sum%        cum   cum%
   131.30s 93.36% 93.36%    131.30s 93.36%  runtime.unlock (partial-inline)
     5.95s  4.23% 97.59%      5.95s  4.23%  runtime._LostContendedRuntimeLock
     3.39s  2.41%   100%      3.52s  2.50%  sync.(*Mutex).Unlock
         0     0%   100%      4.21s  2.99%  _.goready.func1
         0     0%   100%      1.49s  1.06%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%      1.56s  1.11%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%      1.56s  1.11%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      1.49s  1.06%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      1.49s  1.06%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      1.49s  1.06%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%      1.49s  1.06%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%      1.51s  1.07%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%      1.56s  1.11%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%      0.85s   0.6%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      5.16s  3.67%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%      5.16s  3.67%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%      2.47s  1.75%  internal/poll.(*FD).SetReadDeadline (inline)
         0     0%   100%      2.57s  1.83%  internal/poll.runtime_pollSetDeadline
         0     0%   100%      2.57s  1.83%  internal/poll.setDeadlineImpl
         0     0%   100%      2.70s  1.92%  log.(*Logger).output
         0     0%   100%      2.70s  1.92%  log.Printf
         0     0%   100%      5.01s  3.56%  main.httpTracing.func1
         0     0%   100%      2.47s  1.75%  net.(*conn).SetReadDeadline
         0     0%   100%      2.47s  1.75%  net.(*netFD).SetReadDeadline (inline)
         0     0%   100%      5.01s  3.56%  net/http.(*ServeMux).ServeHTTP
```

### goroutine

```
File: sagawise
Build ID: 398df6886c2dd9dead2ef85ad93a84731c071d6d
Type: goroutine
Time: 2026-09-05 08:08:17 PKT
Showing nodes accounting for 58, 100% of 58 total
Showing top 25 nodes out of 97
      flat  flat%   sum%        cum   cum%
        56 96.55% 96.55%         56 96.55%  runtime.gopark
         1  1.72% 98.28%          1  1.72%  runtime.goroutineProfileWithLabels
         1  1.72%   100%          1  1.72%  runtime.notetsleepg
         0     0%   100%         30 51.72%  bufio.(*Reader).Peek
         0     0%   100%         30 51.72%  bufio.(*Reader).fill
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5.(*Conn).execPrepared
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn.(*PgConn).ExecStatement
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn.(*PgConn).execExtendedSuffix
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn.(*PgConn).peekMessage
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn.(*PgConn).receiveMessage
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).readUntilRowDescription
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).receiveMessage
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).Read
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgproto3.(*Frontend).Receive
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgproto3.(*chunkReader).Next
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgxpool.(*Conn).Exec
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck
         0     0%   100%          1  1.72%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func5
         0     0%   100%         15 25.86%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%         15 25.86%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%         15 25.86%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%         10 17.24%  github.com/redis/go-redis/v9.(*Script).EvalSha
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 48 | 48 |
| start | json.get | 1.00 | 9 | 9 |
| publish | evalsha | 1.01 | 99 | 101 |
| publish | json.set | 1.00 | 40 | 40 |
| publish | json.get | 2.00 | 14 | 28 |
| publish | json.merge | 1.00 | 15 | 15 |
| publish | zadd | 1.00 | 3 | 3 |
| publish | zrangebyscore | 0.01 | 2 | 0 |
| consume | evalsha | 1.00 | 82 | 82 |
| consume | json.merge | 1.00 | 40 | 40 |
| consume | json.get | 2.00 | 14 | 27 |
| consume | zrem | 1.00 | 1 | 1 |
| consume(final) | evalsha | 1.04 | 96 | 99 |
| consume(final) | json.merge | 2.00 | 26 | 53 |
| consume(final) | json.get | 3.00 | 13 | 38 |
| consume(final) | zrem | 2.00 | 3 | 6 |
| consume(final) | zadd | 2.00 | 2 | 3 |
| consume(final) | hdel | 1.00 | 0 | 0 |
| consume(final) | zrangebyscore | 0.04 | 5 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 2.0 | 57 |
| publish | 6.0 | 187 |
| consume | 5.0 | 150 |
| consume(final) | 11.1 | 199 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.3 | 0.9 / 1.2 | 0.9 / 1.2 | 1.8 | 2.1 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.2 | 2.0 | 2.1 | 0.5 | 1426.5 |
| 100000 | 0.9 / 1.2 | 0.9 / 1.3 | 0.9 / 1.2 | 2.4 | 2.7 | 0.5 | 1179.0 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.3 | 199.9 | 0.0 |
| 10 | 0.9 / 1.3 | 0.9 / 1.3 | 0.9 / 1.3 | 47.5 | 0.0 |
| 50 | 1.0 / 1.3 | 1.0 / 1.4 | 0.9 / 1.3 | 9.8 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 1.0 / 1.3 | 0.9 / 1.3 | 0.9 / 1.2 | 0.0 |
| 10000B | 0.9 / 1.3 | 0.9 / 1.4 | 0.9 / 1.3 | 0.0 |
| 500000B | 1.0 / 1.4 | 2.2 / 3.4 | 1.1 / 1.6 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 360 | 448 | 530 | 531 |
| 500 | 500 | 0 | -859 | -424 | 105 | 114 |
| 2000 | 2000 | 0 | 21 | 572 | 1029 | 1057 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 3.2 | 4.9 | 5.8 | 6.3 |
| separate instances | 400 | 3.0 | 4.6 | 5.3 | 6.7 |

# Profile: profile-after-phase-6

- **Date:** 2026-09-05T07:41:16+05:00
- **Commit:** `84161db` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 1518 sagas/s (~7590 requests/s). The next step, 2277 sagas/s, breached: achieved 1815 < 90% of target 2277 (load generator or server saturated).
- Redis is near saturation at the knee: 89% of one core, ping p99 5.24 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis round-trips per request: start 6.5, publish 11.7, consume 10.6, final consume 13.1. Each round-trip is a sequential network hop, so per-request latency is roughly round-trips × RTT.
- Most expensive Redis work per request: evalsha on consume(final) (1.0 calls × 153 µs).
- JSON.SET costs 64 µs against 12 µs for JSON.GET (5×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$..topic/from/to/failedAt`), so write count, not read count, is what Redis CPU pays for. A final consume does 4 writes.
- Instances already in Redis: consume p50 1.0 → 0.9 ms from 0 to 100000 instances (-7%). List endpoint p50 2.9 → 2.7 ms.
- Tasks per workflow: consume p50 1.0 → 1.3 ms from 2 to 50 tasks (+39%) at a constant ~1000 req/s. Growth here is the recursive-descent JSONPath and whole-document reads scaling with document size.
- Payload size: consume p50 0.9 → 1.5 ms from 100B to 500000B (+65%). Payloads live inside the document every JSONPath query scans; a consume never needs the payload.
- Simultaneous timeouts: max lag 428 ms at 100 → 1001 ms at 2000, i.e. ~0.3 ms per overdue task. The reaper is sequential; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 6.9 ms vs on separate instances 6.7 ms (+3%). Redis serializes writes to one key; the phase 6 Lua-per-transition design will make this the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 32.94%, instance_engine.(*Engine).jsonGet 18.25%, instance_engine.(*Engine).transition 14.88%, instance_engine.(*Engine).readTaskIdentity 14.29%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 1518 sagas/s.** Breach: achieved 1815 < 90% of target 2277 (load generator or server saturated).

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 999 | 0 (0.00%) | 0.9 / 1.3 | 0.9 / 1.4 | 0.9 / 1.3 | 29 | 0.25 / 0.45 | ok |
| 300 | 300 | 1498 | 0 (0.00%) | 1.0 / 1.4 | 1.0 / 1.4 | 1.0 / 1.5 | 37 | 0.28 / 0.49 | ok |
| 450 | 449 | 2247 | 0 (0.00%) | 1.2 / 2.2 | 1.1 / 1.9 | 1.1 / 2.0 | 48 | 0.33 / 0.85 | ok |
| 675 | 674 | 3371 | 0 (0.00%) | 1.3 / 2.2 | 1.3 / 2.2 | 1.3 / 2.3 | 61 | 0.41 / 0.97 | ok |
| 1012 | 1007 | 5034 | 0 (0.00%) | 1.4 / 3.2 | 1.4 / 2.8 | 1.4 / 2.8 | 76 | 0.46 / 1.11 | ok |
| 1518 | 1447 | 7229 | 0 (0.00%) | 2.1 / 9.2 | 2.2 / 6.1 | 2.2 / 6.1 | 83 | 0.75 / 2.82 | ok |
| 2277 | 1815 | 9070 | 0 (0.00%) | 3.5 / 16.1 | 3.6 / 15.0 | 3.6 / 15.1 | 89 | 1.30 / 5.24 | **breach** |

## 2. Server profiles at the knee (1518 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: cf5394f83cb7fe525eebc8ff0a26e05ebaa03a5b
Type: cpu
Time: 2026-09-05 07:42:12 PKT
Duration: 10s, Total samples = 20.16s (201.59%)
Showing nodes accounting for 11.65s, 57.79% of 20.16s total
Dropped 758 nodes (cum <= 0.10s)
Showing top 25 nodes out of 233
      flat  flat%   sum%        cum   cum%
     4.98s 24.70% 24.70%      4.98s 24.70%  internal/runtime/syscall/linux.Syscall6
     3.73s 18.50% 43.20%      3.73s 18.50%  runtime.futex
     0.33s  1.64% 44.84%      0.33s  1.64%  runtime.procyieldAsm
     0.24s  1.19% 46.03%      0.24s  1.19%  runtime.usleep
     0.23s  1.14% 47.17%      0.27s  1.34%  runtime.step
     0.18s  0.89% 48.07%      0.18s  0.89%  runtime.memmove
     0.18s  0.89% 48.96%      0.18s  0.89%  runtime.write1
     0.17s  0.84% 49.80%      0.27s  1.34%  runtime.scanObject
     0.16s  0.79% 50.60%      0.49s  2.43%  runtime.pcvalue
     0.15s  0.74% 51.34%      0.16s  0.79%  runtime.findfunc
     0.14s  0.69% 52.03%      0.14s  0.69%  runtime.nextFreeFast (inline)
     0.14s  0.69% 52.73%      0.14s  0.69%  time.runtimeNow
     0.13s  0.64% 53.37%      1.15s  5.70%  runtime.mallocgc
     0.12s   0.6% 53.97%      0.12s   0.6%  runtime.osyield
     0.11s  0.55% 54.51%      0.93s  4.61%  runtime.unlock2
     0.10s   0.5% 55.01%      0.56s  2.78%  internal/poll.runtime_pollSetDeadline
     0.08s   0.4% 55.41%      0.82s  4.07%  runtime.lock2
     0.07s  0.35% 55.75%      6.74s 33.43%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.07s  0.35% 56.10%      0.47s  2.33%  runtime.netpoll
     0.06s   0.3% 56.40%      5.13s 25.45%  runtime.findRunnable
     0.06s   0.3% 56.70%      0.56s  2.78%  runtime.mallocgcSmallScanNoHeader
     0.06s   0.3% 56.99%      0.12s   0.6%  runtime.mallocgcSmallScanNoHeaderSC2
     0.06s   0.3% 57.29%      6.64s 32.94%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.05s  0.25% 57.54%      0.52s  2.58%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).getConn
     0.05s  0.25% 57.79%      0.15s  0.74%  go.opentelemetry.io/otel/sdk/trace.(*recordingSpan).SetAttributes
```

### cpu-cumulative

```
File: sagawise
Build ID: cf5394f83cb7fe525eebc8ff0a26e05ebaa03a5b
Type: cpu
Time: 2026-09-05 07:42:12 PKT
Duration: 10s, Total samples = 20.16s (201.59%)
Showing nodes accounting for 9.36s, 46.43% of 20.16s total
Dropped 758 nodes (cum <= 0.10s)
Showing top 40 nodes out of 233
      flat  flat%   sum%        cum   cum%
     0.03s  0.15%  0.15%     13.62s 67.56%  net/http.(*conn).serve
     0.01s  0.05%   0.2%     10.35s 51.34%  net/http.serverHandler.ServeHTTP
     0.01s  0.05%  0.25%     10.34s 51.29%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
     0.01s  0.05%   0.3%     10.34s 51.29%  net/http.HandlerFunc.ServeHTTP
     0.03s  0.15%  0.45%     10.33s 51.24%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.45%      9.31s 46.18%  net/http.(*ServeMux).ServeHTTP
         0     0%  0.45%      9.25s 45.88%  main.httpTracing.func1
     0.02s 0.099%  0.55%      6.77s 33.58%  github.com/redis/go-redis/v9.(*Client).Process
     0.01s  0.05%   0.6%      6.75s 33.48%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.07s  0.35%  0.94%      6.74s 33.43%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.06s   0.3%  1.24%      6.64s 32.94%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.02s 0.099%  1.34%      5.49s 27.23%  runtime.mcall
         0     0%  1.34%      5.45s 27.03%  runtime.park_m
     0.02s 0.099%  1.44%      5.45s 27.03%  runtime.schedule
     0.06s   0.3%  1.74%      5.13s 25.45%  runtime.findRunnable
     0.04s   0.2%  1.93%      5.04s 25.00%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     4.98s 24.70% 26.64%      4.98s 24.70%  internal/runtime/syscall/linux.Syscall6
     0.02s 0.099% 26.74%      4.84s 24.01%  syscall.Syscall
         0     0% 26.74%      4.80s 23.81%  internal/poll.ignoringEINTRIO (inline)
     0.05s  0.25% 26.98%      4.64s 23.02%  syscall.RawSyscall6
         0     0% 26.98%      4.53s 22.47%  github.com/redis/go-redis/v9.(*baseClient).process
     0.01s  0.05% 27.03%      4.53s 22.47%  github.com/redis/go-redis/v9.(*baseClient).withConn
     0.01s  0.05% 27.08%      4.52s 22.42%  github.com/redis/go-redis/v9.(*baseClient).processCommand
     0.01s  0.05% 27.13%      4.49s 22.27%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
     0.01s  0.05% 27.18%      4.48s 22.22%  github.com/redis/go-redis/v9.(*baseClient)._process
     0.01s  0.05% 27.23%      3.90s 19.35%  internal/poll.(*FD).Write
     0.01s  0.05% 27.28%      3.86s 19.15%  syscall.Write (inline)
         0     0% 27.28%      3.85s 19.10%  syscall.write
     3.73s 18.50% 45.78%      3.73s 18.50%  runtime.futex
     0.01s  0.05% 45.83%      3.68s 18.25%  wtfsaga/instance_engine.(*Engine).jsonGet
     0.04s   0.2% 46.03%      3.65s 18.11%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
     0.01s  0.05% 46.08%      3.55s 17.61%  bufio.(*Writer).Flush
         0     0% 46.08%      3.42s 16.96%  net.(*conn).Write
         0     0% 46.08%      3.42s 16.96%  net.(*netFD).Write
     0.03s  0.15% 46.23%      3.26s 16.17%  github.com/redis/go-redis/v9.cmdable.JSONGet
     0.01s  0.05% 46.28%         3s 14.88%  wtfsaga/instance_engine.(*Engine).transition
     0.02s 0.099% 46.38%      2.92s 14.48%  runtime.futexwakeup
         0     0% 46.38%      2.88s 14.29%  wtfsaga/instance_engine.(*Engine).readTaskIdentity
     0.01s  0.05% 46.43%      2.83s 14.04%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 46.43%      2.83s 14.04%  github.com/redis/go-redis/v9.(*Script).Run
```

### heap

```
File: sagawise
Build ID: cf5394f83cb7fe525eebc8ff0a26e05ebaa03a5b
Type: inuse_space
Time: 2026-09-05 07:42:23 PKT
Showing nodes accounting for 14078.22kB, 100% of 14078.22kB total
Showing top 25 nodes out of 55
      flat  flat%   sum%        cum   cum%
 6323.83kB 44.92% 44.92%  6323.83kB 44.92%  bufio.NewWriterSize (inline)
 3073.88kB 21.83% 66.75%  3073.88kB 21.83%  go.opentelemetry.io/otel/sdk/log.newRing (inline)
 2563.28kB 18.21% 84.96%  2563.28kB 18.21%  runtime.mallocgc
 1056.33kB  7.50% 92.46%  1056.33kB  7.50%  bufio.NewReaderSize (inline)
  548.84kB  3.90% 96.36%   548.84kB  3.90%  reflect.compiledTypelinks
  512.05kB  3.64%   100%   512.05kB  3.64%  sync.runtime_notifyListWait
         0     0%   100%   548.84kB  3.90%  encoding/json.Unmarshal (inline)
         0     0%   100%   548.84kB  3.90%  encoding/json/v2.Unmarshal
         0     0%   100%   548.84kB  3.90%  encoding/json/v2.makeStructArshaler.func1
         0     0%   100%   548.84kB  3.90%  encoding/json/v2.makeStructArshaler.func3
         0     0%   100%   548.84kB  3.90%  encoding/json/v2.makeStructFields
         0     0%   100%   548.84kB  3.90%  encoding/json/v2.makeStructFields.func3
         0     0%   100%   548.84kB  3.90%  encoding/json/v2.unmarshalDecode
         0     0%   100%  6866.17kB 48.77%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100%  6866.17kB 48.77%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100%  6866.17kB 48.77%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100%  6866.17kB 48.77%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
         0     0%   100%  1056.33kB  7.50%  github.com/redis/go-redis/v9/internal/proto.NewReaderSize (inline)
         0     0%   100%  3073.88kB 21.83%  go.opentelemetry.io/otel/sdk/log.NewBatchProcessor
         0     0%   100%  3073.88kB 21.83%  go.opentelemetry.io/otel/sdk/log.newQueue (inline)
         0     0%   100%  3622.72kB 25.73%  main.main
         0     0%   100%  3622.72kB 25.73%  main.run
         0     0%   100%  1026.06kB  7.29%  net/http.(*conn).serve
         0     0%   100%   512.05kB  3.64%  net/http.(*connReader).abortPendingRead
         0     0%   100%   512.05kB  3.64%  net/http.(*response).finishRequest
```

### block

```
File: sagawise
Build ID: cf5394f83cb7fe525eebc8ff0a26e05ebaa03a5b
Type: delay
Time: 2026-09-05 07:42:23 PKT
Showing nodes accounting for 248.41s, 99.73% of 249.07s total
Dropped 128 nodes (cum <= 1.25s)
Showing top 25 nodes out of 35
      flat  flat%   sum%        cum   cum%
   193.53s 77.70% 77.70%    193.53s 77.70%  runtime.selectgo
    48.11s 19.31% 97.01%     48.11s 19.31%  runtime.chansend1
     5.08s  2.04% 99.05%      5.08s  2.04%  sync.(*WaitGroup).Wait
     1.69s  0.68% 99.73%      1.69s  0.68%  sync.(*Mutex).Lock (inline)
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0% 99.73%      1.31s  0.53%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 99.73%      1.31s  0.53%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 99.73%      1.43s  0.57%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 99.73%      1.39s  0.56%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0% 99.73%      1.31s  0.53%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).Get
         0     0% 99.73%      1.31s  0.53%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).getConn
         0     0% 99.73%      2.84s  1.14%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0% 99.73%      2.84s  1.14%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0% 99.73%     59.25s 23.79%  go.opentelemetry.io/otel/sdk/log.(*BatchProcessor).process.func1
         0     0% 99.73%      1.27s  0.51%  log.(*Logger).output
         0     0% 99.73%      1.27s  0.51%  log.Printf
         0     0% 99.73%      2.82s  1.13%  main.httpTracing.func1
```

### mutex

```
File: sagawise
Build ID: cf5394f83cb7fe525eebc8ff0a26e05ebaa03a5b
Type: delay
Time: 2026-09-05 07:42:23 PKT
Showing nodes accounting for 80.53s, 100% of 80.53s total
Dropped 377 nodes (cum <= 0.40s)
Showing top 25 nodes out of 67
      flat  flat%   sum%        cum   cum%
    74.60s 92.63% 92.63%     74.60s 92.63%  runtime.unlock (partial-inline)
     4.11s  5.11% 97.74%      4.11s  5.11%  runtime._LostContendedRuntimeLock
     1.82s  2.26%   100%      1.86s  2.31%  sync.(*Mutex).Unlock (partial-inline)
         0     0%   100%      2.01s  2.50%  _.goready.func1
         0     0%   100%      0.79s  0.98%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%      0.81s  1.00%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%      0.81s  1.00%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      0.78s  0.97%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      0.78s  0.97%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      0.78s  0.97%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%      0.78s  0.97%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%      0.81s  1.00%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%      0.81s  1.00%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%      2.74s  3.40%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%      2.74s  3.40%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%   100%      1.27s  1.57%  internal/poll.(*FD).SetReadDeadline (inline)
         0     0%   100%      0.44s  0.54%  internal/poll.ignoringEINTRIO (inline)
         0     0%   100%      1.35s  1.68%  internal/poll.runtime_pollSetDeadline
         0     0%   100%      1.35s  1.68%  internal/poll.setDeadlineImpl
         0     0%   100%      1.41s  1.75%  log.(*Logger).output
         0     0%   100%      1.41s  1.75%  log.Printf
         0     0%   100%      2.68s  3.33%  main.httpTracing.func1
         0     0%   100%      1.27s  1.57%  net.(*conn).SetReadDeadline
         0     0%   100%      1.27s  1.57%  net.(*netFD).SetReadDeadline (inline)
         0     0%   100%      2.68s  3.33%  net/http.(*ServeMux).ServeHTTP
```

### goroutine

```
File: sagawise
Build ID: cf5394f83cb7fe525eebc8ff0a26e05ebaa03a5b
Type: goroutine
Time: 2026-09-05 07:42:23 PKT
Showing nodes accounting for 40, 86.96% of 46 total
Showing top 25 nodes out of 132
      flat  flat%   sum%        cum   cum%
        32 69.57% 69.57%         32 69.57%  runtime.gopark
         1  2.17% 71.74%          1  2.17%  net/http.pathUnescape
         1  2.17% 73.91%          1  2.17%  os.(*File).Write
         1  2.17% 76.09%          1  2.17%  runtime.goexit1
         1  2.17% 78.26%          1  2.17%  runtime.goroutineProfileWithLabels
         1  2.17% 80.43%          1  2.17%  runtime.newobject
         1  2.17% 82.61%          1  2.17%  runtime.notetsleepg
         1  2.17% 84.78%          1  2.17%  syscall.Syscall
         1  2.17% 86.96%          1  2.17%  type:.hash.SSSM8E
         0     0% 86.96%         17 36.96%  bufio.(*Reader).Peek
         0     0% 86.96%          1  2.17%  bufio.(*Reader).ReadLine
         0     0% 86.96%          1  2.17%  bufio.(*Reader).ReadSlice
         0     0% 86.96%         18 39.13%  bufio.(*Reader).fill
         0     0% 86.96%          1  2.17%  github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck
         0     0% 86.96%          1  2.17%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func5
         0     0% 86.96%          5 10.87%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 86.96%          1  2.17%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessPipelineHook.func1
         0     0% 86.96%          6 13.04%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 86.96%          1  2.17%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessPipelineHook.func1
         0     0% 86.96%          1  2.17%  github.com/redis/go-redis/extra/redisotel/v9.funcFileLine
         0     0% 86.96%          6 13.04%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 86.96%          1  2.17%  github.com/redis/go-redis/v9.(*Pipeline).Exec
         0     0% 86.96%          2  4.35%  github.com/redis/go-redis/v9.(*Script).EvalSha
         0     0% 86.96%          2  4.35%  github.com/redis/go-redis/v9.(*Script).Run
         0     0% 86.96%          5 10.87%  github.com/redis/go-redis/v9.(*baseClient)._process
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 64 | 64 |
| start | json.get | 2.50 | 6 | 15 |
| start | zrem | 1.50 | 1 | 1 |
| start | hdel | 1.50 | 0 | 0 |
| publish | evalsha | 1.00 | 147 | 147 |
| publish | json.set | 3.00 | 33 | 98 |
| publish | json.get | 3.55 | 10 | 35 |
| publish | zadd | 1.00 | 3 | 3 |
| publish | zrem | 1.55 | 1 | 1 |
| publish | hdel | 1.55 | 0 | 0 |
| consume | evalsha | 1.00 | 111 | 111 |
| consume | json.set | 2.00 | 36 | 72 |
| consume | json.get | 3.52 | 9 | 33 |
| consume | zrem | 2.52 | 1 | 3 |
| consume | hdel | 1.52 | 0 | 0 |
| consume(final) | evalsha | 1.03 | 153 | 158 |
| consume(final) | json.set | 4.00 | 28 | 111 |
| consume(final) | json.get | 3.00 | 12 | 37 |
| consume(final) | zrem | 2.00 | 3 | 6 |
| consume(final) | zadd | 2.00 | 2 | 3 |
| consume(final) | hdel | 1.00 | 0 | 0 |
| consume(final) | zrangebyscore | 0.04 | 5 | 0 |
| consume(final) | zrange | 0.01 | 5 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 6.5 | 80 |
| publish | 11.7 | 285 |
| consume | 10.6 | 219 |
| consume(final) | 13.1 | 316 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.4 | 1.0 / 1.4 | 1.0 / 1.5 | 2.9 | 3.2 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.2 | 1.0 / 1.4 | 1.0 / 1.4 | 0.9 | 1.0 | 0.6 | -3012.4 |
| 100000 | 0.8 / 1.1 | 0.9 / 1.2 | 0.9 / 1.3 | 2.7 | 3.2 | 0.6 | 1027.8 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 0.9 / 1.3 | 1.0 / 1.3 | 1.0 / 1.3 | 199.9 | 0.0 |
| 10 | 1.1 / 1.6 | 1.1 / 1.5 | 1.0 / 1.5 | 47.5 | 0.0 |
| 50 | 1.5 / 2.0 | 1.4 / 2.3 | 1.3 / 2.1 | 9.8 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 0.9 / 1.3 | 0.9 / 1.4 | 0.9 / 1.3 | 0.0 |
| 10000B | 1.0 / 1.4 | 1.0 / 1.4 | 1.0 / 1.3 | 0.0 |
| 500000B | 1.0 / 1.4 | 2.4 / 4.0 | 1.5 / 2.1 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 288 | 357 | 427 | 428 |
| 500 | 500 | 0 | 245 | 569 | 887 | 893 |
| 2000 | 2000 | 0 | 184 | 589 | 992 | 1001 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 6.9 | 10.2 | 12.1 | 12.2 |
| separate instances | 400 | 6.7 | 9.5 | 10.0 | 10.5 |

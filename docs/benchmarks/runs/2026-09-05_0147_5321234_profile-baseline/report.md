# Profile: profile-baseline

- **Date:** 2026-09-05T01:47:34+05:00
- **Commit:** `5321234` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 1518 sagas/s (~7590 requests/s). The next step, 2277 sagas/s, breached: p99 536 ms > 50 ms.
- Redis is near saturation at the knee: 92% of one core, ping p99 4.32 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis round-trips per request: start 2.0, publish 8.0, consume 7.0, final consume 11.0. Each round-trip is a sequential network hop, so per-request latency is roughly round-trips × RTT.
- Most expensive Redis work per request: json.set on consume(final) (4.0 calls × 64 µs).
- JSON.SET costs 66 µs against 12 µs for JSON.GET (5×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$..topic/from/to/failedAt`), so write count, not read count, is what Redis CPU pays for. A final consume does 4 writes.
- Instances already in Redis: consume p50 2.9 → 2.8 ms from 0 to 100000 instances (-2%). List endpoint p50 2.1 → 3.0 ms.
- Tasks per workflow: consume p50 3.1 → 5.2 ms from 2 to 50 tasks (+69%) at a constant ~1000 req/s. Growth here is the recursive-descent JSONPath and whole-document reads scaling with document size.
- Payload size: consume p50 2.9 → 3.6 ms from 100B to 500000B (+26%). Payloads live inside the document every JSONPath query scans; a consume never needs the payload.
- Simultaneous timeouts: max lag 979 ms at 100 → 1434 ms at 2000, i.e. ~0.2 ms per overdue task. The reaper is sequential; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 13.8 ms vs on separate instances 12.7 ms (+9%). Redis serializes writes to one key; the phase 6 Lua-per-transition design will make this the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 50.10%, instance_engine.(*Engine).handlePublish 21.96%, instance_engine.(*Engine).handleConsumeOrFail 20.95%, instance_engine.jsonMatches[go.shape.string] 17.42%, instance_engine.jsonFirstMatch[go.shape.string] 14.49%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 1518 sagas/s.** Breach: p99 536 ms > 50 ms.

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 998 | 0 (0.00%) | 1.0 / 1.5 | 2.8 / 3.6 | 3.1 / 4.3 | 43 | 0.30 / 0.56 | ok |
| 300 | 299 | 1496 | 0 (0.00%) | 1.1 / 1.7 | 3.4 / 4.3 | 3.7 / 5.1 | 53 | 0.35 / 0.63 | ok |
| 450 | 449 | 2244 | 0 (0.00%) | 1.2 / 2.3 | 3.8 / 5.7 | 4.1 / 6.4 | 61 | 0.38 / 0.90 | ok |
| 675 | 673 | 3363 | 0 (0.00%) | 1.4 / 2.7 | 4.7 / 7.0 | 5.0 / 7.9 | 67 | 0.47 / 1.19 | ok |
| 1012 | 1004 | 5018 | 0 (0.00%) | 1.8 / 4.2 | 6.1 / 9.8 | 6.4 / 10.7 | 75 | 0.59 / 1.63 | ok |
| 1518 | 1434 | 7167 | 0 (0.00%) | 2.5 / 4.9 | 8.9 / 12.9 | 9.4 / 14.9 | 88 | 0.92 / 2.26 | ok |
| 2277 | 1525 | 7622 | 0 (0.00%) | 48.5 / 116.4 | 208.8 / 436.5 | 213.9 / 535.6 | 92 | 1.84 / 4.32 | **breach** |

## 2. Server profiles at the knee (1518 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: 2dd9f4f88e1a954c63b5152725114d2ec00bef25
Type: cpu
Time: 2026-09-05 01:48:22 PKT
Duration: 10s, Total samples = 28.64s (286.40%)
Showing nodes accounting for 15.27s, 53.32% of 28.64s total
Dropped 820 nodes (cum <= 0.14s)
Showing top 25 nodes out of 271
      flat  flat%   sum%        cum   cum%
     6.62s 23.11% 23.11%      6.62s 23.11%  internal/runtime/syscall/linux.Syscall6
     3.83s 13.37% 36.49%      3.83s 13.37%  runtime.futex
     0.54s  1.89% 38.37%      0.64s  2.23%  runtime.step
     0.47s  1.64% 40.01%      0.47s  1.64%  runtime.procyieldAsm
     0.36s  1.26% 41.27%      1.17s  4.09%  runtime.pcvalue
     0.30s  1.05% 42.32%      0.30s  1.05%  time.runtimeNow
     0.27s  0.94% 43.26%      0.27s  0.94%  runtime.memmove
     0.27s  0.94% 44.20%      0.27s  0.94%  runtime.usleep
     0.22s  0.77% 44.97%      2.01s  7.02%  runtime.mallocgc
     0.22s  0.77% 45.74%      0.22s  0.77%  runtime.nextFreeFast (inline)
     0.19s  0.66% 46.40%      0.30s  1.05%  runtime.scanObject
     0.19s  0.66% 47.07%      0.19s  0.66%  sync/atomic.(*Int32).Add (inline)
     0.18s  0.63% 47.70%      0.18s  0.63%  runtime.nanotime (inline)
     0.17s  0.59% 48.29%      0.18s  0.63%  runtime.findfunc
     0.16s  0.56% 48.85%      0.16s  0.56%  internal/runtime/gc/scan.scanSpanPackedAVX512
     0.16s  0.56% 49.41%      0.16s  0.56%  runtime.osyield
     0.15s  0.52% 49.93%      0.55s  1.92%  runtime.(*unwinder).resolveInternal
     0.14s  0.49% 50.42%     13.41s 46.82%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.14s  0.49% 50.91%      0.33s  1.15%  go.opentelemetry.io/otel/trace.NewSpanStartConfig
     0.14s  0.49% 51.40%      0.90s  3.14%  runtime.lock2
     0.12s  0.42% 51.82%      5.72s 19.97%  runtime.findRunnable
     0.11s  0.38% 52.20%      0.16s  0.56%  runtime.chanrecv
     0.11s  0.38% 52.58%      0.24s  0.84%  runtime.mallocgcSmallScanNoHeaderSC2
     0.11s  0.38% 52.97%      0.39s  1.36%  runtime.runqgrab
     0.10s  0.35% 53.32%      1.41s  4.92%  bufio.(*Reader).fill
```

### cpu-cumulative

```
File: sagawise
Build ID: 2dd9f4f88e1a954c63b5152725114d2ec00bef25
Type: cpu
Time: 2026-09-05 01:48:22 PKT
Duration: 10s, Total samples = 28.64s (286.40%)
Showing nodes accounting for 7.54s, 26.33% of 28.64s total
Dropped 820 nodes (cum <= 0.14s)
Showing top 40 nodes out of 271
      flat  flat%   sum%        cum   cum%
         0     0%     0%     19.79s 69.10%  net/http.(*conn).serve
     0.01s 0.035% 0.035%     17.36s 60.61%  net/http.serverHandler.ServeHTTP
     0.06s  0.21%  0.24%     17.35s 60.58%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.24%     17.35s 60.58%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
     0.02s  0.07%  0.31%     17.35s 60.58%  net/http.HandlerFunc.ServeHTTP
         0     0%  0.31%     16.28s 56.84%  net/http.(*ServeMux).ServeHTTP
         0     0%  0.31%     16.20s 56.56%  main.httpTracing.func1
         0     0%  0.31%     14.35s 50.10%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.03s   0.1%  0.42%     13.48s 47.07%  github.com/redis/go-redis/v9.(*Client).Process
     0.04s  0.14%  0.56%     13.45s 46.96%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.14s  0.49%  1.05%     13.41s 46.82%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.05s  0.17%  1.22%      9.29s 32.44%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     0.01s 0.035%  1.26%      8.17s 28.53%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%  1.26%      8.14s 28.42%  github.com/redis/go-redis/v9.(*baseClient).processCommand
     0.02s  0.07%  1.33%      8.13s 28.39%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%  1.33%      8.11s 28.32%  github.com/redis/go-redis/v9.(*baseClient)._process
     0.02s  0.07%  1.40%      8.11s 28.32%  github.com/redis/go-redis/v9.(*baseClient).withConn
     0.02s  0.07%  1.47%      7.31s 25.52%  github.com/redis/go-redis/v9.cmdable.JSONGet
     6.62s 23.11% 24.58%      6.62s 23.11%  internal/runtime/syscall/linux.Syscall6
     0.02s  0.07% 24.65%      6.29s 21.96%  wtfsaga/instance_engine.(*Engine).handlePublish
     0.01s 0.035% 24.69%      6.27s 21.89%  syscall.RawSyscall6
     0.10s  0.35% 25.03%      6.23s 21.75%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
         0     0% 25.03%      6.19s 21.61%  internal/poll.ignoringEINTRIO (inline)
         0     0% 25.03%      6.19s 21.61%  runtime.mcall
     0.03s   0.1% 25.14%      6.19s 21.61%  syscall.Syscall
     0.09s  0.31% 25.45%      6.10s 21.30%  runtime.schedule
         0     0% 25.45%         6s 20.95%  wtfsaga/instance_engine.(*Engine).handleConsumeOrFail
         0     0% 25.45%      5.98s 20.88%  runtime.park_m
     0.12s  0.42% 25.87%      5.72s 19.97%  runtime.findRunnable
         0     0% 25.87%      5.22s 18.23%  github.com/redis/go-redis/v9.cmdable.JSONSet (inline)
         0     0% 25.87%      5.22s 18.23%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0% 25.87%      5.22s 18.23%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
     0.02s  0.07% 25.94%      5.16s 18.02%  internal/poll.(*FD).Write
         0     0% 25.94%      5.13s 17.91%  syscall.Write (inline)
     0.02s  0.07% 26.01%      5.13s 17.91%  syscall.write
     0.01s 0.035% 26.05%      4.99s 17.42%  wtfsaga/instance_engine.jsonMatches[go.shape.string]
     0.04s  0.14% 26.19%      4.78s 16.69%  bufio.(*Writer).Flush
     0.02s  0.07% 26.26%      4.71s 16.45%  net.(*conn).Write
         0     0% 26.26%      4.69s 16.38%  net.(*netFD).Write
     0.02s  0.07% 26.33%      4.15s 14.49%  wtfsaga/instance_engine.jsonFirstMatch[go.shape.string]
```

### heap

```
File: sagawise
Build ID: 2dd9f4f88e1a954c63b5152725114d2ec00bef25
Type: inuse_space
Time: 2026-09-05 01:48:31 PKT
Showing nodes accounting for 15082.35kB, 100% of 15082.35kB total
Showing top 25 nodes out of 74
      flat  flat%   sum%        cum   cum%
 4724.17kB 31.32% 31.32%  4724.17kB 31.32%  bufio.NewWriterSize (inline)
 3587.94kB 23.79% 55.11%  3587.94kB 23.79%  runtime.mallocgc
    3169kB 21.01% 76.12%     3169kB 21.01%  bufio.NewReaderSize (inline)
 1536.94kB 10.19% 86.31%  1536.94kB 10.19%  go.opentelemetry.io/otel/sdk/log.newRing
  525.43kB  3.48% 89.80%   525.43kB  3.48%  github.com/jackc/pgx/v5/internal/stmtcache.NewLRUCache (inline)
  514.38kB  3.41% 93.21%   514.38kB  3.41%  encoding/json/v2.makeStringArshaler.func2
  512.44kB  3.40% 96.60%   512.44kB  3.40%  go.opentelemetry.io/otel/sdk/metric.(*cache[go.shape.struct { Name string; Description string; Kind go.opentelemetry.io/otel/sdk/metric.InstrumentKind; Unit string; Number string },go.shape.struct { go.opentelemetry.io/otel/sdk/metric.val go.shape.*uint8; go.opentelemetry.io/otel/sdk/metric.err error }]).Lookup
  512.06kB  3.40%   100%   512.06kB  3.40%  net.newFD
         0     0%   100%   514.38kB  3.41%  encoding/json.Unmarshal (inline)
         0     0%   100%   514.38kB  3.41%  encoding/json/v2.Unmarshal
         0     0%   100%   514.38kB  3.41%  encoding/json/v2.makeSliceArshaler.func3
         0     0%   100%   514.38kB  3.41%  encoding/json/v2.unmarshalDecode
         0     0%   100%   525.43kB  3.48%  github.com/jackc/pgx/v5.ConnectConfig
         0     0%   100%   525.43kB  3.48%  github.com/jackc/pgx/v5.connect
         0     0%   100%   525.43kB  3.48%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func3
         0     0%   100%   525.43kB  3.48%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).initResourceValue.func1
         0     0%   100%  6866.17kB 45.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100%  6866.17kB 45.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100%  6866.17kB 45.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100%  6866.17kB 45.52%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
         0     0%   100%     3169kB 21.01%  github.com/redis/go-redis/v9/internal/proto.NewReaderSize (inline)
         0     0%   100%   512.44kB  3.40%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).configure
         0     0%   100%   514.38kB  3.41%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%   100%   512.44kB  3.40%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewHandler
         0     0%   100%   512.44kB  3.40%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware
```

### block

```
File: sagawise
Build ID: 2dd9f4f88e1a954c63b5152725114d2ec00bef25
Type: delay
Time: 2026-09-05 01:48:32 PKT
Showing nodes accounting for 2.76hrs, 99.91% of 2.76hrs total
Dropped 121 nodes (cum <= 0.01hrs)
Showing top 25 nodes out of 62
      flat  flat%   sum%        cum   cum%
   2.74hrs 99.31% 99.31%    2.74hrs 99.31%  runtime.selectgo
   0.02hrs   0.6% 99.91%    0.02hrs   0.6%  sync.(*Cond).Wait
         0     0% 99.91%    0.09hrs  3.41%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Acquire
         0     0% 99.91%    0.09hrs  3.41%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0% 99.91%    0.09hrs  3.41%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).Acquire
         0     0% 99.91%    0.09hrs  3.41%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).acquire
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0% 99.91%    2.61hrs 94.66%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 99.91%    2.61hrs 94.66%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 99.91%    2.61hrs 94.71%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0% 99.91%    1.38hrs 49.96%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0% 99.91%    0.94hrs 34.11%  github.com/redis/go-redis/v9.cmdable.JSONSet
         0     0% 99.91%    0.94hrs 34.11%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0% 99.91%    0.94hrs 34.11%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0% 99.91%    0.15hrs  5.33%  github.com/redis/go-redis/v9.cmdable.ZAdd
         0     0% 99.91%    0.15hrs  5.33%  github.com/redis/go-redis/v9.cmdable.ZAddArgs
```

### mutex

```
File: sagawise
Build ID: 2dd9f4f88e1a954c63b5152725114d2ec00bef25
Type: delay
Time: 2026-09-05 01:48:32 PKT
Showing nodes accounting for 244.11s, 100% of 244.11s total
Dropped 372 nodes (cum <= 1.22s)
Showing top 25 nodes out of 115
      flat  flat%   sum%        cum   cum%
   224.04s 91.78% 91.78%    224.04s 91.78%  runtime.unlock (inline)
    11.01s  4.51% 96.29%     11.01s  4.51%  runtime._LostContendedRuntimeLock
     9.06s  3.71%   100%      9.33s  3.82%  sync.(*Mutex).Unlock (inline)
         0     0%   100%      6.42s  2.63%  _.goready.func1
         0     0%   100%     20.45s  8.38%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%     20.61s  8.44%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%     20.61s  8.44%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      5.58s  2.29%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0%   100%      8.29s  3.40%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0%   100%     20.25s  8.29%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      8.29s  3.40%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0%   100%     20.25s  8.29%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      5.58s  2.29%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0%   100%     20.25s  8.29%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%     20.25s  8.29%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%     11.14s  4.56%  github.com/redis/go-redis/v9.(*baseClient).releaseConn
         0     0%   100%     11.14s  4.56%  github.com/redis/go-redis/v9.(*baseClient).releaseConnToPool
         0     0%   100%     20.25s  8.29%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%     11.14s  4.56%  github.com/redis/go-redis/v9.(*baseClient).withConn.func1
         0     0%   100%     20.61s  8.44%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%     11.71s  4.80%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      7.14s  2.93%  github.com/redis/go-redis/v9.cmdable.JSONSet
         0     0%   100%      7.14s  2.93%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0%   100%      7.14s  2.93%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0%   100%      5.37s  2.20%  github.com/redis/go-redis/v9/internal.(*FastSemaphore).Acquire
```

### goroutine

```
File: sagawise
Build ID: 2dd9f4f88e1a954c63b5152725114d2ec00bef25
Type: goroutine
Time: 2026-09-05 01:48:32 PKT
Showing nodes accounting for 157, 99.37% of 158 total
Showing top 25 nodes out of 143
      flat  flat%   sum%        cum   cum%
       148 93.67% 93.67%        148 93.67%  runtime.gopark
         4  2.53% 96.20%          4  2.53%  syscall.Syscall
         1  0.63% 96.84%          1  0.63%  encoding/json/jsontext.(*encoderState).WriteToken
         1  0.63% 97.47%          1  0.63%  github.com/redis/go-redis/v9/internal/pool.getCachedTimeNs
         1  0.63% 98.10%          1  0.63%  go.opentelemetry.io/otel/sdk/metric.resolveAttributes
         1  0.63% 98.73%          1  0.63%  runtime.goroutineProfileWithLabels
         1  0.63% 99.37%          1  0.63%  runtime.notetsleepg
         0     0% 99.37%         73 46.20%  bufio.(*Reader).Peek
         0     0% 99.37%          1  0.63%  bufio.(*Reader).ReadByte
         0     0% 99.37%         74 46.84%  bufio.(*Reader).fill
         0     0% 99.37%          3  1.90%  bufio.(*Writer).Flush
         0     0% 99.37%          1  0.63%  encoding/json.Marshal
         0     0% 99.37%          1  0.63%  encoding/json/jsontext.(*Encoder).WriteToken (inline)
         0     0% 99.37%          1  0.63%  encoding/json/v2.Marshal
         0     0% 99.37%          1  0.63%  encoding/json/v2.makeInterfaceArshaler.func1
         0     0% 99.37%          1  0.63%  encoding/json/v2.makeMapArshaler.func2
         0     0% 99.37%          1  0.63%  encoding/json/v2.marshalEncode
         0     0% 99.37%          1  0.63%  encoding/json/v2.marshalObjectAny
         0     0% 99.37%          1  0.63%  encoding/json/v2.marshalValueAny
         0     0% 99.37%          8  5.06%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0% 99.37%          8  5.06%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0% 99.37%          8  5.06%  github.com/jackc/pgx/v5.(*Conn).execPrepared
         0     0% 99.37%          8  5.06%  github.com/jackc/pgx/v5/pgconn.(*PgConn).ExecStatement
         0     0% 99.37%          8  5.06%  github.com/jackc/pgx/v5/pgconn.(*PgConn).execExtendedSuffix
         0     0% 99.37%          1  0.63%  github.com/jackc/pgx/v5/pgconn.(*PgConn).flushWithPotentialWriteReadDeadlock
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 58 | 58 |
| start | json.get | 1.00 | 9 | 9 |
| publish | json.set | 3.00 | 66 | 199 |
| publish | json.get | 4.00 | 11 | 46 |
| publish | zadd | 1.00 | 6 | 6 |
| publish | zrange | 0.01 | 4 | 0 |
| consume | json.set | 2.00 | 63 | 126 |
| consume | json.get | 4.00 | 12 | 50 |
| consume | zrem | 1.00 | 4 | 4 |
| consume(final) | json.set | 4.00 | 64 | 257 |
| consume(final) | json.get | 6.00 | 12 | 73 |
| consume(final) | zrem | 1.00 | 4 | 4 |
| consume(final) | zrange | 0.01 | 5 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 2.0 | 68 |
| publish | 8.0 | 251 |
| consume | 7.0 | 180 |
| consume(final) | 11.0 | 334 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.3 | 2.6 / 3.1 | 2.9 / 3.7 | 2.1 | 2.4 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.3 | 2.6 / 3.0 | 2.9 / 3.7 | 2.3 | 3.0 | 0.5 | 1706.4 |
| 100000 | 0.9 / 1.3 | 2.5 / 3.0 | 2.8 / 3.6 | 3.0 | 3.4 | 0.5 | 1176.0 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 1.0 / 1.4 | 2.8 / 3.5 | 3.1 / 4.3 | 199.7 | 0.0 |
| 10 | 1.1 / 1.4 | 3.1 / 4.0 | 2.8 / 4.4 | 47.3 | 0.0 |
| 50 | 1.8 / 2.8 | 5.9 / 8.1 | 5.2 / 8.3 | 9.4 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 1.0 / 1.4 | 2.5 / 2.9 | 2.9 / 3.7 | 0.0 |
| 10000B | 1.0 / 1.3 | 2.6 / 3.0 | 2.8 / 3.6 | 0.0 |
| 500000B | 1.1 / 1.5 | 3.5 / 4.3 | 3.6 / 4.8 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 956 | 966 | 979 | 979 |
| 500 | 500 | 0 | 586 | 1012 | 1061 | 1061 |
| 2000 | 2000 | 0 | 764 | 1319 | 1432 | 1434 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 13.8 | 16.0 | 17.0 | 18.9 |
| separate instances | 400 | 12.7 | 16.9 | 18.4 | 18.8 |

# Profile: profile-after-phase-5

- **Date:** 2026-09-05T05:46:35+05:00
- **Commit:** `2637bcc` (working tree dirty)
- **Machine:** AMD Ryzen 9 9900X 12-Core Processor, 12 CPUs, 18416732 kB, linux/amd64
- **Stack:** Go go1.27.1, Redis 7.4.7, Postgres PostgreSQL 18.6
- **Config:** ramp 200→8000 sagas/s ×1.5, hold 6s; instances 10000,100000; payloads 100,10000,500000 bytes; timeouts 100,500,2000; pprof 10s

Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as "achieved < target".

## Findings

- Knee: 1518 sagas/s (~7590 requests/s). The next step, 2277 sagas/s, breached: p99 412 ms > 50 ms.
- Redis is near saturation at the knee: 97% of one core, ping p99 5.36 ms. Reducing commands per request (phase 7) raises the ceiling directly.
- Redis round-trips per request: start 2.0, publish 8.0, consume 7.0, final consume 11.0. Each round-trip is a sequential network hop, so per-request latency is roughly round-trips × RTT.
- Most expensive Redis work per request: json.set on consume(final) (4.0 calls × 60 µs).
- JSON.SET costs 61 µs against 12 µs for JSON.GET (5×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$..topic/from/to/failedAt`), so write count, not read count, is what Redis CPU pays for. A final consume does 4 writes.
- Instances already in Redis: consume p50 2.7 → 2.8 ms from 0 to 100000 instances (+0%). List endpoint p50 2.4 → 2.6 ms.
- Tasks per workflow: consume p50 2.7 → 4.1 ms from 2 to 50 tasks (+51%) at a constant ~1000 req/s. Growth here is the recursive-descent JSONPath and whole-document reads scaling with document size.
- Payload size: consume p50 2.8 → 3.6 ms from 100B to 500000B (+28%). Payloads live inside the document every JSONPath query scans; a consume never needs the payload.
- Simultaneous timeouts: max lag 839 ms at 100 → 1357 ms at 2000, i.e. ~0.3 ms per overdue task. The reaper is sequential; lag grows linearly with the number of tasks that expire together. Missing webhooks: 0.
- Contention: 20 concurrent reports on one instance p50 12.0 ms vs on separate instances 10.5 ms (+14%). Redis serializes writes to one key; the phase 6 Lua-per-transition design will make this the per-instance ceiling.
- Server CPU at the knee, cumulative share by engine function: instance_engine.(*Engine).UpdateInstance 50.54%, instance_engine.(*Engine).handleConsumeOrFail 22.23%, instance_engine.(*Engine).handlePublish 20.58%, instance_engine.jsonMatches[go.shape.string] 18.57%, instance_engine.jsonFirstMatch[go.shape.string] 16.46%

## 1. Saturation ramp

SLO: error rate ≤ 1%, p99 ≤ 50 ms, achieved ≥ 90% of target. **Knee: 1518 sagas/s.** Breach: p99 412 ms > 50 ms.

| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |
|---|---|---|---|---|---|---|---|---|---|
| 200 | 200 | 998 | 0 (0.00%) | 1.0 / 1.6 | 2.7 / 3.6 | 2.9 / 4.2 | 44 | 0.30 / 0.61 | ok |
| 300 | 299 | 1496 | 0 (0.00%) | 1.1 / 1.8 | 3.4 / 4.6 | 3.6 / 5.3 | 53 | 0.34 / 0.67 | ok |
| 450 | 449 | 2244 | 0 (0.00%) | 1.2 / 1.9 | 3.6 / 4.8 | 3.9 / 5.6 | 64 | 0.37 / 0.77 | ok |
| 675 | 673 | 3366 | 0 (0.00%) | 1.3 / 2.4 | 4.2 / 6.1 | 4.5 / 7.1 | 71 | 0.45 / 1.08 | ok |
| 1012 | 1003 | 5016 | 0 (0.00%) | 1.6 / 3.5 | 5.3 / 8.5 | 5.7 / 9.7 | 79 | 0.50 / 1.40 | ok |
| 1518 | 1431 | 7152 | 0 (0.00%) | 2.2 / 5.6 | 7.8 / 13.5 | 8.3 / 15.0 | 90 | 0.79 / 2.35 | ok |
| 2277 | 1577 | 7879 | 0 (0.00%) | 41.7 / 91.2 | 183.6 / 333.7 | 182.2 / 411.6 | 97 | 1.75 / 5.36 | **breach** |

## 2. Server profiles at the knee (1518 sagas/s)

Raw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.

### cpu

```
File: sagawise
Build ID: 264ca76b02de055304a5051b20780bf013f1566c
Type: cpu
Time: 2026-09-05 05:47:26 PKT
Duration: 10s, Total samples = 30.81s (308.09%)
Showing nodes accounting for 16.71s, 54.24% of 30.81s total
Dropped 820 nodes (cum <= 0.15s)
Showing top 25 nodes out of 274
      flat  flat%   sum%        cum   cum%
     7.29s 23.66% 23.66%      7.29s 23.66%  internal/runtime/syscall/linux.Syscall6
     4.14s 13.44% 37.10%      4.14s 13.44%  runtime.futex
     0.58s  1.88% 38.98%      1.25s  4.06%  runtime.pcvalue
     0.50s  1.62% 40.60%      0.50s  1.62%  runtime.procyieldAsm
     0.33s  1.07% 41.67%      0.43s  1.40%  runtime.step
     0.31s  1.01% 42.68%      0.31s  1.01%  runtime.memmove
     0.31s  1.01% 43.69%      0.31s  1.01%  time.runtimeNow
     0.29s  0.94% 44.63%      0.29s  0.94%  runtime.usleep
     0.26s  0.84% 45.47%      0.26s  0.84%  runtime.nextFreeFast (inline)
     0.25s  0.81% 46.28%      0.25s  0.81%  runtime.findfunc
     0.24s  0.78% 47.06%      0.24s  0.78%  runtime.memclrNoHeapPointers
     0.21s  0.68% 47.74%      1.11s  3.60%  runtime.lock2
     0.20s  0.65% 48.39%      0.50s  1.62%  internal/poll.runtime_pollSetDeadline
     0.20s  0.65% 49.04%      0.69s  2.24%  runtime.(*unwinder).resolveInternal
     0.20s  0.65% 49.69%      0.32s  1.04%  runtime.scanObject
     0.18s  0.58% 50.28%     14.77s 47.94%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.17s  0.55% 50.83%      2.26s  7.34%  runtime.mallocgc
     0.15s  0.49% 51.31%      0.35s  1.14%  runtime.markroot
     0.14s  0.45% 51.77%      6.67s 21.65%  runtime.findRunnable
     0.14s  0.45% 52.22%      0.35s  1.14%  runtime.mallocgcTinySC2
     0.13s  0.42% 52.65%      0.16s  0.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).popIdle
     0.13s  0.42% 53.07%      0.16s  0.52%  runtime.exitsyscall
     0.12s  0.39% 53.46%      0.69s  2.24%  runtime.growslice
     0.12s  0.39% 53.85%      0.16s  0.52%  runtime.mallocgcSmallScanNoHeaderSC2
     0.12s  0.39% 54.24%      0.21s  0.68%  runtime.tryDeferToSpanScan
```

### cpu-cumulative

```
File: sagawise
Build ID: 264ca76b02de055304a5051b20780bf013f1566c
Type: cpu
Time: 2026-09-05 05:47:26 PKT
Duration: 10s, Total samples = 30.81s (308.09%)
Showing nodes accounting for 8.15s, 26.45% of 30.81s total
Dropped 820 nodes (cum <= 0.15s)
Showing top 40 nodes out of 274
      flat  flat%   sum%        cum   cum%
     0.05s  0.16%  0.16%     21.29s 69.10%  net/http.(*conn).serve
     0.05s  0.16%  0.32%     18.59s 60.34%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP
         0     0%  0.32%     18.59s 60.34%  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.NewMiddleware.func2.1
         0     0%  0.32%     18.59s 60.34%  net/http.HandlerFunc.ServeHTTP
         0     0%  0.32%     18.59s 60.34%  net/http.serverHandler.ServeHTTP
         0     0%  0.32%     17.55s 56.96%  net/http.(*ServeMux).ServeHTTP
     0.02s 0.065%  0.39%     17.49s 56.77%  main.httpTracing.func1
         0     0%  0.39%     15.57s 50.54%  wtfsaga/instance_engine.(*Engine).UpdateInstance
     0.01s 0.032%  0.42%     14.79s 48.00%  github.com/redis/go-redis/v9.(*Client).Process
     0.01s 0.032%  0.45%     14.78s 47.97%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
     0.18s  0.58%  1.04%     14.77s 47.94%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
     0.05s  0.16%  1.20%     10.10s 32.78%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
     0.02s 0.065%  1.27%      9.01s 29.24%  github.com/redis/go-redis/v9.(*baseClient).process
     0.01s 0.032%  1.30%      8.95s 29.05%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%  1.30%      8.95s 29.05%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%  1.30%      8.95s 29.05%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
     0.02s 0.065%  1.36%      8.94s 29.02%  github.com/redis/go-redis/v9.(*baseClient).withConn
     0.01s 0.032%  1.40%      7.94s 25.77%  github.com/redis/go-redis/v9.cmdable.JSONGet
     7.29s 23.66% 25.06%      7.29s 23.66%  internal/runtime/syscall/linux.Syscall6
         0     0% 25.06%      7.09s 23.01%  runtime.mcall
     0.05s  0.16% 25.22%      6.99s 22.69%  runtime.schedule
     0.08s  0.26% 25.48%      6.97s 22.62%  github.com/redis/go-redis/v9.(*baseClient)._process.func1
     0.03s 0.097% 25.58%      6.89s 22.36%  runtime.park_m
         0     0% 25.58%      6.85s 22.23%  wtfsaga/instance_engine.(*Engine).handleConsumeOrFail
     0.01s 0.032% 25.61%      6.84s 22.20%  syscall.Syscall
         0     0% 25.61%      6.83s 22.17%  internal/poll.ignoringEINTRIO (inline)
     0.02s 0.065% 25.67%      6.80s 22.07%  syscall.RawSyscall6
     0.14s  0.45% 26.13%      6.67s 21.65%  runtime.findRunnable
     0.02s 0.065% 26.19%      6.34s 20.58%  wtfsaga/instance_engine.(*Engine).handlePublish
     0.01s 0.032% 26.23%      5.74s 18.63%  github.com/redis/go-redis/v9.cmdable.JSONSet (inline)
         0     0% 26.23%      5.73s 18.60%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
     0.02s 0.065% 26.29%      5.73s 18.60%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0% 26.29%      5.72s 18.57%  wtfsaga/instance_engine.jsonMatches[go.shape.string]
     0.03s 0.097% 26.39%      5.55s 18.01%  internal/poll.(*FD).Write
         0     0% 26.39%      5.45s 17.69%  syscall.Write (inline)
     0.01s 0.032% 26.42%      5.45s 17.69%  syscall.write
     0.01s 0.032% 26.45%      5.21s 16.91%  bufio.(*Writer).Flush
         0     0% 26.45%      5.11s 16.59%  net.(*conn).Write
         0     0% 26.45%      5.11s 16.59%  net.(*netFD).Write
         0     0% 26.45%      5.07s 16.46%  wtfsaga/instance_engine.jsonFirstMatch[go.shape.string]
```

### heap

```
File: sagawise
Build ID: 264ca76b02de055304a5051b20780bf013f1566c
Type: inuse_space
Time: 2026-09-05 05:47:36 PKT
Showing nodes accounting for 24976.36kB, 100% of 24976.36kB total
Showing top 25 nodes out of 76
      flat  flat%   sum%        cum   cum%
 6661.16kB 26.67% 26.67%  6661.16kB 26.67%  runtime.mallocgc
    6338kB 25.38% 52.05%     6338kB 25.38%  bufio.NewReaderSize (inline)
 5281.67kB 21.15% 73.19%  5281.67kB 21.15%  bufio.NewWriterSize (inline)
 2049.25kB  8.20% 81.40%  2049.25kB  8.20%  go.opentelemetry.io/otel/sdk/log.newRing
 1050.86kB  4.21% 85.60%  1050.86kB  4.21%  github.com/jackc/pgx/v5/internal/stmtcache.NewLRUCache (inline)
 1024.59kB  4.10% 89.71%  1024.59kB  4.10%  slices.Grow[go.shape.[]go.opentelemetry.io/otel/attribute.KeyValue,go.shape.struct { Key go.opentelemetry.io/otel/attribute.Key; Value go.opentelemetry.io/otel/attribute.Value }] (inline)
  521.05kB  2.09% 91.79%   521.05kB  2.09%  encoding/xml.map.init.0
  513.31kB  2.06% 93.85%  1564.17kB  6.26%  github.com/jackc/pgx/v5/pgxpool.NewWithConfig.func3
  512.25kB  2.05% 95.90%   512.25kB  2.05%  go.opentelemetry.io/otel/trace.attributeOption.applySpan (inline)
  512.16kB  2.05% 97.95%   512.16kB  2.05%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
  512.06kB  2.05%   100%   512.06kB  2.05%  github.com/felixge/httpsnoop.Wrap
         0     0%   100%   521.05kB  2.09%  encoding/xml.init
         0     0%   100%  1050.86kB  4.21%  github.com/jackc/pgx/v5.ConnectConfig
         0     0%   100%  1050.86kB  4.21%  github.com/jackc/pgx/v5.connect
         0     0%   100%  1564.17kB  6.26%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).initResourceValue.func1
         0     0%   100%  1536.66kB  6.15%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%  1536.66kB  6.15%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%  1536.66kB  6.15%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%  1024.41kB  4.10%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%   512.25kB  2.05%  github.com/redis/go-redis/v9.cmdable.ZAdd
         0     0%   100%   512.25kB  2.05%  github.com/redis/go-redis/v9.cmdable.ZAddArgs
         0     0%   100% 11619.67kB 46.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn
         0     0%   100% 11619.67kB 46.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).newConn
         0     0%   100% 11619.67kB 46.52%  github.com/redis/go-redis/v9/internal/pool.(*ConnPool).queuedNewConn.func2
         0     0%   100% 11619.67kB 46.52%  github.com/redis/go-redis/v9/internal/pool.NewConnWithBufferSize
```

### block

```
File: sagawise
Build ID: 264ca76b02de055304a5051b20780bf013f1566c
Type: delay
Time: 2026-09-05 05:47:37 PKT
Showing nodes accounting for 7861.66s, 99.88% of 7870.75s total
Dropped 141 nodes (cum <= 39.35s)
Showing top 25 nodes out of 58
      flat  flat%   sum%        cum   cum%
  7861.66s 99.88% 99.88%   7861.66s 99.88%  runtime.selectgo
         0     0% 99.88%    249.45s  3.17%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Acquire
         0     0% 99.88%    249.47s  3.17%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0% 99.88%    249.45s  3.17%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).Acquire
         0     0% 99.88%    249.45s  3.17%  github.com/jackc/puddle/v2.(*Pool[go.shape.*uint8]).acquire
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0% 99.88%   7491.50s 95.18%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0% 99.88%   7491.50s 95.18%  github.com/redis/go-redis/v9.(*Client).Process
         0     0% 99.88%   7491.50s 95.18%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0% 99.88%   7487.43s 95.13%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0% 99.88%   7487.43s 95.13%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0% 99.88%   7491.49s 95.18%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0% 99.88%   7491.50s 95.18%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0% 99.88%   3954.53s 50.24%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0% 99.88%   2697.18s 34.27%  github.com/redis/go-redis/v9.cmdable.JSONSet
         0     0% 99.88%   2697.18s 34.27%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0% 99.88%   2697.18s 34.27%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0% 99.88%    420.14s  5.34%  github.com/redis/go-redis/v9.cmdable.ZAdd
         0     0% 99.88%    420.14s  5.34%  github.com/redis/go-redis/v9.cmdable.ZAddArgs
         0     0% 99.88%    419.70s  5.33%  github.com/redis/go-redis/v9.cmdable.ZRem
```

### mutex

```
File: sagawise
Build ID: 264ca76b02de055304a5051b20780bf013f1566c
Type: delay
Time: 2026-09-05 05:47:37 PKT
Showing nodes accounting for 244.72s, 100% of 244.72s total
Dropped 393 nodes (cum <= 1.22s)
Showing top 25 nodes out of 109
      flat  flat%   sum%        cum   cum%
   226.22s 92.44% 92.44%    226.22s 92.44%  runtime.unlock (partial-inline)
    11.04s  4.51% 96.95%     11.04s  4.51%  runtime._LostContendedRuntimeLock
     7.46s  3.05%   100%      7.76s  3.17%  sync.(*Mutex).Unlock (partial-inline)
         0     0%   100%      6.23s  2.55%  _.goready.func1
         0     0%   100%     21.60s  8.83%  github.com/redis/go-redis/extra/redisotel/v9.(*metricsHook).ProcessHook.func1
         0     0%   100%     21.77s  8.90%  github.com/redis/go-redis/extra/redisotel/v9.(*tracingHook).ProcessHook.func1
         0     0%   100%     21.77s  8.90%  github.com/redis/go-redis/v9.(*Client).Process
         0     0%   100%      4.62s  1.89%  github.com/redis/go-redis/v9.(*Client).Process-fm
         0     0%   100%      8.20s  3.35%  github.com/redis/go-redis/v9.(*baseClient)._getConn
         0     0%   100%     21.57s  8.81%  github.com/redis/go-redis/v9.(*baseClient)._process
         0     0%   100%      8.20s  3.35%  github.com/redis/go-redis/v9.(*baseClient).getConn
         0     0%   100%     21.57s  8.81%  github.com/redis/go-redis/v9.(*baseClient).process
         0     0%   100%      4.62s  1.89%  github.com/redis/go-redis/v9.(*baseClient).process-fm
         0     0%   100%     21.57s  8.81%  github.com/redis/go-redis/v9.(*baseClient).processCommand
         0     0%   100%     21.57s  8.81%  github.com/redis/go-redis/v9.(*baseClient).processWithRetry
         0     0%   100%     12.19s  4.98%  github.com/redis/go-redis/v9.(*baseClient).releaseConn
         0     0%   100%     12.19s  4.98%  github.com/redis/go-redis/v9.(*baseClient).releaseConnToPool
         0     0%   100%     21.57s  8.81%  github.com/redis/go-redis/v9.(*baseClient).withConn
         0     0%   100%     12.19s  4.98%  github.com/redis/go-redis/v9.(*baseClient).withConn.func1
         0     0%   100%     21.77s  8.90%  github.com/redis/go-redis/v9.(*hooksMixin).processHook (inline)
         0     0%   100%     11.50s  4.70%  github.com/redis/go-redis/v9.cmdable.JSONGet
         0     0%   100%      8.11s  3.32%  github.com/redis/go-redis/v9.cmdable.JSONSet
         0     0%   100%      8.11s  3.32%  github.com/redis/go-redis/v9.cmdable.JSONSetMode (inline)
         0     0%   100%      8.11s  3.32%  github.com/redis/go-redis/v9.cmdable.JSONSetWithArgs
         0     0%   100%      1.33s  0.55%  github.com/redis/go-redis/v9.cmdable.ZAdd
```

### goroutine

```
File: sagawise
Build ID: 264ca76b02de055304a5051b20780bf013f1566c
Type: goroutine
Time: 2026-09-05 05:47:37 PKT
Showing nodes accounting for 150, 99.34% of 151 total
Showing top 25 nodes out of 125
      flat  flat%   sum%        cum   cum%
       145 96.03% 96.03%        145 96.03%  runtime.gopark
         1  0.66% 96.69%          1  0.66%  go.opentelemetry.io/otel/sdk/trace.(*tracer).Start
         1  0.66% 97.35%          1  0.66%  runtime.goroutineProfileWithLabels
         1  0.66% 98.01%          1  0.66%  runtime.notetsleepg
         1  0.66% 98.68%          1  0.66%  syscall.Syscall
         1  0.66% 99.34%          1  0.66%  syscall.Syscall6
         0     0% 99.34%         74 49.01%  bufio.(*Reader).Peek
         0     0% 99.34%          1  0.66%  bufio.(*Reader).ReadLine
         0     0% 99.34%          1  0.66%  bufio.(*Reader).ReadSlice
         0     0% 99.34%         75 49.67%  bufio.(*Reader).fill
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5.(*Conn).Exec
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5.(*Conn).exec
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5.(*Conn).execPrepared
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn.(*PgConn).ExecStatement
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn.(*PgConn).execExtendedSuffix
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn.(*PgConn).peekMessage
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn.(*PgConn).receiveMessage
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).readUntilRowDescription
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn.(*ResultReader).receiveMessage
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).Read
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgproto3.(*Frontend).Receive
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgproto3.(*chunkReader).Next
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgxpool.(*Conn).Exec
         0     0% 99.34%          5  3.31%  github.com/jackc/pgx/v5/pgxpool.(*Pool).Exec
         0     0% 99.34%          1  0.66%  github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck
```

## 3. Redis commands per request

Each endpoint run in isolation; `INFO commandstats` delta divided by requests.

| endpoint | command | calls / request | µs / call | µs / request |
|---|---|---|---|---|
| start | json.set | 1.00 | 57 | 57 |
| start | json.get | 1.00 | 10 | 10 |
| publish | json.set | 3.00 | 59 | 178 |
| publish | json.get | 4.00 | 11 | 45 |
| publish | zadd | 1.00 | 6 | 6 |
| consume | json.set | 2.00 | 61 | 121 |
| consume | json.get | 4.00 | 12 | 49 |
| consume | zrem | 1.00 | 4 | 4 |
| consume | zrange | 0.01 | 5 | 0 |
| consume(final) | json.set | 4.00 | 60 | 239 |
| consume(final) | json.get | 6.00 | 12 | 71 |
| consume(final) | zrem | 1.00 | 4 | 4 |
| consume(final) | zrange | 0.01 | 8 | 0 |

| endpoint | round-trips / request | redis µs / request |
|---|---|---|
| start | 2.0 | 66 |
| publish | 8.0 | 229 |
| consume | 7.0 | 174 |
| consume(final) | 11.0 | 314 |

## 4. Instances already in Redis (100 sagas/s)

| instances | start p50/p99 | publish p50/p99 | consume p50/p99 | list_p50_ms | list_p99_ms | get_p50_ms | redis_bytes_per_instance |
|---|---|---|---|---|---|---|---|
| 0 | 0.9 / 1.3 | 2.4 / 2.9 | 2.7 / 3.6 | 2.4 | 4.2 | 0.5 | 0.0 |
| 10000 | 0.9 / 1.3 | 2.4 / 2.9 | 2.7 / 3.5 | 1.4 | 2.1 | 0.5 | -13535.9 |
| 100000 | 0.9 / 1.4 | 2.5 / 3.0 | 2.8 / 3.7 | 2.6 | 3.6 | 0.5 | -320.3 |

## 5. Tasks per workflow (~1000 requests/s)

| tasks | start p50/p99 | publish p50/p99 | consume p50/p99 | sagas_per_sec | error_rate |
|---|---|---|---|---|---|
| 2 | 0.9 / 1.5 | 2.5 / 3.4 | 2.7 / 4.1 | 199.7 | 0.0 |
| 10 | 1.0 / 1.5 | 2.8 / 3.6 | 2.5 / 4.1 | 47.3 | 0.0 |
| 50 | 1.7 / 2.6 | 4.6 / 6.6 | 4.1 / 6.6 | 9.5 | 0.0 |

## 6. Payload size (50 sagas/s)

| payload | start p50/p99 | publish p50/p99 | consume p50/p99 | error_rate |
|---|---|---|---|---|
| 100B | 1.0 / 1.4 | 2.4 / 3.0 | 2.8 / 3.5 | 0.0 |
| 10000B | 0.9 / 1.3 | 2.4 / 3.0 | 2.8 / 3.5 | 0.0 |
| 500000B | 1.0 / 1.5 | 3.1 / 4.0 | 3.6 / 4.5 | 0.0 |

## 7. Simultaneous timeouts (reaper lag, deadline → webhook)

| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |
|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 812 | 827 | 839 | 839 |
| 500 | 500 | 0 | 576 | 1024 | 1079 | 1080 |
| 2000 | 2000 | 0 | 840 | 1323 | 1355 | 1357 |

## 8. Contention (20 concurrent reports, 10 rounds)

| | n | p50 ms | p95 ms | p99 ms | max ms |
|---|---|---|---|---|---|
| same instance | 400 | 12.0 | 13.2 | 13.7 | 15.3 |
| separate instances | 400 | 10.5 | 13.9 | 14.8 | 15.1 |

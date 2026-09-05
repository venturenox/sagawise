# Benchmarks

Every benchmark run is stored here, one directory per run, never overwritten.
Baseline runs before a change and runs after it are compared with
`make bench-compare`.

## Layout

```
docs/benchmarks/
  README.md                       this file
  runs/<date>_<commit>_<label>/   one directory per run
    report.md                     human-readable report
    load.json                     machine-readable results (input to compare)
    go-bench.txt                  raw `go test -bench` output (input to benchstat)
    env.txt                       machine, versions, commit, config
  comparisons/<date>_<A>_vs_<B>.md   side-by-side reports
```

## Running

```bash
make bench BENCH_LABEL=baseline          # one run; prints the run directory
make bench-compare A=runs/<a> B=runs/<b> # A = before, B = after
```

`make bench` stops the `sagawise` container for the duration of the run
(its reaper shares `task_deadlines` and would steal timeouts) and starts it
again afterwards. Redis and Postgres from `make start` must be running.
A full run takes about five minutes. Tune with `BENCH_ARGS`, e.g.
`make bench BENCH_LABEL=quick BENCH_ARGS="-rates 50 -duration 5s -lag-tasks 50 -skip-gobench"`.

## What is measured

The runner (`backend/cmd/bench`) builds the real server binary and launches
it on a free port against the local Redis and Postgres, with its own DSL
and its own webhook receiver, then drives it over HTTP.

- **Load.** Sagas are started open-loop at fixed rates (default 50, 100, 200
  per second, 20 s each). One saga = 5 requests on a two-task workflow:
  start, publish, consume, publish, consume. Recorded per endpoint: p50,
  p95, p99, max latency; error rate; achieved throughput.
- **Redis commands per saga.** From `INFO commandstats` deltas. The cost
  of the bookkeeping itself; the phase 7 efficiency target.
- **Archive completeness.** After each rate, `instance_history` rows for
  completed sagas are counted. Any shortfall is a lost archive (audit #9).
- **Reaper lag.** Tasks are published with a 2 s timeout and never
  consumed; lag is deadline → failure-webhook arrival. The reaper ticks
  once a second, so 0 to ~1000 ms is the design floor.
- **Go micro-benchmarks.** `instance_engine/bench_test.go`: start,
  publish, consume, full saga, and a reaper tick over 10 and 50 overdue
  tasks, called in-process. Compared with `benchstat`
  (`go install golang.org/x/perf/cmd/benchstat@latest`).

## Profile runs: finding the bottlenecks

```bash
make bench-profile BENCH_LABEL=baseline    # ~10 minutes; stored as runs/<date>_<sha>_profile-baseline
```

`make bench` answers "did it get faster or slower". `make bench-profile`
answers "where does the time go and what does it scale with". It writes
`report.md`, `profile.json`, `env.txt` and a `pprof/` directory. Sections:

1. **Saturation ramp.** Rate ×1.5 per step from 200 sagas/s until the SLO
   breaks (error rate > 1 %, p99 > 50 ms, or achieved < 90 % of target).
   The last passing rate is the **knee**. Redis CPU (% of one core) and
   Redis ping RTT are sampled during every step, so a knee with idle Redis
   points at the server; a knee with Redis near 100 % points at the
   command count. The load generator shares the machine; "achieved <
   target" at very high rates can be the generator, and the report says so.
2. **pprof at the knee.** CPU, heap, block, mutex and goroutine profiles
   of the server, with `pprof -top` in the report and raw files in
   `pprof/` (`go tool pprof -http=: <binary> pprof/cpu.pprof`). The server
   exposes pprof only when `SAGAWISE_PPROF_ADDR` is set, which only the
   bench harness does.
3. **Redis commands per request.** Each endpoint run in isolation;
   `INFO commandstats` delta per request, with µs per call. This is the
   round-trip count that bounds per-request latency, and the phase 7 target.
4. **Instances already in Redis.** Latency at 0, 10k and 100k existing
   instances, plus the list and get endpoints and Redis bytes per instance.
5. **Tasks per workflow.** 2, 10 and 50 tasks at a constant ~1000 req/s.
   Document size grows with task count; every JSONPath query scans it.
6. **Payload size.** 100 B, 10 KB, 500 KB publish bodies.
7. **Simultaneous timeouts.** 100, 500, 2000 tasks expiring together; the
   reaper is sequential, so max lag grows linearly. Any `missing` is a
   correctness failure.
8. **Contention.** 20 concurrent reports on one instance vs on 20 instances.

The report opens with a generated **Findings** list that reads the curves
and names the bottleneck each one implies. `make bench-compare` accepts two
profile runs and diffs the knee, ramp, round-trips, and every scaling curve.

## Reading a comparison

Latency and Redis-command changes are shown as A → B percentages;
negative is better. Error rate, archive missing, and reaper `missing`
should be zero in both; a non-zero value is a correctness regression, not
a performance number. Runs from different machines are flagged; do not
compare them.

## Runs

| run | label | commit | purpose |
|---|---|---|---|
| `runs/2026-09-05_0517_8f8e27c_baseline` | baseline | 8f8e27c | Main after phases 0–4 tooling, before any audit fix, quiet machine. 0 errors and 0 lost archives up to 200 sagas/s; ~3 ms per report; 36 Redis commands per saga; reaper lag p50 ≈ 1 s for 200 simultaneous timeouts. |
| `runs/2026-09-05_0521_8f8e27c_profile-baseline` | profile-baseline | 8f8e27c | Bottleneck profile before any fix. Knee 1518 sagas/s (~7.6k req/s) with Redis at 97 % of one core: Redis is the ceiling, driven by JSON.SET at 73 µs (RediSearch re-index per write) × 4–7 writes per request. 2→50 tasks per workflow costs +63 % latency (recursive-descent JSONPath). 100k existing instances: +2 %. Reaper lag grows ~0.4 ms per simultaneous timeout. |
| `runs/2026-09-05_0543_2637bcc_after-phase-5` | after-phase-5 | 2637bcc | After phase 5 (quick wins: fail-fast startup, DSL validation, TAG index fields, paged list, rueidis removed). Performance-neutral by design: every latency within ±8 % of baseline (noise), 36 Redis commands per saga unchanged, 0 errors, 0 lost archives. Comparison: `comparisons/2026-09-05_baseline_vs_after-phase-5.md`. |
| `runs/2026-09-05_0546_2637bcc_profile-after-phase-5` | profile-after-phase-5 | 2637bcc | Profile after phase 5. Same knee (1518 sagas/s), same Redis ceiling (97 % of one core, JSON.SET 61 µs), same round-trips per request. Confirms the TAG schema change did not move the bottleneck. Comparison: `comparisons/2026-09-05_profile-baseline_vs_profile-after-phase-5.md`. |
| `runs/2026-09-05_0738_84161db_after-phase-6` | after-phase-6 | 84161db | After phase 6 (state-machine rewrite: schema 2 docs, one Lua script per transition, durable archive/webhook queues). publish and consume p50 2.4–2.9 ms → 0.9 ms (-61 to -69 %), p99 -52 to -68 %; 0 errors, 0 lost archives; reaper lag p50 1010 → 189 ms (the webhook worker is nudged, no longer waits for a tick), p99 unchanged, one 3.1 s outlier (one delivery retried after the 2 s backoff). Redis commands per saga 36 → 37 (now counted inside the script). Go micro-benchmarks: publish -40 %, consume -35 %, allocations -34 %; reaper tick +9–10 % (each overdue member is one script call instead of ZREM+GET). Comparison: `comparisons/2026-09-05_after-phase-5_vs_after-phase-6.md`. |
| `runs/2026-09-05_0741_84161db_profile-after-phase-6` | profile-after-phase-6 | 84161db | Profile after phase 6. Knee unchanged at 1518 sagas/s, but at every rate consume p99 is -60 to -72 % and Redis CPU 5–15 points lower (89 % vs 97 % at 2277 sagas/s, where the breach is now the load generator, not p99). Client round-trips per report are 2; the "round-trips per request" table counts commands executed inside the Lua script too (`INFO commandstats` includes them), which is why it reads higher, not lower. 50 tasks per workflow +39 % (was +63 %), 500 KB payload +65 % (payload bytes travel with the publish and the script's JSON.SET), 100k instances -7 %. 2000 simultaneous timeouts: max lag 1001 ms (was 1357). Comparison: `comparisons/2026-09-05_profile-after-phase-5_vs_profile-after-phase-6.md`. |

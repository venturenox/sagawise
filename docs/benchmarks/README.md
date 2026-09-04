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

## Reading a comparison

Latency and Redis-command changes are shown as A → B percentages;
negative is better. Error rate, archive missing, and reaper `missing`
should be zero in both; a non-zero value is a correctness regression, not
a performance number. Runs from different machines are flagged; do not
compare them.

## Runs

| run | label | commit | purpose |
|---|---|---|---|

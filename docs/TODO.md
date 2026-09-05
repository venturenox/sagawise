# Sagawise TODO

Ordered. Each phase is one or more PRs. Tick items as they land.
Findings numbered `#N` refer to `docs/correctness-audit-2026-08-29.md`.

## Phase 0 — CI

Done on branch `ci/phase-0` (2026-09-04). Run locally with `make ci-tools` then `make ci`.

- [x] Add GitHub Actions workflow. Runs on every PR. (`.github/workflows/ci.yml`)
- [x] Service containers: redis-stack and postgres.
- [x] Steps: `go vet`, `staticcheck`, `golangci-lint`.
- [x] Steps: `govulncheck`, `gosec`.
- [x] Step: `go test -race ./...`.
- [x] Build the Docker image in CI to catch Dockerfile breakage.
- [x] Bump Go 1.22.2 → 1.27.1 and refresh deps. govulncheck went from 42 findings to 0.

Temporary exclusions (remove when the linked phase lands):
- `errcheck` is disabled in `backend/.golangci.yml` → Phase 5 (#7).
- gosec `G104` (unhandled errors) excluded → Phase 5 (#7).
- gosec `G706` (log injection: raw query params in logs) excluded → Phase 9 structured logging.

## Phase 1 — Make the code testable

Done on branch `testability` (2026-09-04), stacked on `ci/phase-0`.

- [x] Pass Redis and Postgres clients into the engine. No package-level globals. (`Engine` struct in `instance_engine/engine.go`)
- [x] Replace `context.Background()` vars with request contexts. (archive goroutine uses `context.WithoutCancel`)
- [x] Add a clock interface so the reaper can be tested with a fake clock. (`Clock`; also `ServiceRegistry` and `HTTPClient` seams for webhooks)
- [x] Put integration tests behind a build tag so the Docker build stage still passes. (`-tags integration`; `make test-integration`; CI runs them against service containers)
- [x] DSL dir and services file are parameters (`SAGAWISE_DSL_DIR`, `SAGAWISE_SERVICES_FILE`), so tests and local runs need no `/sagawise` mount.

## Phase 2 — Write the contract

Done: `docs/contract.md` (branch `contract`, 2026-09-04). Seven decisions (D1–D7) at the end of it need sign-off before phase 3 encodes them.

- [x] Define what `is_retry` means. One page. (#2) → contract §4
- [x] Define the full task and instance state machine. Every allowed transition. → contract §2, §3
- [x] Define what happens to sibling tasks when an instance goes terminal. (#3) → contract I5, decision D1
- [x] Define error responses: 4xx for client mistakes, 5xx for infra failures. (#7) → contract §9
- [x] Sign off decisions D1–D7 in `docs/contract.md`. All seven accepted 2026-09-04.

## Phase 3 — Test suite

Done on branch `tests` (2026-09-05). Written against `docs/contract.md`, not current behavior: tests today's code cannot pass are wrapped in `testx.XFail(t, "#N", …)` (Go), `todo` (Node), `xfail(strict=True)` (Python). They report as skipped with the finding, and flip to a hard failure when the fix lands so the wrapper is removed. `make test-status` lists what is still owed, grouped by finding.

- [x] Unit tests: DSL parsing and validation. Reject missing or zero timeouts. (#6) → `templating/dsl_test.go`
- [x] Unit tests: state transitions, table-driven, every allowed and forbidden move. → `contract_transitions_test.go` (42 rows)
- [x] Integration tests: full publish → consume → archive flow against real Redis and Postgres. → `integration_test.go`
- [x] Integration tests: publish → timeout → reaper → webhook fired → archived. → `integration_test.go`
- [x] Concurrency tests under `-race`: consume vs fail on one task. (#1) → `contract_concurrency_test.go`
- [x] Concurrency tests: reaper vs consume on sibling tasks. (#1) → reaper vs consume on the last task; concurrent sibling failures
- [x] Retry tests: every retry case from the contract. (#2) → transition rows with `retry=true`; strict `is_retry` parsing
- [x] Terminal-instance tests: events after archive are rejected. (#3) → `contract_terminal_test.go` + sibling rows
- [x] Fuzz tests: `event_name`, `service_name`, `workflow_instance_id`. (#13) → `FuzzUpdateInstanceQueryValues` + injection tests
- [x] Fuzz tests: publish body with `topic`/`to`/`index` keys. (#12) → `FuzzPublishBody` + payload-shadowing tests
- [x] List endpoint tests: pagination, hyphenated names, empty results. (#10) → `contract_list_test.go`
- [x] Failure-injection tests: Redis error between claim and write. (#4, #9) → `contract_faults_test.go` (go-redis hook)
- [x] Failure-injection tests: Postgres down during archive. (#9) → pgxpool `BeforeConnect` fault
- [x] Failure-injection tests: webhook that never responds. (#5)
- [x] SDK tests: Node and Python actually send requests and raise on error. (#15) → `sdk/nodejs/test`, `sdk/python/tests`
- [x] Startup tests: bad DSL dir or bad DB config fails the process. (#8) → `backend/startup_test.go` (runs the real binary on `SAGAWISE_ADDR`)
- [x] HTTP shape: status codes, JSON error bodies, get-by-id (D4–D7) → `contract_http_test.go`

Known-failing count at phase 3 close (`make test-status`): see the phase 3 commit message. Every one of those is a phase 5/6 deliverable.

## Phase 4 — Baseline benchmark

Tooling on branch `bench` (2026-09-05): `backend/cmd/bench` + `instance_engine/bench_test.go`; `make bench BENCH_LABEL=<label>` writes one immutable run directory under `docs/benchmarks/runs/`; `make bench-compare A= B=` writes a before/after report under `docs/benchmarks/comparisons/`. See `docs/benchmarks/README.md`.

- [x] Go benchmarks for `UpdateInstance` and the reaper tick. → start, publish, consume, full saga, reaper tick ×10/×50
- [x] HTTP load script. Record p50, p99, error rate at fixed event rates. → Go load generator in `cmd/bench`, open-loop, real binary over HTTP
- [x] Measure reaper lag: time from deadline to webhook under load.
- [x] Measure Redis commands per request. → `INFO commandstats` delta per saga
- [x] Measure archive completeness under load (lost `instance_history` rows, #9).
- [x] Save results in `docs/benchmarks/`. One directory per run, never overwritten; `env.txt` records machine and commit.
- [x] Baseline run recorded before phase 5: `docs/benchmarks/runs/2026-09-05_0517_8f8e27c_baseline`.
- [x] Bottleneck profile (`make bench-profile`): saturation ramp with pprof at the knee, Redis command breakdown, scaling curves (instances, tasks per workflow, payload size, simultaneous timeouts), contention. Baseline: `runs/2026-09-05_0521_8f8e27c_profile-baseline`. Verdict: Redis CPU is the ceiling (JSON.SET re-index per state write); recursive-descent JSONPath scales with document size; reaper lag is linear in simultaneous timeouts.
- [ ] After each of phases 5, 6, 7: run `make bench BENCH_LABEL=after-phase-N` and `make bench-profile BENCH_LABEL=after-phase-N`, and commit both comparisons. Phase 5 done: `runs/2026-09-05_0543_2637bcc_after-phase-5`, `runs/2026-09-05_0546_2637bcc_profile-after-phase-5`, neutral (same knee, ±8 % noise). Phase 6 done: `runs/2026-09-05_0738_84161db_after-phase-6`, `runs/2026-09-05_0741_84161db_profile-after-phase-6`: publish/consume p50 -61 to -69 % and p99 -52 to -72 % at every rate, Redis CPU lower, knee unchanged (1518 sagas/s), reaper lag p50 -81 %, 0 errors, 0 lost archives. Phase 7 done: `runs/2026-09-05_0811_b974cf0_after-phase-7`, `runs/2026-09-05_0818_a89cf09_profile-after-phase-7`: Redis commands per saga -14 %, document writes per report roughly halved, reaper max lag -46 % at 2000 simultaneous timeouts, 0 errors, 0 lost archives. See the phase 7 section for the two measurement caveats (the reaper-lag harness fix, and which comparison to trust).

## Phase 5 — Quick wins PR

Done on branch `quick-wins` (2026-09-05), stacked on `bench`. Contract tests that flipped from XFAIL to passing: startup (#8, #6), DSL validation (#6), list paging/escaping (#10), strict `is_retry`, get-by-id (D7), and the non-string-index payload-shadowing case (a decode error is now an error, not `""`; the string-index case still waits for phase 6). Node `todo`s and Python `xfail`s are gone.

- [x] Webhook client with a timeout. (#5) → `instance_engine.WebhookTimeout` (5 s), the `New` default. The reaper still calls it inline; the worker is phase 6.
- [x] Reject non-positive or missing DSL timeouts at load. (#6) → `templating.Validate`: contract §7 in full (name, ≥1 task, topic/from/to, timeout > 0, unique `(topic, to)`, unique names across files). Nothing is stored until every file validates.
- [x] Fix Node SDK `is_retry` loose-equality bug. Rethrow errors. (#15) → explicit presence checks, `is_retry` must be a boolean, every failure rejects.
- [x] Fix Python SDK: raise instead of return. Fix seconds-vs-ms timeout. → default 1.0 s; `is_retry` sent as `true`/`false`.
- [x] Startup returns errors. `main` fails fast. (#8) → `DBConnect`/`ConnectPostgres`/`ParseDSL` return errors; `main` also checks every DSL `from` has a `failure_url` (W5) and handles SIGTERM as well as SIGINT.
- [x] Re-enable `errcheck` in `.golangci.yml` and drop `G104` from `GOSEC_EXCLUDE` (Makefile + ci.yml). (#7) → both on; the "helpers return errors, callers map to 5xx" half of #7 stays in phase 6.
- [x] Remove `/shutdown` or make it only cancel the signal context. (#14) → removed, with the Helm `preStop` hook that called it. One teardown path: drain server → stop reaper → close clients.
- [x] Delete rueidis. One Redis client. (#11) → `ListWorkflowInstances` uses go-redis `FTSearchWithArgs`; `Engine.Search` is gone.
- [x] List endpoint: explicit LIMIT, pagination params, escaped filter values, 5xx on errors. (#10) → `limit` (default 50, max 1000), `offset`, response `{ids, total, limit, offset}` with bare ids; empty page is 200. Filter fields became TAG in `workflows_index` so values match literally; `templating.ensureIndex` versions the schema under `index_schema:<name>` and recreates the index on change.
- [x] Parse `is_retry` strictly. Reject garbage. → `true`/`false`, case-insensitive; anything else 400.
- [x] `GetWorkflowInstance`: only allow `workflow_instance:` keys. → takes `workflow_instance_id` (alphanumeric); `doc_key` is gone (D7). Postman collection, example README and bench updated.

## Phase 6 — State machine rewrite PR

One design. One PR. Test suite from Phase 3 is the gate.

Done on branch `state-machine` (2026-09-05), stacked on `quick-wins`. Design: `docs/design-phase-6.md`. Every contract test now runs unwrapped (`make test-status` prints none); 6 new contract tests cover the queues.

- [x] Write a short design note first. Get it agreed. — `docs/design-phase-6.md`, agreed 2026-09-05.
- [x] Move tasks under a `$.tasks` array in the Redis doc. — schema 2 document (`instanceDoc`); no migration of schema 1 docs, `make clean` to upgrade a dev stack (P4).
- [x] Resolve tasks in Go. No string-built JSONPath. (#12, #13) — `readTaskIdentity` reads `$.tasks[*].topic`/`$.tasks[*].to`, plain string equality in `UpdateInstance`.
- [x] Store payloads outside the searchable part of the doc. — payloads stay at `$.tasks[i].payload`; only explicit `$.tasks[*].{topic,from,to}` paths are indexed (P1).
- [x] Fix the `workflows_index` schema so payload fields are not indexed. — no `$..` paths; `ensureIndex` recreates the index on first start.
- [x] Helpers return errors. Callers map infra errors to 5xx. (#7) — `jsonMatches`/`jsonFirstMatch`/`markTask` deleted; `jsonGet` and `transition` return errors; handlers answer 500 `INTERNAL`. D4/D5/D6 done: all responses JSON with stable codes.
- [x] One Lua script per transition: check state, write state, update deadline. Atomic. (#1, #2, #3) — `instance_engine/transition.lua` (one script, action argument, P2), run via `Engine.transition`.
- [x] Cancel sibling deadlines when an instance goes terminal. (#3) — in the script, same step as `$.state=FAILED`.
- [x] Reaper claims durably. Mark failed, then remove deadline. Never the reverse. (#4) — the `reap` action of the script; no pre-ZREM.
- [x] Reaper treats Redis errors as retriable, not as "not PUBLISHED". (#4) — a script error leaves the member; only NOT_FOUND/BAD_SCHEMA drop it.
- [x] Webhooks go to a worker. A slow endpoint cannot stall the tick. (#5) — `webhook_pending` zset + `Worker` (`queue.go`): 30 s lease, 2 s×3 backoff capped 5 min, 8 attempts, 16 parallel deliveries (P3).
- [x] Archive via a pending-archive set drained by a retrying worker. (#9) — `archive_pending` zset enqueued by the script; 1 s×2 backoff capped 30 s, never gives up.
- [x] Graceful shutdown: drain server → stop reaper → drain workers → close clients. — `main.go`; in-flight jobs finish (bounded by their timeouts), the rest stays leased in Redis.
- [x] Decide: fold the build-time registry refactor (DSL + `services.json`) into this PR or not. — Not folded in (P5).
- [x] Benchmarks: runs `after-phase-6` and comparisons against the phase 5 runs — see the "After each of phases" line under Phase 4.

## Phase 7 — Efficiency PR

Done on branch `state-machine` (2026-09-05), stacked on phase 6. Measured
against the phase 6 runs; the phase 6 profile's verdict (Redis CPU is the
ceiling, and `JSON.SET` pays for it because every write re-indexes the whole
document) is what these changes target.

- [x] Cache `services.json` in memory. — `FileRegistry` reads and parses once
  on the first lookup, then serves from a map. The file is baked into the
  image at build time, so a re-read never saw a change anyway; read/parse
  errors are not cached, so a transient failure is retried. `FileRegistry` is
  now used by pointer (it carries the cache).
- [x] One doc read plus one pipeline per `/update_instance`. — a report was
  already 2 client round-trips since phase 6 (one `JSON.GET` to resolve the
  task, one `EVALSHA`); what was left was the *write* count inside the
  script. `transition.lua` now writes once per task instead of once per
  field, carrying the state changes in a single `JSON.MERGE`. Measured
  document writes per request: publish 3 → 2, consume 2 → 1, terminal
  consume 4 → 2, fail/reap 2 → 1. The payload keeps its own `JSON.SET`
  because it must be *replaced*, not RFC 7386 deep-merged — verified: a
  republish with fewer keys does not leave the old ones behind.
- [x] Reaper tick as a single Lua batch. — the `reap_batch` action reads the
  overdue members of `task_deadlines` and reaps each inside Redis, so a tick
  is one round-trip instead of one per expired task. Each member still goes
  through the same `run()` path as an HTTP report, so the state machine lives
  in one place and mark-failed/remove-deadline stays atomic (#4, TO5, TO6).
- [x] Fix list/get N+1. — there was none left: phase 5 already made
  `/workflow_instances/list` a single `FT.SEARCH` returning bare ids and
  `/workflow_instances/get` a single `JSON.GET`. `ListWorkflows` had a
  related defect and was fixed instead: with no `LIMIT`, `FT.SEARCH` returns
  its default 10 documents, so the endpoint silently truncated the workflow
  list at the eleventh workflow.
- [x] Raise server `ReadTimeout` above 1s. — 1 s → 15 s. It covers the whole
  body, so 1 s cut off large publish payloads on a slow link as a bare EOF
  rather than an actionable error. `ReadHeaderTimeout` stays tight (2 s).
- [x] Re-run benchmarks. Record before/after. — see below.

Benchmarks. Authoritative runs (`b974cf0`/`a89cf09`, after the harness fix
below): `runs/2026-09-05_0811_b974cf0_after-phase-7` and
`runs/2026-09-05_0818_a89cf09_profile-after-phase-7`.

- Redis commands per saga −14 % at every rate; 0 errors, 0 lost archives.
- Document writes per request: publish 3 → 2, consume 2 → 1, terminal
  consume 4 → 2 (measured, `INFO commandstats`).
- Redis commands per request: publish −14 %, consume −17 %, terminal consume
  −15 %; Redis CPU lower at every rate on the ramp.
- Simultaneous timeouts (max reaper lag): 100 tasks −21 %, 500 −25 %,
  2000 −46 % (2295 → 1248 ms). This is the batched tick.

Two caveats recorded so the numbers are not over-read:

- The reaper-lag measurement was wrong before this phase. It published its
  tasks in one sequential loop and stamped each deadline from the client
  clock, so at n≥500 the publish phase outlasted the 2 s timeout and the
  reported lag went negative (min −876 ms). Fixed in `b974cf0`/`a89cf09`:
  publish concurrently, stamp each task from the return of its own publish.
  Any reaper-lag figure in a run before `b974cf0` is unreliable.
- The `_vs_` comparison against the *original* phase 6 profile
  (`comparisons/2026-09-05_profile-after-phase-6_vs_profile-after-phase-7.md`)
  overstates the win (it shows knee +50 %, start round-trips −69 %). That run's
  `INFO commandstats` delta was polluted by concurrent queue-worker traffic.
  The trustworthy pair re-ran the phase 6 *server* under today's harness in
  the same session:
  `comparisons/2026-09-05_profile-p6server-newharness_vs_profile-after-phase-7.md`
  (`runs/2026-09-05_0821_a89cf09_profile-p6server-newharness`). It shows the
  knee unchanged at 2277 sagas/s — the ramp is generator-bound at that point
  — with the command-count and reaper-lag wins above. Superseded phase 7 runs
  (`d13f921`, `0814_b974cf0`) are kept because runs are immutable, but are not
  the reference.

## Phase 8 — Security

- [ ] Write a threat model. One page. Who can call what.
- [ ] Add auth to the API. API key or mTLS.
- [ ] Replace wildcard CORS with an allowlist.
- [ ] Sign or authenticate outgoing failure webhooks.
- [ ] Review Helm chart: secrets, network policy, non-root container.
- [ ] Add dependency scanning (Dependabot or Renovate).

## Phase 9 — Operations

- [ ] Confirm Redis AOF persistence is on in compose and Helm. Deadlines must survive a restart.
- [ ] Metrics: reaper lag, pending deadlines, archive failures, webhook failures.
- [ ] Structured logging with instance ID on every line. Then drop `G706` from `GOSEC_EXCLUDE`.
- [ ] Alerts on archive failures and reaper lag.
- [ ] Runbook: what to do when Redis or Postgres is down.
- [ ] Health checks reflect real state: reaper alive, DBs reachable.

## Phase 10 — Release

- [ ] Publish Node SDK to npm.
- [ ] Publish Python SDK to PyPI.
- [ ] Version the HTTP API.
- [ ] Update Postman collection and README.
- [ ] Tag a release.

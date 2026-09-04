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

Run once on current main before any fix. Keep the numbers.

- [ ] Go benchmarks for `UpdateInstance` and the reaper tick.
- [ ] HTTP load script (k6 or vegeta). Record p50, p99, error rate at fixed event rates.
- [ ] Measure reaper lag: time from deadline to webhook under load.
- [ ] Measure Redis commands per request.
- [ ] Save results in `docs/benchmarks/`.

## Phase 5 — Quick wins PR

- [ ] Webhook client with a timeout. (#5)
- [ ] Reject non-positive or missing DSL timeouts at load. (#6)
- [ ] Fix Node SDK `is_retry` loose-equality bug. Rethrow errors. (#15)
- [ ] Fix Python SDK: raise instead of return. Fix seconds-vs-ms timeout.
- [ ] Startup returns errors. `main` fails fast. (#8)
- [ ] Re-enable `errcheck` in `.golangci.yml` and drop `G104` from `GOSEC_EXCLUDE` (Makefile + ci.yml). (#7)
- [ ] Remove `/shutdown` or make it only cancel the signal context. (#14)
- [ ] Delete rueidis. One Redis client. (#11)
- [ ] List endpoint: explicit LIMIT, pagination params, escaped filter values, 5xx on errors. (#10)
- [ ] Parse `is_retry` strictly. Reject garbage.
- [ ] `GetWorkflowInstance`: only allow `workflow_instance:` keys.

## Phase 6 — State machine rewrite PR

One design. One PR. Test suite from Phase 3 is the gate.

- [ ] Write a short design note first. Get it agreed.
- [ ] Move tasks under a `$.tasks` array in the Redis doc.
- [ ] Resolve tasks in Go. No string-built JSONPath. (#12, #13)
- [ ] Store payloads outside the searchable part of the doc.
- [ ] Fix the `workflows_index` schema so payload fields are not indexed.
- [ ] Helpers return errors. Callers map infra errors to 5xx. (#7)
- [ ] One Lua script per transition: check state, write state, update deadline. Atomic. (#1, #2, #3)
- [ ] Cancel sibling deadlines when an instance goes terminal. (#3)
- [ ] Reaper claims durably. Mark failed, then remove deadline. Never the reverse. (#4)
- [ ] Reaper treats Redis errors as retriable, not as "not PUBLISHED". (#4)
- [ ] Webhooks go to a worker. A slow endpoint cannot stall the tick. (#5)
- [ ] Archive via a pending-archive set drained by a retrying worker. (#9)
- [ ] Graceful shutdown: drain server → stop reaper → drain workers → close clients.
- [ ] Decide: fold the build-time registry refactor (DSL + `services.json`) into this PR or not.

## Phase 7 — Efficiency PR

Measure against the Phase 4 baseline.

- [ ] Cache `services.json` in memory.
- [ ] One doc read plus one pipeline per `/update_instance`.
- [ ] Reaper tick as a single Lua batch.
- [ ] Fix list/get N+1.
- [ ] Raise server `ReadTimeout` above 1s.
- [ ] Re-run benchmarks. Record before/after.

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

# Sagawise Backend Correctness Audit

**Date:** 2026-08-29
**Repo state:** branch `correctness`, commit `0a511ae` (immediately after the `*pgx.Conn` → `*pgxpool.Pool` fix)
**Scope:** everything under `backend/`, plus the SDK client wrappers where they affect the API contract
**How it was produced:** Claude Code multi-agent review at extra-high effort — 10 independent finder angles (line-by-line, state-machine invariants, cross-file contracts, Go pitfalls, API boundary, reuse, simplification, efficiency, altitude, conventions), ~70 raw candidates deduplicated, each surviving finding verified; the highest-impact claims were **reproduced live** against the running Docker stack (marked "live-verified" below).

## If you're reading this years later: what is this?

Sagawise is the saga-pattern distributed-transaction *bookkeeper* in this repo: services exchange real messages over their own transport (Kafka etc.) and separately report `publish`/`consume`/`fail` events to Sagawise over HTTP. Sagawise compares the reports against a declared workflow DSL. The only thing it infers on its own is **silence** — a task published but not consumed before its timeout is marked FAILED and the publisher's `failure_url` webhook is called so it can compensate.

Architecture at audit time: stdlib `net/http` server on :5000 (`backend/main.go`); Redis (RedisJSON + RediSearch) is the live store; Postgres holds only `instance_history`, the archive of finished instances. Instances are RedisJSON docs `workflow_instance:<id>` with tasks keyed by numeric index. Deadlines live in the sorted set `task_deadlines`; a reaper goroutine ticks every second (`backend/instance_engine/reaper.go`).

This audit found that **the core bookkeeping is not trustworthy under concurrency, retries, adversarial input, or infrastructure hiccups.** Line numbers below are as of commit `0a511ae` — they will drift, but each finding names the function, which should survive refactors.

## What was already fixed before/during the audit session

- ✅ `*pgx.Conn` shared across goroutines (handlers + reaper) → replaced with concurrency-safe `*pgxpool.Pool`.
- ✅ Blind `time.Sleep(3s)` wait-for-Postgres at startup → bounded ping-retry loop.

---

## The 15 findings

Grouped into four themes. Each has a checkbox — tick it when fixed, and note the fixing commit next to it.

### Theme 1 — The state machine is not atomic

Every task/instance state transition is a *read state → check in Go → write state* sequence against RedisJSON with **no concurrency control** (no Lua, no WATCH/MULTI). All the guards in the handlers are therefore racy at the root. **The deep fix for this whole theme is one Lua script (or transaction) per transition** that checks state, writes state, and adds/removes the deadline atomically.

- [ ] **1. Non-atomic transitions / terminal-gate race** — `checkWorkflowState`, `instance_engine.go:376` (root cause at `:277`).
  The terminal-state guard is check-then-act. Reaper failing task A can race a consume completing task B: both pass the guard, write conflicting terminal states, and spawn duplicate archive goroutines — `instance_history` can permanently record COMPLETED for an instance whose live state is FAILED (whose compensation webhook fired). Concurrent consume+fail on one task completes it, then flips it FAILED and fires compensation for a task that succeeded.

- [ ] **2. `is_retry=true` bypasses every gate** — `handleConsumeOrFail` `instance_engine.go:259`, `handlePublish` `:214`.
  A retry consume resurrects a FAILED task to COMPLETED *after* its compensation webhook fired; a retry consume on a never-published PENDING task marks it COMPLETED; a retry fail flips a COMPLETED task to FAILED (with webhook); a retry publish regresses a terminal task to PUBLISHED inside an already-archived instance and re-arms its deadline. Redis then permanently disagrees with the archive row (`ON CONFLICT DO NOTHING` never updates it). Retry semantics need to be defined properly: a retry should be *idempotent re-delivery of the same report*, not a gate bypass.

- [ ] **3. Terminal instances keep accepting events** — `handlePublish`, `instance_engine.go:187`; no instance-level check anywhere, and sibling deadlines are not cancelled on terminal transition (`checkWorkflowState:336`).
  After a two-task instance goes FAILED and is archived, publishing the still-PENDING second task works (no retry flag needed), schedules a deadline inside the dead instance, and later fires a second compensation webhook. An already-PUBLISHED sibling's orphaned deadline does the same on its own.

### Theme 2 — Timeout enforcement (the product's core guarantee) has silent single points of failure

- [ ] **4. Reaper spends the deadline before recording the failure** — `reapExpiredDeadlines`, `reaper.go:54`.
  ZREM-as-claim destroys the deadline first; if the process crashes, or the follow-up state read hits a transient Redis error (which `jsonFirstMatch` swallows into `""`), the member is gone and the task stays PUBLISHED **forever** — no webhook, no terminal state, no archive. Fix direction: claim durably (move to a "processing" set, or fail-then-remove atomically in Lua) and treat Redis errors as retriable, not as "not PUBLISHED".

- [x] **5. Failure webhook has no timeout and blocks the reaper** (timeout: phase 5, branch `quick-wins`; webhook worker: phase 6) — `reportFailure`, `instance_engine.go:323`.
  `http.DefaultClient` (no timeout), called synchronously inside the reaper's sequential loop. One `failure_url` that accepts the connection and never responds stalls the single reaper goroutine forever → **all timeout enforcement system-wide stops until restart**. Fix: `http.Client{Timeout: ~5s}` at minimum; better, dispatch webhooks to a worker so a slow endpoint can't stall the tick.

- [x] **6. DSL timeouts are not validated** (phase 5, branch `quick-wins`) — `ParseDSL`, `templating.go:55`.
  `"timeout": 0` loads without complaint → every publish schedules a deadline of `now+0` → the reaper fails every task of that workflow within 1 second and fires compensation, systematically, even though consumers were fine. A missing/typo'd timeout key means no deadline at all → task can hang PUBLISHED forever. Fix: reject non-positive/missing timeouts at DSL load, loudly.

### Theme 3 — Errors are systematically swallowed (outages masquerade as business outcomes)

- [ ] **7. `jsonMatches`/`jsonFirstMatch`/`markTask` swallow all errors** (`errcheck` and gosec G104 are on since phase 5; helpers returning errors is phase 6) — `instance_engine.go:52–77`. **Root cause of much of this theme.**
  Redis outage → `UpdateInstance` returns 400 "workflow_instance Not Found" for a live instance; publish returns 403 "Task Already COMPLETED or FAILED" for a PENDING task; consume 404s — clients abandon healthy sagas. Conversely `markTask`'s failed JSONSet still lets the handler answer 200 "Instance State Updated" while the write was lost. Fix: make the helpers return errors; callers map infra errors to 5xx.

- [x] **8. Startup is log-and-continue; server serves half-initialized** (phase 5, branch `quick-wins`) — `ParseDSL`, `templating.go:114` (and the whole `else` block at `:39`).
  `CREATE TABLE`/`FT.CREATE`/DSL errors are logged or discarded; an empty or mis-mounted `/sagawise` dir (the `fs.Glob` error is also discarded) skips index **and** table creation entirely; a nil pgx pool from bad config panics here. The server then starts, reports healthy, and every archive INSERT fails with "relation does not exist" *forever* — history silently evaporates. Fix: init functions return errors; `main` fails fast and lets the orchestrator restart.

- [ ] **9. Archive is one-shot fire-and-forget** — `checkWorkflowState`, `instance_engine.go:391`.
  Instance is stamped terminal in Redis *first*, then a detached goroutine re-reads and INSERTs. If the goroutine dies (process exit — `srv.Shutdown` doesn't wait for it — Postgres briefly down, insert error is log-only), the terminal guard blocks any retry: the row is lost permanently. Fix: archive synchronously from the in-memory doc, or mark-and-enqueue into a pending-archive set drained by a retrying worker (the deadlines-zset + reaper pattern already in the repo).

- [x] **10. List endpoint: capped at 10, errors become 404, values unescaped** (phase 5, branch `quick-wins`) — `ListWorkflowInstances`, `instance_engine.go:493`.
  **Live-verified:** the index held 4,100 instances; the endpoint returned exactly 10 IDs with a 200 (no `LIMIT` sent → RediSearch default page). Also live-verified: `?workflow_name=order-flow` returns 0 matches (hyphen parsed as RediSearch negation) → 404 for a workflow that exists. The FT.SEARCH error is discarded, so a dropped index is indistinguishable from "no instances". Fix: explicit LIMIT + pagination params, escape/tag-quote filter values, surface errors as 5xx.

- [x] **11. Rueidis client: dial error discarded, config split-brain** (phase 5, branch `quick-wins`: rueidis deleted) — `ConnectRueidis`, `db_connect.go:62`.
  `rueidis.NewClient` dials **eagerly** (unlike go-redis); the error is discarded, so a boot race leaves a nil client for the process lifetime → every `/live`, `/ready`, `/health` request panics (`client.B()` on nil) → permanent probe failure → crash-loop; `/workflow_instances/list` and `/shutdown` panic too. Separately it ignores `REDIS_CONNECTION_STRING`, so a URL-configured deployment points rueidis at a *different Redis* than go-redis. Simplest fix: **delete rueidis entirely** — go-redis already runs FT.SEARCH (see `ListWorkflows`); one client, one config path.

### Theme 4 — Trust-boundary gaps

- [ ] **12. JSONPath payload-shadowing hijacks task resolution** — `handleConsumeOrFail`, `instance_engine.go:249` (also `handlePublish:189`). **Live-verified, the single worst finding.**
  Task lookup uses recursive descent (`$..[?(@.topic=='T' && @.to=='S')].index`) over the *whole* document — which includes stored message payloads. A publish body containing `{"topic":"T","to":"S","index":"0"}` gets matched by a later consume and resolves to the wrong task. If the payload's `index` is non-string, decode yields `""` and `markTask` writes path `$..state` — flipping **every** task and the workflow to COMPLETED and archiving a bogus instance. Fix: never resolve tasks by recursive descent — iterate the known task indexes (or move tasks under `$.tasks[...]` and query only there), and don't store payloads where the query can see them.

- [ ] **13. JSONPath injection via query params** — `instance_engine.go:189/249`. **Live-verified.**
  `event_name`/`service_name` are spliced unescaped into the filter: `event_name=x' || @.topic!='zzz` matched every task; an innocent apostrophe breaks the query into a swallowed error → spurious 404 → deadline never cleared → reaper later fails a task that was consumed. Fix follows from #12: resolve tasks in Go, not via string-built JSONPath.

- [x] **14. `/shutdown`: unauthenticated, destructive, and deadlocks** (phase 5, branch `quick-wins`: endpoint removed) — `main.go:81`.
  A plain GET behind wildcard CORS closes both Redis clients and the pg pool while in-flight handlers and the reaper still use them, then calls `srv.Shutdown` *from inside its own handler* — which waits for that very connection to go idle → deadlock; the 200 is never written, and `main` exits the moment `ListenAndServe` returns, killing archive goroutines mid-insert. Fix: drop the endpoint (Kubernetes sends SIGTERM) or make it just cancel the signal context so there is exactly one ordered teardown path (drain server → stop reaper → close pools).

- [x] **15. Node SDK: every non-retry call is a silent no-op** (phase 5, branch `quick-wins`) — `sdk/nodejs/sagawise.js:52` (also `:84`, `:115`).
  `is_retry == ''` is `true` for the default `is_retry = false` (JS loose equality: both coerce to 0), so `publish_message`/`consume_message`/`fail_message` throw "Required keys…" *before sending any HTTP request*, and the catch returns the error instead of rethrowing. Anyone using the published SDK is tracking nothing. (The repo examples use raw axios, which is why it went unnoticed.) Fix: use `=== undefined`/explicit presence checks, and rethrow or return a typed failure.

---

## Found but cut from the formal report (15-finding cap)

- **Python SDK** (`sdk/python/sagawise/sagawise.py`) — fixed in phase 5 (branch `quick-wins`): (a) caught exceptions are **returned** as truthy values — callers get a `RequestException` where a `workflow_instance_id` is expected, then report events against `str(exception)` forever while the saga runs untracked; (b) `timeout=1000` is passed to `requests`, which reads **seconds** — ~16.7 minutes per hung call (the Node SDK's identical `1000` is axios *milliseconds*, confirming ms was intended).
- **`GetWorkflowInstance` reads arbitrary keys** — fixed in phase 5 (takes `workflow_instance_id`; contract D7) — only validated "contains a colon", so `?doc_key=workflow_template:x` (or any other app's RedisJSON key on the shared instance) is readable through the unauthenticated endpoint.
- **`workflows_index` recursive-descent schema** (`$..topic`, `$..from`, `$..to`) indexes payload fields too: payloads pollute list filters, and a type-mismatched payload field can knock the whole document out of the index.
- **`is_retry` parsed with `strconv.ParseBool` ignoring the error** — fixed in phase 5: `true`/`false` only, else 400.
- **Efficiency:** `services.json` re-read + re-parsed from disk on *every* failure webhook; ~8 Redis round-trips per `/update_instance` (one full-doc read + pipeline would do); reaper does 2N+1 commands per tick; list/get is a cross-endpoint N+1; server `ReadTimeout` of 1s can drop slow POST bodies.
- **Cleanup:** `"workflow_instance:"+id` key-building scattered across ~8 sites; `$.<index>.<field>` path concatenation across two files; two Redis clients where one suffices (see #11); per-package `context.Background()` vars detach handler I/O from request contexts (breaks OTel spans and cancellation); `ping: PONG` string-compare health checks; task identity as top-level numeric keys forcing the `strconv.Atoi` "is this a task?" heuristic — tasks under a `$.tasks` array would kill several findings at once.

## Suggested order of attack

1. **Quick, high-value, low-risk (an afternoon):** webhook timeout (#5), DSL timeout validation (#6), Node SDK check (#15), Python SDK returns (cut list), fail-fast startup (#8), delete `/shutdown` or reduce it to a context cancel (#14), delete rueidis (#11), explicit LIMIT + escaping on list (#10).
2. **The real project (do as one design):** atomic state transitions via Lua/transactions (#1, #2, #3), task resolution in Go instead of string-built recursive JSONPath (#12, #13), reaper claim ordering (#4), archive via durable queue or synchronous insert (#9), error-returning helpers (#7). These all touch the same code — `instance_engine` — and are best fixed together, ideally alongside moving tasks to a `$.tasks` array.
3. **Then:** the efficiency batch (services.json cache, pipelined Redis ops, reaper Lua batch).

A rebuild note for future-you: DSL files and `services.json` are baked into the image at build time — any fix touching those still requires `make restart` (see CLAUDE.md), unless the slated registry refactor happened by the time you read this.

# Phase 6 design: the state-machine rewrite

**Status:** agreed and implemented on branch `state-machine`, 2026-09-05. Roadmap phase 6 (`docs/TODO.md`). Decisions P1–P7 below were all taken as recommended.
**Goal:** make `instance_engine` satisfy `docs/contract.md` in full, so every remaining `testx.XFail` flips to `testx.Run`: #1, #2, #3, #4, #5, #7, #9, #12, #13, D4, D6.
**Scope:** one PR on branch `state-machine`, stacked on `quick-wins`. No efficiency work beyond what the design gives for free; phase 7 measures and tunes.

What stays the same: the HTTP routes and query parameters, the `task_deadlines` sorted set and its member format, the archive table, the SDKs, the DSL, `services.json`, the reaper interval. What changes is the document layout, how a report becomes a transition, and how the three side effects (deadline, webhook, archive) are made durable.

---

## 1. Document layout, schema 2

Today a task is a top-level key named by its index (`$.0`, `$.1`), which forces the "is this key a number" heuristic and the recursive-descent JSONPath that #12 and #13 exploit.

```json
{
  "schema": 2,
  "name": "order_flow", "version": "1.0", "schema_version": "1.0",
  "state": "PENDING",
  "startedAt": 1757030400,
  "completedAt": 0,
  "failedAt": 0,
  "tasks": [
    {"topic": "order_created", "from": "orders", "to": "payments", "timeout": 20000,
     "state": "PUBLISHED", "publishedAt": 1757030401, "consumedAt": 0, "failedAt": 0,
     "payload": {"order_id": 42}}
  ]
}
```

- Tasks live under `$.tasks`, addressed by array index. The `index` field is dropped; the position is the identity, as in the DSL.
- Timestamps stay unix seconds. Absent stamps are `0`, never missing, so every path in the Lua script exists.
- Instance `completedAt` is the terminal time for both terminal states (the archive column uses it); `failedAt` is set only on FAILED and feeds the `failed_at` filter.
- Payloads stay inside the document at `$.tasks[i].payload` so `/workflow_instances/get` and the archive row are still one read. They are "outside the searchable part" because the index only names explicit paths:

```
workflows_index  ON JSON PREFIX 1 workflow_instance:
  $.name            AS workflow_name    TAG
  $.state           AS workflow_state   TAG
  $.tasks[*].topic  AS topic            TAG
  $.tasks[*].from   AS from             TAG
  $.tasks[*].to     AS to               TAG
  $.startedAt       AS started_at       NUMERIC
  $.completedAt     AS completed_at     NUMERIC
  $.failedAt        AS failed_at        NUMERIC
```

  No `$..` anywhere, so a payload field named `topic` is never indexed and a type-mismatched payload field cannot knock the document out of the index. `ensureIndex` recreates the index on first start from the schema hash; no manual step.

- **No migration of schema 1 documents.** Nothing is released (phase 10), and a migration would be dead code after one deploy. A report against a document without `$.tasks` answers 500 `INTERNAL` with a log line naming the schema. Upgrading a dev stack is `make clean`. **[P4]**

## 2. A report is one read plus one script

`/update_instance` becomes a straight line: parse, resolve, transition, respond. No detached goroutines, no check-then-act.

1. **Parse.** Same parameters as today. `is_retry` strict, `action_type` in {publish, consume, fail}. Any failure is 400 with a JSON error body (§9).
2. **Resolve in Go.** One `JSON.GET workflow_instance:<id> $.tasks[*].topic $.tasks[*].to`. A missing key is 404 `INSTANCE_NOT_FOUND`. The two arrays are compared to `event_name` and `service_name` with plain string equality in Go, giving the target index list (`publish`: every task with the topic; `consume`/`fail`: the one `(topic, to)` task). No match is 404 `TASK_NOT_FOUND`. Query values are never spliced into a path or query. (#12, #13)
3. **Transition in Lua.** One `EVALSHA transition.lua` with the index list. The script re-reads state under Redis's single-threaded execution, so the Go-side read is only for resolution and error messages; the script's answer is the truth. (#1, #2, #3)
4. **Respond.** The script's result code maps to the contract's status code and JSON body. Infrastructure errors from either round-trip are 500 `INTERNAL`; the state is untouched because the script either ran completely or not at all. (#7)
5. **Nudge.** If the script reports "webhook needed" or "instance became terminal", the handler pokes the matching worker (a non-blocking send on a wake channel). The work item itself was already enqueued by the script, so a lost nudge only costs latency.

Redis round-trips per report drop from 7 to 11 down to 2. That is a measurable change, so the PR ends with `make bench` and `make bench-profile` runs and comparisons, as phase 4 requires.

## 3. The transition script

One embedded file, `instance_engine/transition.lua`, loaded with `redis.NewScript` (EVALSHA with EVAL fallback). One script with an action argument rather than three files, because the terminal-instance gate, sibling freeze and archive enqueue are shared and must not drift. **[P2]**

```
KEYS  1 workflow_instance:<id>   2 task_deadlines   3 archive_pending   4 webhook_pending
ARGV  1 action     publish | consume | fail | reap
      2 is_retry   0 | 1
      3 indexes    comma-separated task indexes
      4 now_s      engine clock, seconds (stamps)
      5 now_ms     engine clock, milliseconds (deadline score)
      6 payload    JSON, publish only
      7 id         instance id (deadline and queue members)
```

Steps, all inside the one atomic call:

1. Read `$.state`, `$.tasks[*].state`, `$.tasks[*].timeout` in one `JSON.GET`. Payloads are never read by the script.
2. **Instance gate (I4).** If the instance is terminal: the report is answered `INSTANCE_TERMINAL`, unless `is_retry=1` and the report is a duplicate under §4, which is `IDEMPOTENT` with no side effects at all. `reap` on a terminal instance removes the stale member and returns `STALE`.
3. **Task gate (§2, §4).** Evaluate every target index against the table below. For `publish` with several indexes the decision is all-or-nothing: if any task refuses, nothing changes and the refusal is the answer. **[P6]**
4. **Write.** `JSON.SET` the new state and stamp (and payload on publish). `ZADD` the deadline on publish (or re-arm on a D2 retry), `ZREM` it on consume, fail and reap.
5. **Instance transition (I1, I2, I3).** If the task became COMPLETED and every task is COMPLETED, set `$.state=COMPLETED`, `$.completedAt`. If it became FAILED, set `$.state=FAILED`, `$.completedAt`, `$.failedAt`, and `ZREM` every sibling deadline (I5). On either, `ZADD archive_pending <now_ms> <id>`.
6. **Webhook enqueue (W1).** If a task became FAILED, `ZADD webhook_pending <now_ms> <id>:<index>`.
7. Return `{code, task_states_json, instance_state, terminal_now, webhook_queued}`.

| code | HTTP | when |
|---|---|---|
| `OK` | 200 | transition made |
| `IDEMPOTENT` | 200 | duplicate under `is_retry=true`; body carries `idempotent: true` |
| `INSTANCE_TERMINAL` | 409 | I4 |
| `TASK_NOT_PUBLISHED` | 409 | consume or fail on PENDING |
| `TASK_ALREADY_PUBLISHED` | 409 | publish on PUBLISHED, `is_retry=false` |
| `TASK_ALREADY_COMPLETED` | 409 | per §2 |
| `TASK_ALREADY_FAILED` | 409 | per §2 |
| `STALE` | n/a | reap: task no longer PUBLISHED or instance terminal; member removed |
| `NOT_FOUND` | 404 | document vanished between resolve and script |

The `reap` action is `fail` with two differences: it only acts on a PUBLISHED task in a non-terminal instance, and its "no" is a silent `STALE`. Because marking FAILED and removing the deadline happen in the same call, the deadline is never spent before the failure is recorded. A Redis error means the script did not run and the member is still there for the next tick. (#4, TO5, TO6)

## 4. Reaper

```
every 1 s:
  members = ZRANGEBYSCORE task_deadlines -inf now_ms LIMIT 0 1000
  for each member: EVALSHA transition.lua reap
```

No `ZREM` before the script: the transition is the claim. A second replica running the same tick gets `STALE` for anything it lost, which is the first time multiple replicas are safe by construction (still untested; contract §11). The batch of 1000 bounds one tick; a backlog drains over successive ticks. Phase 7 may fold the loop into one script call.

The reaper never calls a webhook. It only runs scripts and nudges the webhook worker. (#5, TO7)

## 5. Two durable queues, one worker pattern

Both side effects that leave Redis get the same treatment as deadlines: a sorted set is the queue, the score is "when it is due", and the entry is added by the transition script itself, so it cannot be lost between the state change and the work. **[P3]**

```
archive_pending   member <id>          score due_ms
webhook_pending   member <id>:<index>  score due_ms
webhook_attempts  hash   member → attempts so far
```

A worker is a goroutine with a 1 s ticker and a wake channel:

1. **Claim.** One small script: `ZRANGEBYSCORE key -inf now LIMIT 0 N`, then `ZADD` each returned member with score `now + lease` (30 s). A crash mid-job means the member comes back after the lease. This is the at-least-once guarantee; the consumers are idempotent (`ON CONFLICT DO NOTHING`; W3 says receivers tolerate duplicates).
2. **Work.**
   - Archive: `JSON.GET $` (the document is immutable once terminal, so the row always equals the final Redis state, A1), `INSERT … ON CONFLICT DO NOTHING`.
   - Webhook: `JSON.GET $.tasks[i].from $.tasks[i].to $.tasks[i].payload`, registry lookup, POST with the 5 s client, query `service=<to>`.
3. **Done:** `ZREM`. **Failed:** `ZADD` with backoff.
   - Archive retries forever: 1 s doubling, capped at 30 s. A Postgres outage is a backlog, never a lost row. (#9, A2, A3)
   - Webhook retries with 2 s tripling, capped at 5 min, and gives up after 8 attempts (about 15 min): `ZREM`, `HDEL`, one log line, a counter. State never changes on the outcome. (D3, W3, W4)

One `zqueue` type in `instance_engine` carries claim/done/retry; the two workers differ only in the work function. Concurrency inside a worker: the archive worker runs jobs sequentially (Postgres is the bottleneck and order does not matter); the webhook worker runs up to 16 deliveries in parallel so one slow endpoint delays at most its own retry, not the others (TO7).

A webhook whose task was never published (W2) sends `{}`; the script cannot produce that case but the worker must not crash on a missing payload.

## 6. HTTP surface (D4, D5, D6)

All responses are JSON. Success on `/update_instance`:

```json
{"workflow_instance_id": "…", "task_index": 0, "task_state": "COMPLETED", "workflow_state": "PENDING", "idempotent": false}
```

`task_index` and `task_state` are arrays when a publish resolved several tasks. Errors are `{"error": CODE, "message": "…"}` with the codes of contract §9. Status mapping: 400 parse errors, 404 unknown workflow/instance/task, 409 every script refusal, 500 any Redis or script error. `/start_instance` on an unknown workflow becomes 404 `WORKFLOW_NOT_FOUND` (today 400). `/workflows/list` with no templates becomes 200 `[]` (today 404), for consistency with the instances list.

`/workflow_instances/get` returns the schema 2 document, so callers see `tasks[]` instead of `"0"`, `"1"`. The SDKs pass response bodies through untouched; the Postman collection, `examples/order_flow/README.md` and `backend/docs/engine-internals.html` are updated in the PR. The order_flow README also drops two claims that stopped being true ("completion is evaluated asynchronously", "only completed workflows are archived").

## 7. Engine and shutdown

`Engine` gains `Archiver` and `Webhooks` workers and a `transition *redis.Script`; the helpers `jsonMatches`, `jsonFirstMatch`, `markTask`, `checkWorkflowState`, `reportFailure` and `archiveInstance` are deleted. Every remaining function returns an error; only handlers and worker loops log. `context.WithoutCancel` goes away because nothing runs detached from a request any more.

Startup, after validation: load the script, start the reaper, start both workers (they drain any backlog left by the previous process), then serve.

Shutdown, in order: `srv.Shutdown` (10 s) → stop reaper → stop workers, each waiting for its in-flight jobs (bounded by the 5 s webhook timeout and a 10 s insert timeout) → close clients. Anything not finished is still in its queue with a lease and resumes on the next start.

## 8. Tests

- Every `XFail` in `contract_*_test.go` and the `#N` rows in `contract_transitions_test.go` become `testx.Run`. The harness's `taskState`, `taskPayload` and `deadline` helpers read `$.tasks[i]`.
- New contract tests: archive backlog drains after a simulated restart (enqueue, new engine, worker tick); webhook delivered after N failures with attempts visible in `webhook_attempts`; webhook given up after the cap with state unchanged; lease reclaim after a worker dies mid-job; shared-topic publish is all-or-nothing; a schema 1 document is a clean 500.
- Unit tests for the Lua state table: the script runs against the local Redis in the integration suite, but the Go-side code→HTTP mapping and task resolution are pure and get table tests.
- `cmd/bench/profile.go` seeds schema 2 documents; `cmd/bench/run.go` fixes its cleanup query to the TAG syntax.
- `make ci` green, then `make bench` and `make bench-profile` with label `after-phase-6`, comparisons against the phase 5 runs, committed with the PR.

## 9. Out of this PR

- Registry refactor (DSL and `services.json` at build time). Orthogonal, and the TODO asks for a decision: **not folded in.** [P5]
- Caching `services.json`, `JSON.MSET` to cut re-indexing per transition, batching the reaper tick: phase 7, measured.
- Eviction of terminal documents from Redis (A4), metrics for queue depth and give-ups (phase 9; the counters are kept in the workers so phase 9 only has to export them).

---

## Decisions to agree

| # | Decision | Alternative |
|---|---|---|
| P1 | Payloads stay at `$.tasks[i].payload`; the index uses explicit paths, so they are not searchable. | Separate key `workflow_payload:<id>:<i>`. Smaller re-index per write, but `get` and archive become N+1 reads and every payload needs its own cleanup. Revisit in phase 7 if the profile says re-indexing is the cost. |
| P2 | One `transition.lua` with an action argument. | Three scripts. Duplicates the terminal gate, sibling freeze and enqueue logic. |
| P3 | Webhook delivery is queued durably in Redis, same pattern as the archive queue. | In-memory retry. Simpler, but a crash between the FAILED write and delivery loses the webhook, which contradicts W3. |
| P4 | No schema 1 → 2 migration; upgrade a dev stack with `make clean`. | Startup scan-and-convert. About 40 lines that run once and then rot. |
| P5 | The build-time registry refactor stays out. | Fold it in. Doubles the PR's surface for an unrelated concern. |
| P6 | A shared-topic publish is all-or-nothing. | Publish the tasks that can be, refuse the rest, answer 409. Today's behavior; violates T4's "one winner" reading and leaves the client unsure what happened. |
| P7 | Reaper batch of 1000 members per tick, no pre-claim. | Unbounded tick. Simpler, but one huge backlog stalls the tick for its full length. |

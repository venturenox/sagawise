# Sagawise Behavioral Contract

**Status:** v1, accepted 2026-09-04 (decisions D1–D7 all accepted). Roadmap phase 2 (`docs/TODO.md`).
**Purpose:** the single source of truth for what the engine must do. Phase 3 turns every rule here into a test. Phases 5 and 6 make the code match. Where current code disagrees with this document, the document wins and the audit finding is noted as `#N` (`docs/correctness-audit-2026-08-29.md`).

Decisions that change today's observable behavior are marked **[DECISION]** and listed at the end for sign-off.

---

## 1. Vocabulary

- **Workflow (template):** a named DSL file: an ordered list of tasks. Loaded at startup.
- **Instance:** one run of a workflow, identified by a 20-character `workflow_instance_id`.
- **Task:** one hop of the saga: service `from` publishes `topic`, service `to` must consume it within `timeout` ms. Identified inside an instance by its DSL index.
- **Report:** an HTTP call to `/update_instance` claiming that something happened on the real transport: `publish`, `consume`, or `fail`.
- **Deadline:** the moment a PUBLISHED task becomes overdue: `publishedAt + timeout`.
- **Reaper:** the background loop that fails overdue tasks.
- **Failure webhook:** the POST Sagawise sends to a service's `failure_url` so it can compensate.

Sagawise is a bookkeeper. It never moves messages. It only records reports, enforces the order and timing the DSL declares, and infers one thing on its own: silence past a deadline.

## 2. Task state machine

States: `PENDING` → `PUBLISHED` → `COMPLETED` | `FAILED`. `COMPLETED` and `FAILED` are terminal.

Four events act on a task. Three are reports; one is the reaper.

| Current state | `publish` | `consume` | `fail` | reaper (deadline passed) |
|---|---|---|---|---|
| PENDING | → PUBLISHED, deadline armed | 409 `TASK_NOT_PUBLISHED` | 409 `TASK_NOT_PUBLISHED` | impossible (no deadline exists) |
| PUBLISHED | dup (see §4) | → COMPLETED, deadline removed | → FAILED, deadline removed, webhook | → FAILED, webhook |
| COMPLETED | dup | dup | 409 `TASK_ALREADY_COMPLETED` | no-op (stale deadline is discarded) |
| FAILED | 409 `TASK_ALREADY_FAILED` | 409 `TASK_ALREADY_FAILED` | dup | no-op |

"dup" means the report repeats a transition that already happened. §4 defines what happens.

Rules:

- **T1.** A task leaves a terminal state under no circumstances. Not on retry, not on re-publish. (#2)
- **T2.** A task is resolved by `(topic, to)` for `consume` and `fail`, and by `topic` alone for `publish` (every task with that topic is published together, as today). Resolution never looks inside stored payloads. (#12, #13)
- **T3.** `publish` stores the request body as the task's `payload`. It is the body that the failure webhook later carries.
- **T4.** Every transition is atomic: check state, write state, arm or remove the deadline, all in one step. Two concurrent reports for the same task see exactly one winner; the loser gets the 409 the winner's new state implies. (#1)
- **T5.** The deadline set and the task state never disagree for longer than one atomic step. A COMPLETED or FAILED task has no deadline. A PUBLISHED task has exactly one. (#4)

## 3. Instance state machine

States: `PENDING` → `COMPLETED` | `FAILED`. Both are terminal.

- **I1.** The instance becomes `COMPLETED` the moment its last task becomes COMPLETED.
- **I2.** The instance becomes `FAILED` the moment any task becomes FAILED.
- **I3.** The transition to a terminal state happens in the same atomic step as the task transition that caused it. Exactly one task transition can cause it; a second cannot. (#1)
- **I4.** A terminal instance rejects every state-changing report with 409 `INSTANCE_TERMINAL`, whatever the target task's state. The only reports it still answers with 200 are idempotent duplicates under `is_retry=true` (§4), and in a terminal instance those have no side effects at all: a duplicate `publish` does not re-arm a deadline or replace the payload. (#2, #3)
- **I5. Siblings freeze.** When an instance becomes FAILED, every other task keeps the state it has. PENDING stays PENDING. PUBLISHED stays PUBLISHED, and its deadline is removed in the same step, so the reaper never touches it and no second webhook fires. **[DECISION D1]** One failure per instance, one webhook per instance. (#3)
- **I6.** An instance with zero tasks cannot be started; the DSL validation in §7 guarantees this.

## 4. Retries and duplicates

A **duplicate** is a report that asks for a transition the task has already made: `publish` on a PUBLISHED task, `consume` on a COMPLETED task, `fail` on a FAILED task. Duplicates happen for one legitimate reason: at-least-once delivery on the caller's side (a retried HTTP call, a redelivered message).

`is_retry` is the caller's statement that it knows it may be re-sending. It changes only how a duplicate is answered. **It never unlocks a transition that §2 forbids.** (#2)

| Duplicate report | `is_retry=false` | `is_retry=true` |
|---|---|---|
| `publish` on PUBLISHED | 409 `TASK_ALREADY_PUBLISHED`, no change | 200, payload replaced, **deadline re-armed** from now **[DECISION D2]** (no side effects if the instance is terminal, I4) |
| `consume` on COMPLETED | 409 `TASK_ALREADY_COMPLETED`, no change | 200, no change |
| `fail` on FAILED | 409 `TASK_ALREADY_FAILED`, no change | 200, no change |

Everything that is not a duplicate ignores `is_retry`:

- `is_retry=true` `consume` on a PUBLISHED task is just a consume.
- `is_retry=true` `consume` on a FAILED task is 409 (T1). Compensation already fired; the task cannot come back.
- `is_retry=true` `consume` on a PENDING task is 409 (nothing was published).
- `is_retry=true` `fail` on a COMPLETED task is 409 (T1).
- `is_retry=true` anything on a terminal instance is 409 (I4).

`is_retry` must be exactly `true` or `false`. Anything else is 400 `INVALID_PARAM`. Today it silently becomes `false`.

Rationale for D2: a re-published message means the consumer gets a fresh copy, so it gets a fresh window. This is the only case where a retry has a side effect.

## 5. Timeouts

- **TO1.** `timeout` in the DSL is milliseconds, an integer, strictly greater than zero.
- **TO2.** The deadline is `publishedAt + timeout`, computed from the engine clock at publish time.
- **TO3.** The reaper fails a task no earlier than its deadline and no later than `deadline + reaper interval` (1 s) under normal operation.
- **TO4.** Reaping a task is the `fail` transition of §2 with the same atomicity and the same webhook. The reaper and a concurrent `consume` see exactly one winner (T4).
- **TO5.** The reaper never loses a deadline. If the process dies or Redis errors between noticing an overdue task and failing it, the deadline is still there on the next tick. (#4)
- **TO6.** A Redis error while reading a task's state is not evidence about the state. The reaper leaves the deadline in place and retries next tick. Today an error is treated as "not PUBLISHED" and the deadline is dropped. (#4, #7)
- **TO7.** One slow or dead `failure_url` must not delay the reaping of any other task. (#5)

## 6. Failure webhook

- **W1.** Fires exactly when a task enters FAILED, whether by `fail` report or by the reaper. Given I5, that is at most once per instance.
- **W2.** Target: the `failure_url` registered for the failed task's `from` service. Method POST, `Content-Type: application/json`, body = the task's stored payload (`{}` if the task was never published, which under §2 cannot happen, but the code must not crash), query `service=<to>`.
- **W3.** Delivery has a timeout (5 s) and is retried with backoff for a bounded period. Delivery is at-least-once; receivers must tolerate duplicates. **[DECISION D3]** (#5)
- **W4.** Delivery outcome never changes any state. A webhook that can never be delivered is logged and counted; the instance stays FAILED and archived.
- **W5.** A `from` service with no registered `failure_url` is a startup-time DSL validation error (§7), not a runtime surprise.

## 7. Startup and DSL validation

The process starts serving only if all of this holds, otherwise it logs the reason and exits non-zero. Kubernetes restarts it; a broken config never runs half-initialized. (#6, #8)

- Redis and Postgres are reachable.
- The DSL directory exists and contains at least one file.
- Every DSL file parses, and every workflow in it has: a non-empty unique `name`; at least one task; for every task non-empty `topic`, `from`, `to`, and an integer `timeout > 0`; and no two tasks with the same `(topic, to)` pair (a consume would be ambiguous). Two tasks may share a `topic` with different `to`, as `user_creation` does today.
- Every `from` service in every DSL has a `failure_url` in the service registry.
- RediSearch indexes and the Postgres table exist or were created.

## 8. Archive

- **A1.** Every instance that reaches a terminal state gets exactly one row in `instance_history`, whose `instance_data.state` equals the instance's final Redis state. (#9)
- **A2.** Archiving is at-least-once and idempotent (the `ON CONFLICT DO NOTHING` on `id` stays). A crash or Postgres outage between the terminal transition and the insert must not lose the row: the work is queued durably and retried. (#9)
- **A3.** A terminal transition never waits on Postgres. The report that caused it gets its 200 once Redis is updated.
- **A4.** The Redis document stays after archiving, as today. Eviction is out of scope for v1.

## 9. HTTP contract

### Status codes

| Code | Meaning | Examples |
|---|---|---|
| 200 | Report accepted, or an idempotent duplicate under `is_retry=true` | |
| 400 | The request is malformed | missing param, unknown `action_type`, `is_retry` not `true`/`false`, body not valid JSON on `publish` |
| 404 | The thing named does not exist | unknown `workflow_name`, unknown `workflow_instance_id`, no task matches `(topic, to)` |
| 409 | The request is well formed but the state machine forbids it | every 409 code in §2–§4 |
| 500 | Sagawise's own infrastructure failed | Redis or Postgres error, timeout, unexpected reply |
| 503 | Not ready | `/ready` while a dependency is unreachable |

**[DECISION D4]** Today's 403s become 409s: nothing about these is authorization. Today's "instance not found" is a 400 and becomes a 404.

**[DECISION D5]** An infrastructure error is never reported as a business outcome. Today a Redis outage produces 400 "Not Found" and 403 "Already COMPLETED"; under this contract it is 500 and the client should retry. (#7)

### Bodies

All responses are JSON. **[DECISION D6]**

Success on `/update_instance`:

```json
{"workflow_instance_id": "…", "task_index": 0, "task_state": "COMPLETED", "workflow_state": "PENDING", "idempotent": false}
```

`idempotent` is `true` on an accepted duplicate. When `publish` resolves several tasks (shared topic), `task_index` and `task_state` become arrays.

Errors:

```json
{"error": "TASK_ALREADY_COMPLETED", "message": "task 1 (payment_done → shipping) is already COMPLETED"}
```

Error codes are stable strings: `MISSING_PARAM`, `INVALID_PARAM`, `INVALID_BODY`, `WORKFLOW_NOT_FOUND`, `INSTANCE_NOT_FOUND`, `TASK_NOT_FOUND`, `TASK_NOT_PUBLISHED`, `TASK_ALREADY_PUBLISHED`, `TASK_ALREADY_COMPLETED`, `TASK_ALREADY_FAILED`, `INSTANCE_TERMINAL`, `INTERNAL`.

### Read endpoints

- `/workflow_instances/list` returns a page, never a silent cap: `limit` (default 50, max 1000) and `offset`, plus `total`. Filter values are escaped; a hyphenated name matches literally. No matches is 200 with an empty page and `total: 0`, not 404. An index error is 500, not 404. (#10)
- `/workflow_instances/get` accepts only a `workflow_instance_id`; it never reads an arbitrary Redis key. **[DECISION D7]**

## 10. Concurrency guarantees, in one place

1. Same task, concurrent reports: one winner, others 409 (T4).
2. Same task, report vs reaper: one winner (TO4).
3. Sibling tasks, concurrent terminal-causing transitions: exactly one moves the instance to terminal and archives; the other is 409 `INSTANCE_TERMINAL` (I3, I4).
4. Archive row state always equals final Redis state (A1). It cannot say COMPLETED for a FAILED instance.
5. Nothing above depends on request ordering or on the reaper interval.

## 11. Out of scope for v1

Authentication and CORS policy (phase 8). Metrics and structured logs (phase 9). Eviction of archived documents from Redis. Multiple Sagawise replicas sharing one Redis (the atomicity rules make it possible; it is not yet tested). Changing the DSL without a rebuild.

---

## Decisions needing sign-off

| # | Decision | Alternative considered |
|---|---|---|
| D1 | One failure per instance. Siblings freeze, PUBLISHED siblings lose their deadline, no second webhook. | Also fail PUBLISHED siblings and webhook each of their publishers. More compensation signals, but every publisher already has the instance ID and can query state. |
| D2 | `is_retry=true` `publish` on a PUBLISHED task re-arms the deadline and replaces the payload. | Pure no-op. Rejected: a re-sent message gives the consumer a fresh copy, so it deserves a fresh window. |
| D3 | Webhook delivery is at-least-once with timeout and bounded retries. | Fire-and-forget (today). Rejected: a single dropped webhook means a saga is never compensated. |
| D4 | 409 instead of 403 for state-machine refusals; 404 instead of 400 for unknown instance. | Keep today's codes. Rejected: the SDKs do not branch on codes, so the cost is nil and the meaning is right. |
| D5 | Infrastructure errors are 500, never a business answer. | None. |
| D6 | All responses JSON with stable error codes. | Keep plain text. Rejected: clients cannot act on prose. |
| D7 | `/workflow_instances/get` takes an ID, not a raw Redis key. | Keep `doc_key`. Rejected: it exposes every key on a shared Redis. |

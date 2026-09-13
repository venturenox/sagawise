# Sagawise runbook

**Scope:** what to look at and what to do when Sagawise misbehaves. The alerts in `deploy/alerts.yml` (and the chart's `PrometheusRule`) each point at a section here. Every variable named below is defined in [configuration](configuration.md); every rule cited by letter and number (D5, A2) is in the [contract](contract.md).

## What runs where

One process, three loops, two stores.

| Piece | Lives in | If it stops |
|---|---|---|
| HTTP API (`/start_instance`, `/update_instance`, reads) | the process | services cannot report; their own transport keeps working |
| Reaper (fails PUBLISHED tasks past their deadline, every 1 s) | the process | timeouts are not enforced; nothing is lost, deadlines wait in Redis |
| Archive worker (`archive_pending` → Postgres `instance_history`) | the process | terminal instances queue in Redis |
| Webhook worker (`webhook_pending` → each publisher's `failure_url`) | the process | services are not told to compensate; jobs wait in Redis |
| Instance documents, `task_deadlines`, both queues | **Redis** (RedisJSON + RediSearch) | everything stops; see *Redis is down* |
| `instance_history` archive | **Postgres** | the API keeps serving; see *Postgres is down* |

Redis is the live store and the only thing that must not lose data: an armed deadline or a queued webhook that vanishes is a saga that never gets compensated. That is why the binary refuses to start against a Redis without AOF (below).

## Signals

### Probes (no API key)

| Path | Answers | 503 when |
|---|---|---|
| `/live` | the reaper, archive worker and webhook worker have each ticked within 30 s | a loop is dead or stalled; the orchestrator should restart the process |
| `/ready`, `/health` | `/live` plus a Redis ping and a Postgres ping | Redis unreachable, or `/live` fails. **Postgres down is `degraded`, still 200**: the API works and the archive queue catches up later |

Body: `{"status":"ok|degraded|unavailable","checks":{"reaper":"ok","archive_worker":"ok","webhook_worker":"ok","redis":"ok","postgres":"error"}}`. Check values are `ok`, `error`, `stalled`, `not_running`. The body carries no error text (it is unauthenticated); the reason is in the log.

### Metrics

Prometheus text on `SAGAWISE_METRICS_ADDR` (default `:9464`, path `/metrics`; `off` disables). No API key, so keep the port off the ingress: in Kubernetes it is a second container port with an optional `ServiceMonitor` and a `NetworkPolicy` entry (`networkPolicy.metricsIngress`).

| Series | Meaning | Look when |
|---|---|---|
| `sagawise_reaper_last_tick_seconds` | unix time of the last completed reaper tick | `time() - it > 60` means the reaper is stuck |
| `sagawise_reaper_overdue_seconds` | age of the oldest deadline not yet failed (0 when nothing is overdue) | grows when the reaper is stalled or behind |
| `sagawise_reaper_lag_seconds` (histogram) | how late each timeout actually fired | p99 tells how late compensation starts |
| `sagawise_reaper_ticks_total{result}` | ticks, `ok` or `error` | errors are Redis errors; nothing is lost |
| `sagawise_tasks_timed_out_total` | tasks failed by the reaper | business rate of timeouts |
| `sagawise_reaper_deadlines_dropped_total` | deadlines whose instance was gone | should stay 0; nonzero means someone deleted documents |
| `sagawise_deadlines_pending` | armed deadlines (tasks PUBLISHED, not yet consumed) | a plateau under load is normal; unbounded growth is consumers not reporting |
| `sagawise_queue_pending{queue}` | jobs waiting or leased in `archive` / `webhook` | backlog |
| `sagawise_queue_jobs_total{queue,result}` | `done`, `failed` (will retry), `gave_up` (webhook only, after 8 attempts), `dropped` (unresolvable) | `failed` on `archive` is Postgres; `gave_up` on `webhook` is a service that never answered |
| `sagawise_reports_total{action,result}` | `/update_instance` outcomes by contract code | a spike of `TASK_NOT_FOUND` or `INSTANCE_TERMINAL` is a misbehaving client |
| `sagawise_instances_started_total`, `sagawise_instances_terminal_total{state}` | sagas started; sagas ended by state | |
| `sagawise_store_up{store}` | 1 when the ping at scrape time answered | |
| `sagawise_redis_appendonly` | 1 when Redis reports `appendonly yes`; absent when CONFIG is not permitted | must be 1 |
| `http_server_request_duration_seconds{http_request_method,http_response_status_code}` | request latency by method and status (otelhttp) | |
| `db_client_*`, `go_*`, `process_*` | Redis client pool (redisotel), Go runtime, process | |

### Logs

One JSON object per line on stdout (`SAGAWISE_LOG_FORMAT=text` for a terminal). Level from `SAGAWISE_LOG_LEVEL`; access lines are `INFO` for 2xx, `WARN` for 4xx, `ERROR` for 5xx, so `warn` keeps every problem and drops the traffic.

Keys to filter on:

- `instance_id`: on every line about a workflow instance, wherever it was logged from (request, reaper, webhook, archive).
- `request_id`: on every line inside one HTTP request. Generated, or taken from the caller's `X-Request-Id`; echoed back in the response header either way.
- `component`: `reaper` or `worker` (with `queue`), for lines from the loops.
- `msg`: stable strings. The ones an operator will search for:

| `msg` | Level | When |
|---|---|---|
| `request` | info/warn/error | every HTTP request: `method`, `path`, `status`, `duration_ms`, plus `action_type`, `event_name`, `service_name`, `workflow_name` when present |
| `request refused` | info | a 4xx: `code`, `message` |
| `report applied` | info | a transition: `action`, `task_index`, `task_state`, `workflow_state` |
| `instance started` | info | |
| `task timed out` | info | reaper failed a task: `task_index`, `lag_ms` |
| `reaper tick` | info | a tick that failed at least one task: counts |
| `reaper tick failed; will retry` | error | Redis error; deadlines wait |
| `webhook delivered` | info | `service`, `consumer`, `status` |
| `job failed; will retry` | warn | archive or webhook attempt failed: `attempt`, `retry_in`, `err` |
| `giving up on job` | error | webhook dropped after 8 attempts: **this instance was never compensated** |
| `instance archived` | info | |
| `readiness: … ping failed` | warn | why `/ready` is not `ok` |

Trace one saga: filter on its `instance_id`. Trace one call: filter on the `request_id` the client got back.

## When something is wrong

### Redis is down (`SagawiseStoreDown{store="redis"}`, `/ready` 503, 500s on every report)

What happens: every report and read answers `500 INTERNAL`; the reaper logs `reaper tick failed; will retry` once a second; the workers log claim failures. Nothing is decided while Redis is away, so nothing is lost or half-done. Clients should retry (contract D5). The process keeps running and `/live` stays 200, so Kubernetes does not restart it.

Do:

1. Restore Redis. Nothing in Sagawise needs a restart; the next tick picks up where it left off.
2. Confirm it came back with its data: `redis-cli ZCARD task_deadlines` should not be 0 if sagas were in flight, and `redis-cli FT.INFO workflows_index` must answer. If the data is gone the AOF was off or the volume was lost: see *Redis lost its data*.
3. Expect a burst: every deadline that passed during the outage fires on the first ticks (up to 1000 per tick), each queueing a webhook. Watch `sagawise_queue_pending{queue="webhook"}` drain.
4. The examples' and SDKs' reports that failed during the outage were retried by the callers or not; a task whose `publish` never reached Sagawise stays PENDING and will be refused with `TASK_NOT_PUBLISHED` on `consume`. That is the reporting service's retry to make.

### Redis lost its data (`FT.INFO` unknown index, empty `task_deadlines` after a restart)

Sagawise recreates the templates and indexes on its next start, but instance documents, deadlines and queued jobs are gone: every saga that was in flight has vanished, uncompensated. There is no recovery from inside Sagawise; the reporting services must reconcile from their own records.

Prevent it: AOF must be on and on a persistent volume. The binary checks `CONFIG GET appendonly` at startup and exits with `redis has appendonly no` unless `SAGAWISE_REDIS_AOF=warn`. `docker-compose.yml` sets `REDIS_ARGS=--appendonly yes --appendfsync everysec` with a `redis_data` volume; the chart's Redis subchart sets `appendonly yes` and a PVC. A managed Redis that refuses `CONFIG` logs `redis persistence unknown` and `sagawise_redis_appendonly` is absent; check it on the provider's side.

### Postgres is down (`SagawiseStoreDown{store="postgres"}`, `SagawiseArchiveFailing`, `/ready` says `degraded`)

What happens: the API keeps serving and every transition still succeeds (a terminal transition never waits on Postgres, contract A3). Terminal instances stay in `archive_pending` and the archive worker retries each one with backoff (1 s doubling, capped at 30 s) forever; `sagawise_queue_jobs_total{queue="archive",result="failed"}` climbs and `sagawise_queue_pending{queue="archive"}` grows by one per finished saga. `/ready` stays 200 so the pods are not pulled from the Service.

Do:

1. Restore Postgres. No restart needed.
2. Watch `sagawise_queue_pending{queue="archive"}` drain to 0; each row is logged as `instance archived`. Inserts are `ON CONFLICT DO NOTHING`, so a retry after a half-committed insert is harmless (A2).
3. If the outage is long, mind Redis memory: terminal documents are kept in Redis anyway (A4), so the backlog costs one sorted-set member per instance, which is small.

### Reaper stalled (`SagawiseReaperStalled`, `/live` 503 with `"reaper":"stalled"`)

The reaper loop has not completed a tick in 60 s. A Redis outage does *not* cause this (a failed tick still beats); this is the process itself wedged. `/live` fails at 30 s, so Kubernetes restarts the pod before the alert fires; under compose, `restart: always` does the same once the healthcheck fails. If it keeps happening, take a goroutine dump: set `SAGAWISE_PPROF_ADDR` on one replica and fetch `/debug/pprof/goroutine?debug=2`.

### Reaper behind (`SagawiseReaperBehind`, `sagawise_reaper_overdue_seconds` growing)

The reaper runs but the oldest overdue deadline keeps getting older. Causes, in order of likelihood:

1. Ticks are erroring: `sagawise_reaper_ticks_total{result="error"}` rising, log `reaper tick failed`. Fix Redis.
2. More than 1000 deadlines expire per second for a sustained period: each tick reaps at most 1000, so the backlog drains at that rate. The lag histogram shows it. This is a capacity signal; the [benchmark](benchmarks.md) method measures the knee.
3. Several replicas do not help here: every replica's reaper works on the same `task_deadlines`, and the batch script is atomic, so they do share the load, but Redis is the bottleneck.

### A webhook was given up (`SagawiseWebhookGaveUp`, log `giving up on job`)

The publishing service's `failure_url` did not answer 2xx in 8 attempts over about 15 minutes (2 s tripling, capped at 5 min). The task is FAILED and the instance is FAILED and archived; only the notification was lost, so **the service has not compensated**.

Do:

1. Find the instances: `msg="giving up on job"` lines carry `instance_id` and `task_index`; `sagawise_queue_jobs_total{queue="webhook",result="gave_up"}` counts them.
2. Fix the receiver (the URL in `services.json`, its availability, its signature verification if `SAGAWISE_WEBHOOK_SECRET` was rotated).
3. Replay by hand: `GET /workflow_instances/get?workflow_instance_id=<id>` returns the document; `tasks[<task_index>].payload` is what the webhook would have carried, `tasks[<task_index>].from` is the service. POST it to that service's `failure_url` with `?service=<tasks[i].to>`, or trigger the compensation on the service's side directly.

There is no re-enqueue endpoint in v1; `ZADD webhook_pending <now_ms> <id>:<index>` on Redis re-queues a job if you would rather Sagawise deliver it (the attempt counter under `webhook_attempts` is cleared on give-up, so it starts fresh).

### Archive backlog (`SagawiseArchiveBacklog`)

`archive_pending` above 1000 for 15 minutes with Postgres up. Either the archive worker is stalled (`/live` would say so) or Postgres accepts connections but the inserts are slow or failing: look for `job failed; will retry` with `queue=archive` and read `err`. A full disk or a locked table shows up here.

### Clients get 4xx they do not expect

`sagawise_reports_total{result}` by code, and the `request refused` lines with `instance_id`, `action_type`, `event_name`, `service_name`. The codes are in the [contract](contract.md), §9. `TASK_NOT_FOUND` on consume means `(event_name, service_name)` does not match any task's `(topic, to)` in the DSL; `INSTANCE_TERMINAL` means a late report after the reaper already failed the saga (look for the `task timed out` line for that instance).

### Everything is slow

`http_server_request_duration_seconds` by status; `db_client_*` for the Redis pool. The engine does one `JSON.GET` and one script call per report. If Redis latency is fine and the API is slow, check the Go runtime series (`go_goroutines`, GC pause) and the pod's CPU limit. Method and known numbers: [benchmarks](benchmarks.md).

## Restarts and upgrades

- SIGTERM: the server drains in-flight requests (10 s), then the reaper and the workers stop; a job in progress finishes. Anything still queued is leased in Redis and resumes on the next start, so a rolling restart loses nothing.
- Schema: the RediSearch index schemas are hashed; a version that changes one recreates the index on startup (documents are kept). The Postgres table is `CREATE IF NOT EXISTS`.
- The DSL and `services.json` are baked into the image: changing either is a rebuild and a rolling restart. A DSL change does not migrate running instances (they carry their own task list).
- Several replicas are safe: every transition is one atomic Redis script, queue jobs are leased, and the reaper batch is atomic.

## Configuration

Every variable, with defaults and what fails at start-up, is in [configuration.md](configuration.md).

Alert rules: `deploy/alerts.yml`; the Helm chart renders the same rules with `metrics.prometheusRule.enabled=true` and a scrape target with `metrics.serviceMonitor.enabled=true`.

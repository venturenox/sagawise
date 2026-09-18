# HTTP API

Read this to call Sagawise directly, or to know exactly what the SDKs send. Behaviour rules (which transitions are allowed, what `is_retry` does, timing guarantees) are in the [contract](contract.md); this page is the wire format.

## Conventions

- Base URL: `http://<host>:5000` (`SAGAWISE_ADDR`). The method is not checked; the SDKs use POST for reports and GET for reads.
- **All parameters are URL query strings.** The only request body is the publish payload.
- **Every response is JSON.** Errors are `{"error": "<CODE>", "message": "<text>"}` with a stable code (table at the end).
- **Authentication:** `Authorization: Bearer <key>` on every request except the probes, with a key from `SAGAWISE_API_KEYS`. Missing or wrong: 401 `UNAUTHORIZED`.
- **Request id:** every response carries `X-Request-Id`, the caller's if sent, otherwise generated. Every log line the request produced has the same `request_id`.
- Request bodies above `SAGAWISE_MAX_BODY_BYTES` (default 1 MiB) get 413 `PAYLOAD_TOO_LARGE` and change nothing.

The examples assume `export SAGAWISE_URL=http://localhost:5000 KEY=dev-api-key-change-me`, the development defaults.

## Reports

### `POST /start_instance`

Creates one instance of a workflow. Every task starts PENDING.

| Parameter | Required | Meaning |
| --- | --- | --- |
| `workflow_name` | yes | Name of a workflow loaded at start-up. |
| `workflow_version` | no | Accepted and ignored; the SDKs send it. |

```bash
curl -s -X POST -H "Authorization: Bearer $KEY" "$SAGAWISE_URL/start_instance?workflow_name=order_flow"
```

```json
{"workflow_instance_id": "k3Jd9sLq2mZp8xWv4bNc"}
```

404 `WORKFLOW_NOT_FOUND` for an unknown name. The id is 20 letters and digits; put it inside the real message so the consumer can report against it.

### `POST /update_instance`

Reports that something happened on the real transport.

| Parameter | Required | Meaning |
| --- | --- | --- |
| `workflow_instance_id` | yes | From `/start_instance`. |
| `action_type` | yes | `publish`, `consume` or `fail`. |
| `event_name` | yes | The task's topic. |
| `service_name` | consume, fail | The reporting (consuming) service. Selects the task by `(topic, to)`. |
| `is_retry` | yes | `true` or `false`, case-insensitive. Anything else is 400 `INVALID_PARAM`. |

`publish` sends the message payload as the request body: a JSON object, `Content-Type: application/json`. It is stored on the task and replayed in the failure webhook. A `publish` selects every task with that topic, so one report can start several tasks.

```bash
curl -s -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"order_id": 42}' \
  "$SAGAWISE_URL/update_instance?workflow_instance_id=$ID&action_type=publish&event_name=order_created&is_retry=false"
```

```bash
curl -s -X POST -H "Authorization: Bearer $KEY" \
  "$SAGAWISE_URL/update_instance?workflow_instance_id=$ID&action_type=consume&event_name=order_created&service_name=payments&is_retry=false"
```

Success:

```json
{"workflow_instance_id": "k3Jd9sLq2mZp8xWv4bNc", "task_index": 0, "task_state": "COMPLETED", "workflow_state": "PENDING", "idempotent": false}
```

`idempotent` is `true` when an `is_retry=true` report repeated a transition that had already happened. When a `publish` selects several tasks, `task_index` and `task_state` are arrays.

| Status | When |
| --- | --- |
| 200 | Applied, or an accepted duplicate. |
| 400 | Missing parameter, bad `action_type` or `is_retry`, body not a JSON object. |
| 404 | Unknown instance, or no task matches `event_name` (and `service_name`). |
| 409 | The state machine refuses it: `TASK_NOT_PUBLISHED`, `TASK_ALREADY_PUBLISHED`, `TASK_ALREADY_COMPLETED`, `TASK_ALREADY_FAILED`, `INSTANCE_TERMINAL`. The instance is unchanged. |
| 500 | Redis failed. Nothing changed; retry. |

## Reads

### `GET /workflows/list`

Names of the loaded workflows, as a JSON array of strings.

### `GET /workflow_instances/list`

A page of instance ids matching the filters.

| Parameter | Meaning |
| --- | --- |
| `workflow_name`, `workflow_state`, `topic`, `from`, `to` | Exact match. `workflow_state` is `PENDING`, `COMPLETED` or `FAILED`; `topic`, `from`, `to` match any task. |
| `started_at`, `completed_at`, `failed_at` | `5m` or `15m`: within the last 5 or 15 minutes. Other values are ignored. |
| `limit` | Page size, default 50, maximum 1000. |
| `offset` | Skip this many matches, default 0. |

```bash
curl -s -H "Authorization: Bearer $KEY" "$SAGAWISE_URL/workflow_instances/list?workflow_name=order_flow&workflow_state=FAILED&limit=10"
```

```json
{"ids": ["k3Jd9sLq2mZp8xWv4bNc"], "total": 1, "limit": 10, "offset": 0}
```

No match is 200 with an empty `ids` and `total: 0`. A bad `limit` or `offset` is 400 `INVALID_PARAM`. Filter values are matched literally; a hyphen or a space in a name is not query syntax.

### `GET /workflow_instances/get`

The full instance document, whose layout is in the [architecture](architecture.md#data).

| Parameter | Meaning |
| --- | --- |
| `workflow_instance_id` | 1 to 64 letters or digits. Anything else is 400; unknown is 404 `INSTANCE_NOT_FOUND`. |

```bash
curl -s -H "Authorization: Bearer $KEY" "$SAGAWISE_URL/workflow_instances/get?workflow_instance_id=$ID"
```

## Probes

No API key. Bodies carry check names and states only, never error text.

| Path | 200 when | 503 when |
| --- | --- | --- |
| `GET /live` | The reaper and both queue workers have ticked within 30 s. | A loop is stalled or not running. Never touches a store. |
| `GET /ready`, `GET /health` | `/live` passes and Redis answers. Postgres down is `"status": "degraded"`, still 200. | Redis unreachable, or `/live` fails. |

```json
{"status": "ok", "checks": {"reaper": "ok", "archive_worker": "ok", "webhook_worker": "ok", "redis": "ok", "postgres": "ok"}}
```

`GET /ping` answers a plain-text banner and needs a key; it predates the probes and is kept for compatibility. Metrics are not on this port: see `SAGAWISE_METRICS_ADDR` in [configuration](configuration.md).

## The failure webhook

Sagawise makes one outbound call: when a task becomes FAILED, a `POST` to the `failure_url` registered for the task's `from` service, with query `service=<to>` (the service that went silent or failed), `Content-Type: application/json`, and the task's stored publish payload as the body. When `SAGAWISE_WEBHOOK_SECRET` is set it carries `X-Sagawise-Timestamp` (unix seconds) and `X-Sagawise-Signature: v1=<hex HMAC-SHA256(secret, "<timestamp>.<body>")>`; verify against the raw body and reject timestamps more than 5 minutes off. Delivery is retried for about 15 minutes and is at-least-once, so receivers must tolerate duplicates. Answer 2xx to acknowledge. The SDKs ship `verify_signature`.

## Error codes

| Code | Status | Meaning |
| --- | --- | --- |
| `MISSING_PARAM`, `INVALID_PARAM`, `INVALID_BODY` | 400 | The request is malformed. |
| `UNAUTHORIZED` | 401 | No valid API key. |
| `WORKFLOW_NOT_FOUND`, `INSTANCE_NOT_FOUND`, `TASK_NOT_FOUND` | 404 | The thing named does not exist. |
| `TASK_NOT_PUBLISHED`, `TASK_ALREADY_PUBLISHED`, `TASK_ALREADY_COMPLETED`, `TASK_ALREADY_FAILED`, `INSTANCE_TERMINAL` | 409 | Well formed, but the state machine forbids it. |
| `PAYLOAD_TOO_LARGE` | 413 | Body over the cap. |
| `INTERNAL` | 500 | Sagawise's own infrastructure failed. Retry. |

## SDKs

`sdk/nodejs` and `sdk/python` wrap the calls above, read `SAGAWISE_URL` and `SAGAWISE_API_KEY` from the environment, and pass response bodies through unchanged. Both include `verify_signature` for webhook receivers.

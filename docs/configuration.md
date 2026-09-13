# Configuration

Every setting the binary reads, in one place. Read this when deploying, or when a start-up log line names a variable. Settings are environment variables only; there is no config file. A bad value makes the process log the reason and exit non-zero before it serves anything, so a broken configuration never runs half-initialised.

## Stores

| Variable | Default | Meaning |
| --- | --- | --- |
| `REDIS_CONNECTION_STRING` | none | Redis URL, for example `redis://redis:6379`. When set, `REDIS_HOST` and `REDIS_PORT` are ignored. Only the address is taken from it: the password always comes from `REDIS_PASSWORD`. |
| `REDIS_HOST`, `REDIS_PORT` | none | Redis address when no connection string is set. |
| `REDIS_PASSWORD` | empty | Redis password. Empty means none. |
| `POSTGRES_HOST`, `POSTGRES_PORT` | none | Postgres address. Start-up waits up to 30 s for it to answer. |
| `POSTGRES_USERNAME`, `POSTGRES_PASSWORD` | none | Postgres credentials. |
| `POSTGRES_DATABASE` | none | Database holding the `instance_history` archive table, created if missing. |

Redis must be Redis Stack (RedisJSON and RediSearch) with append-only persistence on; see `SAGAWISE_REDIS_AOF` below and the [runbook](operations.md).

## Security

Reasoning for each is in the [threat model](security.md).

| Variable | Default | Meaning |
| --- | --- | --- |
| `SAGAWISE_AUTH` | `api-key` | `api-key` requires a bearer key on every request except the probes. `off` serves an unauthenticated API, for development only. Anything else exits. |
| `SAGAWISE_API_KEYS` | none | Comma-separated keys clients send as `Authorization: Bearer <key>`. Empty with `SAGAWISE_AUTH=api-key` exits. |
| `SAGAWISE_WEBHOOK_SECRET` | empty | HMAC secret for signing failure webhooks. Empty means unsigned deliveries, with a warning at start-up. |
| `SAGAWISE_CORS_ORIGINS` | empty | Comma-separated exact browser origins allowed to call the API. Empty means none. `*` exits. |
| `SAGAWISE_MAX_BODY_BYTES` | `1M` | Largest accepted request body, so the largest `publish` payload. Integer with optional `K` or `M` suffix. Larger bodies get 413 and change nothing. |

## Operations

Meaning of the signals is in the [runbook](operations.md).

| Variable | Default | Meaning |
| --- | --- | --- |
| `SAGAWISE_ADDR` | `:5000` | API listen address. |
| `SAGAWISE_METRICS_ADDR` | `:9464` | Prometheus `/metrics` listen address, never the API port. `off` disables it. Keep it internal. |
| `SAGAWISE_LOG_FORMAT` | `json` | `json` (one object per line) or `text`. |
| `SAGAWISE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Probe requests are logged at debug. |
| `SAGAWISE_REDIS_AOF` | `require` | Start-up check of Redis `appendonly`. `require` exits when it is off, `warn` logs, `off` skips the check. A Redis that forbids `CONFIG GET` cannot be checked and gets a warning under every mode. |
| `SAGAWISE_PPROF_ADDR` | unset | When set, serves Go profiling on that address. For benchmarks only; never set it in production. |
| `OTEL_SERVICE_NAME` | `sagawise` | Service name on exported metrics. Standard OpenTelemetry variables such as `OTEL_RESOURCE_ATTRIBUTES` are honoured too. |

## Workflow files

| Variable | Default | Meaning |
| --- | --- | --- |
| `SAGAWISE_DSL_DIR` | `/sagawise` | Directory of workflow JSON files. Must exist and hold at least one valid file. |
| `SAGAWISE_SERVICES_FILE` | `services.json` | Service registry: `service_name` to `failure_url`. Every publishing service in every workflow must have an entry. |

Both are copied into the container image at build time, so changing a workflow or a failure URL is a rebuild and a rolling restart, not a restart. Running instances keep the task list they were started with.

## Start-up checks

The process exits non-zero, in this order, when:

1. a logging, auth, CORS, body-size or AOF setting has an unknown value;
2. `SAGAWISE_AUTH` is `api-key` and there is no key;
3. Redis does not answer, or Postgres does not answer within 30 s;
4. the workflow directory is missing or empty, a file does not parse, or a workflow breaks a validation rule of the [contract](contract.md#7-startup-and-dsl-validation);
5. a workflow's publishing service has no `failure_url`;
6. the Redis scripts do not load;
7. Redis reports `appendonly no` under `SAGAWISE_REDIS_AOF=require`.

Warnings, not exits: `SAGAWISE_AUTH=off`, an empty webhook secret, an unknown AOF state.

## Where the values are set

- **Docker Compose**: `.env` at the repository root, read by `docker-compose.yml`. The committed values are development defaults; every deployment sets its own keys and secrets.
- **Helm**: the chart's `values.yaml`. `auth.mode`, `auth.apiKeys` or `auth.existingSecret`, `webhook.secret`, `cors.origins`, `maxBodyBytes`, `logging.format`, `logging.level`, `metrics.enabled` and `metrics.port`, and `redisAOF` map one to one onto the variables above. Store addresses come from the bundled Redis and Postgres sub-charts or from `externalRedis` and `externalPostgresql`. See the chart's README.
- **Clients**: the SDKs read `SAGAWISE_URL` and `SAGAWISE_API_KEY`; the Node SDK also reads `SAGAWISE_TIMEOUT_MS`.

# Sagawise threat model

**Status:** v1, 2026-09-05. Roadmap phase 8 (`docs/TODO.md`). One page: who can call what, what an attacker gains, and which control answers each threat. Every control below is implemented and tested; the "not covered" list at the end is deliberate.

## What Sagawise is, for this purpose

A bookkeeper with an HTTP API. It holds, for every running saga, the state of each task and the **publish payload** the reporting service handed it. It calls out to one place: the `failure_url` of a publishing service, with that payload, when a task fails. It never touches the services' own transport.

Assets, in order of value:

1. **Correctness of the ledger.** A forged report can mark a task consumed that never was (silencing a timeout), fail a healthy saga (triggering compensation), or start sagas until Redis is full.
2. **Payloads.** Whatever the services put in a publish body: order ids, user ids, sometimes more. Readable through `/workflow_instances/get` and `/list`, and replayed to the webhook.
3. **The compensation webhook.** A receiver that trusts any POST to its `failure_url` can be made to undo real work.
4. **Availability.** The reaper and the queues share one Redis; a flooded API or a huge payload hurts every saga.

## Who calls what

| Caller | Endpoints | Trust |
|---|---|---|
| Reporting services (via the SDKs or plain HTTP) | `/start_instance`, `/update_instance` | Trusted to report the truth about their own topics. Must present an API key. |
| Operators, dashboards | `/workflows/list`, `/workflow_instances/list`, `/workflow_instances/get` | Read payloads. Must present an API key. A browser UI must also be on the CORS allowlist. |
| Kubernetes / Docker | `/live`, `/ready`, `/health` | No key. They reveal only "up or not". |
| Sagawise itself → services | POST to each service's `failure_url` | The service must be able to tell a real delivery from a forged one. |
| Redis, Postgres | internal | Reached over the network with credentials from Secrets. Not exposed by the API. |
| Benchmark harness | `/debug/pprof` on `SAGAWISE_PPROF_ADDR` | Opt-in, never set in production. |

There is one role. Every key can do everything; per-service keys that may report only their own topics are listed under "not covered".

## Threats and controls

| # | Threat | Control | Where it lives | Test |
|---|---|---|---|---|
| T1 | **Anyone who can reach the port can report or read.** The API was open. | Bearer API keys, required on every endpoint except the probes. Constant-time comparison against SHA-256 digests. The process refuses to start with no key unless `SAGAWISE_AUTH=off` is set explicitly, and then logs a warning. | `backend/httpsec` `APIKeys`; `main.go` `loadSecurityConfig` | `httpsec_test.go`, `startup_test.go` `TestStartup_NoAPIKeyExits`, `_APIKeyRequired`, `_AuthOffServesOpen` |
| T2 | **Key guessing.** | Keys are compared in constant time; a wrong key gets a 401 that names nothing. No lockout: keys are long random strings, not passwords. | `httpsec.APIKeys.Allowed` | `TestAPIKeys` (prefix and suffix cases) |
| T3 | **A malicious web page in an operator's browser calls the API** with the operator's ambient credentials. | The old wildcard CORS with `Allow-Credentials: true` is gone. `SAGAWISE_CORS_ORIGINS` is an exact allowlist; unlisted origins get no CORS headers and a refused preflight; credentials are never allowed; `*` is refused at startup. Bearer tokens are not ambient, so even a listed origin needs the key. | `httpsec.CORS` | `TestCORS`, `TestStartup_WildcardCORSExits` |
| T4 | **Forged compensation.** Anything that can reach a service's `failure_url` can POST a payload and trigger a rollback. | Every delivery carries `X-Sagawise-Timestamp` and `X-Sagawise-Signature: v1=<HMAC-SHA256(secret, "ts.body")>`. Receivers verify against the raw body and reject timestamps older than 5 min (replay). The SDKs ship `verify_signature`; the three implementations share one test vector. Unset secret means unsigned deliveries and a startup warning. | `backend/webhooksig`; `Engine.WebhookSecret`; `webhookJob` | `webhooksig_test.go`, `TestContract_WebhookIsSigned`, `_WebhookUnsignedWithoutSecret`, SDK tests |
| T5 | **Resource exhaustion by payload.** A publish body is stored in Redis and replayed in the webhook; an unbounded body is a memory and storage exposure. | Request bodies are capped (`SAGAWISE_MAX_BODY_BYTES`, default 1 MiB); over the cap is 413 `PAYLOAD_TOO_LARGE`, nothing stored, the task stays PENDING. Header and body read timeouts were already in place. | `httpsec.MaxBody`; `UpdateInstance` | `TestMaxBody`, `TestContract_PublishBodyCap` |
| T6 | **Lateral movement from a compromised pod; credentials in plain env.** | Image runs as uid 65532 with no shell; the chart defaults to the restricted Pod Security Standard (non-root, no privilege escalation, all capabilities dropped, read-only root filesystem, RuntimeDefault seccomp). API keys, webhook secret and external-store passwords come from a Secret (chart-rendered or `existingSecret`); the Redis connection string no longer embeds the password. Optional default-deny ingress NetworkPolicy. | `backend/Dockerfile`; `charts/sagawise` `values.yaml`, `secret.yaml`, `networkpolicy.yaml` | `helm lint`/`template` in review; the Docker CI job builds the image |
| T7 | **Known-vulnerable dependencies.** | `govulncheck` and `gosec` already run on every PR. Dependabot now opens weekly grouped PRs for Go modules, npm, pip, the Docker base image and the GitHub Actions. | `.github/dependabot.yml`, `.github/workflows/ci.yml` | CI |
| T8 | **Reading arbitrary Redis keys through the API.** | Already closed by contract D7: `/workflow_instances/get` accepts only an instance id and builds the key itself. | `GetWorkflowInstance` | contract tests |

## Configuration summary

| Variable | Default | Meaning |
|---|---|---|
| `SAGAWISE_AUTH` | `api-key` | `off` serves an open API (development only; warned). |
| `SAGAWISE_API_KEYS` | required | Comma-separated bearer tokens. Rotate by listing old and new, rolling the clients, dropping the old. |
| `SAGAWISE_CORS_ORIGINS` | empty (none) | Exact origins allowed to call from a browser. |
| `SAGAWISE_WEBHOOK_SECRET` | empty (unsigned) | HMAC secret for failure webhooks. |
| `SAGAWISE_MAX_BODY_BYTES` | `1M` | Cap on a request body. |

Clients: `Authorization: Bearer <key>` on every request; the SDKs read `SAGAWISE_API_KEY`. Receivers: verify with `verify_signature` (SDKs) or `webhooksig.Verify` (Go) using the raw body.

## Not covered in v1, on purpose

- **Per-service authorization.** Every key can report on every topic. Binding a key to the services it may speak for needs the registry refactor that is already on the roadmap (services.json is a build-time file). Until then, one key per deployment, or one per service purely for rotation and audit.
- **TLS.** Sagawise speaks plain HTTP. Terminate TLS at the ingress (the chart's ingress has a TLS block) or a mesh. Keys sent over plain HTTP on an untrusted network are readable.
- **Rate limiting.** A key holder can start sagas until Redis is full. Bound it at the ingress, or add a per-key limiter in phase 9 when metrics exist to size it.
- **Audit log.** Which key did what is not recorded; phase 9 structured logging is where it belongs.
- **Redis and Postgres hardening** (AUTH, TLS, network isolation) beyond passing the password through a Secret: the operator's stores, the chart's subcharts, and phase 9 (AOF persistence).
- **Secrets at rest** in `.env` and the example `.env` files: dev defaults, labelled as such.

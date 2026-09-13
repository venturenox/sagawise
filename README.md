# Sagawise

<p align="center"><img src="docs/assets/sagawise-platform-logo.png" alt="Sagawise" width="360"></p>

Sagawise keeps score for saga-pattern distributed transactions. Your services exchange their real messages over their own transport, Kafka or anything else, and separately report `publish`, `consume` and `fail` events to Sagawise over HTTP. Sagawise checks the reports against a declared workflow. Sagawise itself decides only one thing: that a message was never picked up. When a service reports that it published a task, a timer starts. If the receiving service does not report a matching `consume` before the task's timeout, Sagawise marks the task FAILED and calls the publisher's `failure_url`, so the publisher can undo its side of the work.

It is a **bookkeeper, not a broker**. It never moves a message. Go, Redis Stack for live state, Postgres for the archive.

## Documentation

The full documentation is a site built from [`docs/`](docs/) and published at **https://venturenox.github.io/sagawise/**. Start with [Your first saga](docs/first-saga.md) to learn to use it, [Concepts](docs/concepts.md) if sagas are new to you, [Architecture](docs/architecture.md) if the code is.

| Page | For |
| --- | --- |
| [Your first saga](docs/first-saga.md) | Write a workflow, register a failure callback, run a saga, break it, wire it into your services. Twenty minutes. |
| [Concepts](docs/concepts.md) | The problem, the vocabulary, how the pieces fit. No code. |
| [Architecture](docs/architecture.md) | Every component, where it lives, how one saga moves through them. |
| [Contract](docs/contract.md) | The exact rules: state machine, retries, status codes, timing. Tests follow this, not the code. |
| [HTTP API](docs/api.md) | Endpoints, parameters, responses, errors, with curl. |
| [Configuration](docs/configuration.md) | Every environment variable and what refuses to start. |
| [Operations](docs/operations.md) | Probes, metrics, logs, alerts, and what to do when a store is down. |
| [Security](docs/security.md) | Who may call what; API keys, CORS, signed webhooks. |
| [Development](docs/development.md) | Building, the test tiers, CI, conventions. |
| [Benchmarks](docs/benchmarks.md) | Method, numbers, how performance moved. |

## Five-minute start

Needs Docker with Compose. Development defaults, including the API key, are in [`.env`](.env).

```bash
make start
```

```bash
export URL=http://localhost:5000 KEY=dev-api-key-change-me
ID=$(curl -s -X POST -H "Authorization: Bearer $KEY" "$URL/start_instance?workflow_name=order_flow" | sed 's/.*: *"\([^"]*\)".*/\1/')
curl -s -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{"order_id": 42}' \
  "$URL/update_instance?workflow_instance_id=$ID&action_type=publish&event_name=order_created&is_retry=false"
curl -s -X POST -H "Authorization: Bearer $KEY" \
  "$URL/update_instance?workflow_instance_id=$ID&action_type=consume&event_name=order_created&service_name=payments&is_retry=false"
```

Skip the `consume` and, 20 seconds later, `/workflow_instances/get?workflow_instance_id=$ID` shows the task FAILED and the instance FAILED: the timeout in [`backend/sagawise/order_flow.json`](backend/sagawise/order_flow.json) passed with no consume reported. A running three-service version of the same flow, with the compensation webhook wired up, is `make order_flow`; its [README](examples/order_flow/README.md) is the best short explanation of the protocol.

## Describing a saga

One JSON file per workflow in [`backend/sagawise/`](backend/sagawise/):

```json
{"workflow": {"name": "order_flow", "version": "1.0", "schema_version": "1.0",
  "tasks": [
    {"topic": "order_created", "from": "orders",   "to": "payments", "timeout": 20000},
    {"topic": "payment_done",  "from": "payments", "to": "shipping", "timeout": 20000}
  ]}}
```

A task is one message from `from` to `to` on `topic`, which `to` must report consuming within `timeout` milliseconds. [`services.json`](services.json) maps each service name to the `failure_url` Sagawise calls when a task that service published fails. Both files are baked into the image, so a change is `make restart`. Validation rules are in the [contract](docs/contract.md#7-startup-and-dsl-validation).

## Repository

| Path | Contents |
| --- | --- |
| [`backend/`](backend/) | The Go service. |
| [`sdk/nodejs/`](sdk/nodejs/), [`sdk/python/`](sdk/python/) | Client libraries, with webhook signature verification. |
| [`examples/`](examples/) | `order_flow` (three services, minimal) and `api_examples` (four services with databases). |
| [`charts/sagawise/`](charts/sagawise/) | Helm chart; see its [README](charts/sagawise/README.md). |
| [`deploy/`](deploy/) | Prometheus alert rules for non-Helm deployments. |
| [`benchmarks/`](benchmarks/) | Recorded benchmark runs and comparisons. |
| [`docs/`](docs/) | The documentation site. `make docs` serves it locally. |

Images are published to [Docker Hub](https://hub.docker.com/r/venturenox/sagawise). Development commands are in the [Makefile](Makefile) and explained in [Development](docs/development.md).

## License

Apache 2.0, see [LICENSE.txt](LICENSE.txt).

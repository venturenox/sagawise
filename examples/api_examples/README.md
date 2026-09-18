# api_examples

Four Node services with their own databases, running the `user_creation` workflow. It is the larger of the two examples; read [order_flow](../order_flow/README.md) first for the protocol itself, since this one adds databases, migrations and a fan-out on top.

## The workflow

[`backend/sagawise/user_creation.json`](../../backend/sagawise/user_creation.json) declares four tasks:

| Task | From | To | Topic |
| --- | --- | --- | --- |
| 0 | `auth` | `notification` | `user_created` |
| 1 | `notification` | `final` | `user_created_saga` |
| 2 | `notification` | `intermediate` | `user_created_saga` |
| 3 | `intermediate` | `final` | `user_created_saga_final` |

Tasks 1 and 2 share a topic with different receivers. One `publish` report from `notification` starts both; each is completed by its own receiver's `consume`. That fan-out is the one thing this example shows that `order_flow` does not.

Each service does the same three things through the HTTP API: the first one calls `/start_instance`, every publisher reports `publish` before sending, every consumer reports `consume` after receiving. The publisher puts the `workflow_instance_id` inside the message so the consumer can report against it. Every service exposes the `failure_url` registered for it in [`services.json`](../../services.json), where it compensates when Sagawise reports a timeout.

## Running it

```bash
make api_examples
```

That runs `make clean`, starts the core stack, then builds and starts the four services on the shared network. Their ports and environment are in [`docker-compose.yml`](docker-compose.yml); each service's code is in its own directory (`auth/`, `intermediate/`, `final/`, `notification/`). Trigger a run through the `auth` service's user-creation endpoint and follow it with:

```bash
curl -s -H "Authorization: Bearer dev-api-key-change-me" \
  "http://localhost:5000/workflow_instances/list?workflow_name=user_creation"
```

then `/workflow_instances/get?workflow_instance_id=<id>` for the document. Stop a consumer to watch a task time out and its publisher's `failure_url` get called. Endpoint details are in the [API reference](../../docs/api.md).

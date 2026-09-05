# Order Flow Example

A minimal two-task workflow built directly against the Sagawise HTTP API. It is
deliberately smaller than [api_examples](../api_examples/) -- three services, no
databases, no auth -- so the saga protocol itself is the only thing to read.

```
  orders  ──order_created──▶  payments  ──payment_done──▶  shipping
```

| Task | From | To | Topic |
| ---- | ---- | -- | ----- |
| 0 | orders | payments | `order_created` |
| 1 | payments | shipping | `payment_done` |

Defined in [backend/sagawise/order_flow.json](../../backend/sagawise/order_flow.json).

---

## The one idea worth taking away

There are **two independent planes**, and Sagawise only sits on one of them:

- **Data plane (Kafka)** -- services publish and consume real messages.
  Sagawise is *not* in this path and never sees these messages.
- **Control plane (HTTP)** -- every service *tells* Sagawise what it just did.

Sagawise is a bookkeeper, not a broker. It cannot move your messages or stop
them. It compares what services report against the DSL, and the one thing it
infers on its own is **silence**: if a task is published and nobody reports
consuming it before `timeout`, Sagawise marks it `FAILED` and calls the
publisher's `failure_url` so it can compensate.

Each service does the same three things, and nothing more:

1. `start_instance` -- only the service that begins the workflow
2. `update_instance?action_type=publish` -- before producing to Kafka
3. `update_instance?action_type=consume` -- after receiving from Kafka

All Sagawise calls live in `sagawiseClient.js`, duplicated per service so each
one reads standalone. That file is the only thing to rewrite if the transport
ever changes.

---

## Running it

**Prerequisite:** the root stack (Sagawise, Postgres, Redis) must be up, since
these services call `http://sagawise:5000`. The `order_flow` target starts it
for you.

```bash
make order_flow
```

This example brings its own Kafka broker, so it does not depend on the
`api_examples` stack. Both can run at once.

**Wait ~30 seconds** before sending an order. Kafka consumers need to join their
consumer group, and until they do a message can sit unread long enough to trip
the 20s task timeout. Check readiness with:

```bash
docker logs payments 2>&1 | grep -q "payments listening" && echo ready
```

### Trigger a workflow

```bash
ID=$(curl -s -X POST http://localhost:4010/orders -H 'Content-Type: application/json' -d '{"order_id":300}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["workflow_instance_id"])'); echo "ID=$ID"
```

### Watch it happen

```bash
docker compose logs -f orders payments shipping
```

```
[orders]   started workflow VsUOkmuulX0m230Bb2gc for order 300
[sagawise] publish order_created
[orders]   published order_created
[payments] received order_created
[sagawise] consume order_created        <- task 0 COMPLETED
[sagawise] publish payment_done
[payments] published payment_done
[shipping] received payment_done
[sagawise] consume payment_done         <- task 1 COMPLETED
[shipping] shipped order 300 -- workflow complete
```

### Inspect the workflow state

```bash
curl -s "http://localhost:5000/workflow_instances/get?workflow_instance_id=$ID" | python3 -m json.tool
```

Workflow completion is evaluated **asynchronously**, so immediately after the
last consume you will see both tasks `COMPLETED` while the workflow itself is
still `PENDING`. Wait ~20s and it flips to `COMPLETED`, then gets archived:

```bash
docker exec -e PGPASSWORD=venturenox postgres psql -U postgres -d sagawise -tAc \
  "SELECT id, name, instance_data->>'state' FROM instance_history WHERE id='$ID'"
```

Only completed workflows are written to Postgres. A failed one stays in Redis.

---

## Watching failure

Two different failure modes end in the same state.

**Timeout** -- stop the consumer and send an order. Nobody reports the consume,
so after 20s Sagawise marks the task `FAILED` on its own and POSTs the failure
report to `orders`, which logs it:

```bash
docker compose stop payments
```

**Explicit failure** -- set `FAIL_PAYMENTS=true` on the `payments` service and it
reports `action_type=fail` instead of `consume`. Same end state, but marked
immediately rather than after the timeout.

In both cases the report goes to the task's **`from`** service, not the one that
failed. The publisher is the one holding state that needs undoing -- that is
where a compensating transaction belongs.

---

## Things worth knowing

- **One topic carries every event.** Services filter on the `event` field in the
  payload, which is why each consumer needs its own `KAFKA_GROUP_ID`.
- **The `workflow_instance_id` travels inside the message.** That is what lets a
  downstream service report against a workflow it did not start.
- **Reporting can legitimately fail.** If a task already timed out, Sagawise
  answers `403`. Both consumers wrap their handler in a `try/catch` so one
  rejected report cannot kill the consumer for every message behind it.
- **The dev topic keeps messages for 5 minutes** (`retention.ms=300000`), so
  stale events from earlier runs do not get replayed into a new run.
- **Adding a service means editing
  [services.json](../../services.json) and rebuilding Sagawise**, since the DSL
  and the service list are baked into the image at build time.

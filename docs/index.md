# Sagawise

Sagawise is a **bookkeeper** for saga-pattern distributed transactions. Services exchange their real messages over their own transport _(e.g kafka)_ and separately report `publish`, `consume` and `fail` events to Sagawise over HTTP. Sagawise checks those reports against a declared workflow. Sagawise itself decides only one thing: that a message was never picked up. When a service reports that it published a task, a timer starts. If the receiving service does not report a matching `consume` before the task's timeout, Sagawise marks the task FAILED and calls the publisher's `failure_url`, so the publisher can undo its side of the work.

The pages have a tutorial to do, explanations to understand, how-to guides for a task, reference to look things up.

| Read | When |
| --- | --- |
| **Tutorial** | |
| [Your first saga](first-saga.md) | You are new. Write a workflow, register a failure callback, run a saga, break it, then wire the calls into your services. Twenty minutes. |
| **Explanation** | |
| [Concepts](concepts.md) | Basic concepts about a saga tracker. The problem, the vocabulary, and how the pieces fit. |
| [Architecture](architecture.md) | You are new to the code. Every component, where it lives, what it owns, and how one saga moves through them. |
| [History](history/index.md) | The audit that started the hardening work, the roadmap that answered it, and the design note for the state-machine rewrite. Frozen; read for the reasoning, not for current behaviour. |
| **How-to** | |
| [Operations](operations.md) | You run it: probes, metrics, logs, alerts, and what to do when Redis or Postgres is down. |
| [Security](security.md) | You deploy it: who may call what, API keys, CORS, signed webhooks. |
| [Benchmarks](benchmarks.md) | You want numbers, or you are about to claim a performance change. Method, charts across the hardening work, and every recorded run. |
| [Development](development.md) | You are changing the code. Layout, build, the test tiers and the rules they follow, CI, this site. |
| **Reference** | |
| [Contract](contract.md) | You need the exact state machine, retry semantics, status codes or timing guarantees. Tests are written against this, not against the code. |
| [HTTP API](api.md) | You are calling it, or want to know exactly what the SDKs send. Every endpoint, parameter, response and error code, with curl. |
| [Configuration](configuration.md) | You are deploying it, or a start-up log line names a variable. Every setting, its default, and what makes the process refuse to start. |

Source, examples, SDKs and the Helm chart are in the
[repository](https://github.com/venturenox/sagawise).

_(The documentation follows Diátaxis model)_
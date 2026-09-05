# Sagawise Python SDK

![sagawise platform logo](https://raw.githubusercontent.com/venturenox/sagawise/main/sdk/sagawise-platform-logo.png)

Easy to adopt workflow tracking which instantly lets developers bring resilience into their implementations of saga patterns.

[Website](https://venturenox.com/work/sagawise/) | [Documentation](https://github.com/venturenox/wtfsaga/tree/main)

## Table of Contents

- [Sagawise Python SDK](#sagawise-python-sdk)
  - [Table of Contents](#table-of-contents)
  - [Features](#features)
  - [Installing](#installing)
  - [Importing](#importing)
  - [Start Workflow](#start-workflow)
    - [Input](#input)
    - [Return](#return)
    - [Example](#example)
  - [Publish](#publish)
    - [Input](#input-1)
    - [Return](#return-1)
    - [Example](#example-1)
  - [Consume](#consume)
    - [Input](#input-2)
    - [Return](#return-2)
    - [Example](#example-2)
  - [Fail](#fail)
    - [Input](#input-3)
    - [Return](#return-3)
    - [Example](#example-3)

---

## Authentication and webhook signatures

Set `SAGAWISE_API_KEY` alongside `SAGAWISE_URL` (or pass `Sagawise(api_key=...)`);
every call then carries `Authorization: Bearer <key>`. Sagawise answers 401
`UNAUTHORIZED` without it.

Failure webhooks are signed when the server has `SAGAWISE_WEBHOOK_SECRET`.
Verify against the raw body before trusting a compensation request:

```python
from sagawise.sagawise import verify_signature

@app.post('/failure_report')
def failure_report():
    if not verify_signature(os.environ['SAGAWISE_WEBHOOK_SECRET'], request.headers, request.get_data()):
        abort(401)
    # compensate ...
    return '', 200
```

`verify_signature` returns `True` only when `X-Sagawise-Signature` matches
and `X-Sagawise-Timestamp` is within 5 minutes of now (`tolerance_seconds`).

## Features

- Call functions to interact with Sagawise
- Start a Workflow instance
- Publish message on specific topic
- Confirm consumption of message
- Report failure to consume message
- Apply re-try mechanism by:
  - Re-publishing
  - Re-consuming
  - Re-failing

---

## Installing

Sagawise will soon be published on `pip` (see Roadmap)

<!-- ### Using Pip
```
pip install sagawise
``` -->

---

## Importing

Once the package is installed, you can import the library using `import` approach.

```python
from sagawise import Sagawise

sagawise_instance = Sagawise()            # per-request timeout: 1 second
sagawise_instance = Sagawise(timeout=5)   # seconds
```

The client reads `SAGAWISE_URL` (base URL of the Sagawise server) from the environment. `timeout` is in **seconds**, the unit `requests` uses.

---

## Start Workflow

To start a workflow instance, use `start_workflow` function.

### Input

The `start_workflow` function **requires** the following keys:

- workflow_name (STRING)
- workflow_version (STRING)

### Return

The `start_workflow` function returns the workflow instance ID (STRING).

It **raises** instead of returning a value when:

- a required argument is empty (`ValueError`)
- Sagawise is unreachable or times out (`requests.exceptions.ConnectionError` / `Timeout`)
- Sagawise answers with a non-2xx status (`requests.exceptions.HTTPError`; `error.response` carries the answer)

All request failures are subclasses of `requests.exceptions.RequestException`. Exceptions are never caught and returned as values.

### Example

```python
workflow_instance_id = sagawise_instance.start_workflow('workflow_name', 'workflow_version')
```

---

## Publish

To inform the publish of message on a topic, use `publish_message` function.

### Input

The `publish_message` function **requires** the following keys:

- workflow_instance_id (STRING)
- workflow_version (STRING)
- event_name (STRING)
- payload (dict, non-empty)

Optional Key:

- is_retry (BOOLEAN). Default value is `false`

### Return

The `publish_message` function returns `None` on success. It **raises** when a required argument is empty, or when Sagawise is unreachable or answers with a non-2xx status (see [Start Workflow](#return)).

### Example

```python
sagawise_instance.publish_message(
	workflow_instance_id,
	'1.0',
	payload.event,
	payload
)
```

---

## Consume

To inform the successful consumption of a message on a topic, use `consume_message` function.

### Input

The `consume_message` function **requires** the following keys:

- workflow_instance_id (STRING)
- workflow_version (STRING)
- event_name (STRING)
- service_name (STRING)

Optional Key:

- is_retry (BOOLEAN). Default value is `false`

### Return

The `consume_message` function returns `None` on success. It **raises** when a required argument is empty, or when Sagawise is unreachable or answers with a non-2xx status (see [Start Workflow](#return)).

### Example

```python
sagawise_instance.consume_message(
	data.workflow_instance_id,
	'1.0',
	data.event,
	'notification'
)
```

---

## Fail

To inform the failure of a message consumption by a specific service, use `fail_message` function.

### Input

The `fail_message` function **requires** the following keys:

- workflow_instance_id (STRING)
- workflow_version (STRING)
- event_name (STRING)
- service_name (STRING)

Optional Key:

- is_retry (BOOLEAN). Default value is `false`

### Return

The `fail_message` function returns `None` on success. It **raises** when a required argument is empty, or when Sagawise is unreachable or answers with a non-2xx status (see [Start Workflow](#return)).

### Example

```python
sagawise_instance.fail_message(
	data.workflow_instance_id,
	'1.0',
	data.event,
	'payment'
)
```

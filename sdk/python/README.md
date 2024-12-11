# Sagawise Python SDK

![sagawise platform logo](../sagawise-platform-logo.png)

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

sagawise_instance = Sagawise()
```

---

## Start Workflow

To start a workflow instance, use `start_workflow` function.

### Input

The `start_workflow` function **requires** the following keys:

- workflow_name (STRING)
- workflow_version (STRING)

### Return

The `start_workflow` function may return any of these:

- Workflow instance ID (STRING) - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

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
- data (Object)

Optional Key:

- is_retry (BOOLEAN). Default value is `false`

### Return

The `publish_message` function may return any of these:

- Nothing - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

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

The `consume_message` function may return any of these:

- Nothing - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

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

The `fail_message` function may return any of these:

- Nothing - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

### Example

```python
sagawise_instance.fail_message(
	data.workflow_instance_id,
	'1.0',
	data.event,
	'payment'
)
```

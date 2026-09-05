# Sagawise Nodejs SDK

![sagawise platform logo](https://raw.githubusercontent.com/venturenox/sagawise/main/sdk/sagawise-platform-logo.png)

Easy to adopt workflow tracking which instantly lets developers bring resilience into their implementations of saga patterns.

[Website](https://venturenox.com/work/sagawise/) | [Documentation](https://github.com/venturenox/wtfsaga/tree/main) | [NodeJS SDK Documentation](https://github.com/venturenox/sagawise/blob/main/sdk/nodejs)

## Table of Contents

- [Features](#features)
- [Installing](#installing)
- [Importing](#importing)
- [Start Workflow](#start-workflow)
- [Publish](#publish)
- [Consume](#consume)
- [Fail](#fail)

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

Sagawise will soon be published on `npm` and `yarn` (see Roadmap)

<!-- ### Using npm
```
npm install sagawise
```

### Using yarn
```
yarn add sagawise
```

### Using pnpm
```
pnpm add sagawise
``` -->

---

## Importing

Once the package is installed, you can import the library using `require` approach. **Only default export is available**:

```javascript
const sagawise = require("sagawise");
```

The client reads `SAGAWISE_URL` (base URL of the Sagawise server) at import time, and `SAGAWISE_TIMEOUT_MS` (per-request timeout, default `1000`).

---

## Start Workflow

To start a workflow instance, use `start_workflow` function.

### Input

The `start_workflow` function requires an **object** with the following **required** keys:

- workflow_name (STRING)
- workflow_version (STRING)

### Return

The `start_workflow` function resolves to the workflow instance ID (STRING).

It **rejects** (throws when awaited) instead of returning a value when:

- a required key is missing or empty (`Error`)
- Sagawise is unreachable, times out, or answers with a non-2xx status (an axios error; `error.response.status` and `error.response.data` carry Sagawise's answer)

Errors are never caught and returned as values, so an `await` inside a `try`/`catch` is enough to notice a failed report.

### Example

```javascript
const workflow_instance_id = await sagawise.start_workflow({
  workflow_name: "user_creation",
  workflow_version: "1.0",
});
```

---

## Publish

To inform the publish of message on a topic, use `publish_message` function.

### Input

The `publish_message` function requires an **object** with the following **required** keys:

- workflow_instance_id (STRING)
- workflow_version (STRING)
- event_name (STRING)
- payload (Object, non-null)

Optional Key:

- is_retry (BOOLEAN, must be a real boolean). Default value is `false`

### Return

The `publish_message` function resolves to `undefined` on success. It **rejects** when a required key is missing or empty, or when Sagawise is unreachable or answers with a non-2xx status (see [Start Workflow](#return)).

### Example

```javascript
await sagawise.publish_message({
  workflow_instance_id,
  workflow_version: "1.0",
  event_name: payload.event,
  payload,
});
```

---

## Consume

To inform the successful consumption of a message on a topic, use `consume_message` function.

### Input

The `consume_message` function requires an **object** with the following **required** keys:

- workflow_instance_id (STRING)
- workflow_version (STRING)
- event_name (STRING)
- service_name (STRING)

Optional Key:

- is_retry (BOOLEAN, must be a real boolean). Default value is `false`

### Return

The `consume_message` function resolves to `undefined` on success. It **rejects** when a required key is missing or empty, or when Sagawise is unreachable or answers with a non-2xx status (see [Start Workflow](#return)).

### Example

```javascript
await sagawise.consume_message({
  workflow_instance_id: data.workflow_instance_id,
  workflow_version: "1.0",
  event_name: data.event,
  service_name: "notification",
});
```

---

## Fail

To inform the failure of a message consumption by a specific service, use `fail_message` function.

### Input

The `fail_message` function requires an **object** with the following **required** keys:

- workflow_instance_id (STRING)
- workflow_version (STRING)
- event_name (STRING)
- service_name (STRING)

Optional Key:

- is_retry (BOOLEAN, must be a real boolean). Default value is `false`

### Return

The `fail_message` function resolves to `undefined` on success. It **rejects** when a required key is missing or empty, or when Sagawise is unreachable or answers with a non-2xx status (see [Start Workflow](#return)).

### Example

```javascript
await sagawise.fail_message({
  workflow_instance_id: data.workflow_instance_id,
  workflow_version: "1.0",
  event_name: data.event,
  service_name: "payment",
});
```

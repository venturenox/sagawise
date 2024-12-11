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

---

## Start Workflow

To start a workflow instance, use `start_workflow` function.

### Input

The `start_workflow` function requires an **object** with the following **required** keys:

- workflow_name (STRING)
- workflow_version (STRING)

### Return

The `start_workflow` function may return any of these:

- Workflow instance ID (STRING) - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

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
- data (Object)

Optional Key:

- is_retry (BOOLEAN). Default value is `false`

### Return

The `publish_message` function may return any of these:

- Nothing - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

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

- is_retry (BOOLEAN). Default value is `false`

### Return

The `consume_message` function may return any of these:

- Nothing - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

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

- is_retry (BOOLEAN). Default value is `false`

### Return

The `fail_message` function may return any of these:

- Nothing - in case of success
- Error - in case if required object or keys are empty
- Error - in case of any problem with sagawise server

### Example

```javascript
await sagawise.fail_message({
  workflow_instance_id: data.workflow_instance_id,
  workflow_version: "1.0",
  event_name: data.event,
  service_name: "payment",
});
```

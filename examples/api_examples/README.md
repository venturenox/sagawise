# Sagawise

## Example
Let's take an example of this Workflow.

![Sagawise Example Visualization](https://venturenox.com/wp-content/uploads/2024/05/Sagawise-architecture-1024x592.png)

## DSL File
- 1 DSL file for this single Workflow.
- 1 Workflow inside that DSL file
- 4 tasks inside this workflow
	1. Service A **-->** Service B
	2. Service B **-->** Service C
	3. Service C **-->** Service D
	4. Service B **-->** Service D

#### Example DSL file:
```
{
	"workflow": {
		"version": "1.0",
		"schema_version": "1.0",
		"name": "user_creation",
		"tasks": [
			{
				"topic": "user_created",
				"from": "service_a",
				"to": "service_b",
				"timeout": 1000
			},
			{
				"topic": "user_created_saga",
				"from": "service_b",
				"to": "service_c",
				"timeout": 1500
			},
			{
				"topic": "user_created_saga_final",
				"from": "service_c",
				"to": "service_d",
				"timeout": 2000
			},
			{
				"topic": "user_created_saga",
				"from": "service_b",
				"to": "service_d",
				"timeout": 2500
			}
		]
	}
}
```

#### Example Services file of same Workflow:
```
[
	{
		"service_name": "auth",
		"failure_url": "http://auth:4000/api/v1/failure_report"
	},
	{
		"service_name": "notification",
		"failure_url": "http://notification:4003/api/v1/failure_report"
	},
	{
		"service_name": "intermediate",
		"failure_url": "http://intermediate:4005/api/v1/failure_report"
	},
	{
		"service_name": "final",
		"failure_url": "http://final:4004/api/v1/failure_report"
	}
]
```

## Service A
1. Create DB Record
2. Start Workflow instance by sending API request to Sagawise service
3. Use received `workflow_instance_id` and insert into message payload
4. Publish Workflow Task by sending API request to Sagawise service
5. Send Payload to your Pub/Sub host

#### Example code:
```javascript
// Create DB Record etc

// Start workflow
const resp = await axios({
	method: 'post',
	url: process.env.SAGAWISE_URL+'/start_instance',
	params: {
		workflow_name: 'user_creation',
		workflow_version: '1.0',
	}
});

// Insert ID in payload
const payload = {
	time_stamp: Date.now(),
	user_id,
	tenant_id,
	workflow_instance_id: resp.data.workflow_instance_id,
	event: process.env.USER_CREATED,
};

// Publish event
const resp2 = await axios({
	method: 'post',
	url: process.env.SAGAWISE_URL+'/update_instance',
	params: {
		workflow_instance_id: resp.data.workflow_instance_id,
		workflow_version: '1.0',
		event_name: payload.event,
		action_type: 'publish',
		is_retry: true,
	},
	data: payload
});

// Send Payload to Pub/Sub
const value = Buffer.from(JSON.stringify(message));
await this._producer.produce(
	topic,
	partition,
	value,
	key,
);
```

## Service B
1. Consume Event and get payload data
2. Use received `workflow_instance_id` from payload data to refer to workflow
3. In case of Success, Consume Workflow Task by sending API request to Sagawise service
4. In case of Failure, Fail Workflow Task by sending API request to Sagawise service
5. Publish second event by sending publish API request to Sagawise service again, with same workflow_instance_id
6. Send Payload to your Pub/Sub host

#### Example code:
```
// Message consuming inside Consumer

// Consume Event
const resp = await axios({
	method: 'post',
	url: process.env.SAGAWISE_URL+'/update_instance',
	params: {
		workflow_instance_id: data.workflow_instance_id,
		workflow_version: '1.0',
		event_name: data.event,
		action_type: 'consume',
		service_name: 'service_b',
		is_retry: false,
	}
});

// Publish Next Event
const resp2 = await axios({
	method: 'post',
	url: process.env.SAGAWISE_URL+'/update_instance',
	params: {
		workflow_instance_id: data.workflow_instance_id,
		workflow_version: '1.0',
		event_name: 'user_created_saga',
		action_type: 'publish',
		is_retry: false,
	},
	data: {
		...data,
		event: 'user_created_saga',
	},
});

// Send Payload to Pub/Sub
const message = {
	...data,
	event: 'user_created_saga',
},
const value = Buffer.from(JSON.stringify(message));
await this._producer.produce(
	topic,
	partition,
	value,
	key,
);
```


## Service C
1. Consume Event and get payload data
2. Use received `workflow_instance_id` from payload data to refer to workflow
3. In case of Success, Consume Workflow Task by sending API request to Sagawise service
4. In case of Failure, Fail Workflow Task by sending API request to Sagawise service
5. Publish second event by sending publish API request to Sagawise service again, with same workflow_instance_id
6. Send Payload to your Pub/Sub host

#### Example code:
```
// Message consuming inside Consumer

if (fail) {
	await axios({
		method: 'post',
		url: process.env.SAGAWISE_URL+'/update_instance',
		params: {
			workflow_instance_id: data.workflow_instance_id,
			workflow_version: '1.0',
			event_name: data.event,
			action_type: 'fail',
			service_name: 'service_c',
			is_retry: false,
		}
	});
} else {
	
	// Consume Event
	const resp = await axios({
		method: 'post',
		url: process.env.SAGAWISE_URL+'/update_instance',
		params: {
			workflow_instance_id: data.workflow_instance_id,
			workflow_version: '1.0',
			event_name: data.event,
			action_type: 'consume',
			service_name: 'service_c',
			is_retry: true,
		}
	});

	// Publish Next Event
	const resp2 = await axios({
		method: 'post',
		url: process.env.SAGAWISE_URL+'/update_instance',
		params: {
			workflow_instance_id: data.workflow_instance_id,
			workflow_version: '1.0',
			event_name: 'user_created_saga_final',
			action_type: 'publish',
			is_retry: false,
		},
		data: {
			...data,
			event: 'user_created_saga_final',
		}
	});

	// Send Payload to Pub/Sub
	const message = {
		...data,
		event: 'user_created_saga_final',
	},
	const value = Buffer.from(JSON.stringify(message));
	
	await this._producer.produce(
		topic,
		partition,
		value,
		key,
	);
}
```


## Service D
1. Consume Event and get payload data
2. Use received `workflow_instance_id` from payload data to refer to workflow
3. In case of Success, Consume Workflow Task by sending API request to Sagawise service
4. In case of Failure, Fail Workflow Task by sending API request to Sagawise service

#### Example code:
```
// Message consuming inside Consumer

if (event == 'user_created_saga') {

	// Consume Event 1
	const resp = await axios({
		method: 'post',
		url: process.env.SAGAWISE_URL+'/update_instance',
		params: {
			workflow_instance_id: data.workflow_instance_id,
			workflow_version: '1.0',
			event_name: data.event,
			action_type: 'consume',
			service_name: 'final',
			is_retry: true,
		}
	});

} else if (event == 'user_created_saga_final') {

	// Consume Event 2
	const resp = await axios({
		method: 'post',
		url: process.env.SAGAWISE_URL+'/update_instance',
		params: {
			workflow_instance_id: data.workflow_instance_id,
			workflow_version: '1.0',
			event_name: data.event,
			action_type: 'consume',
			service_name: 'final',
			is_retry: true,
		}
	});
}
```

<!-- Command for a clean start: make stop && docker image remove $(docker image ls -aq) && docker volume remove $(docker volume ls -q) && clear && make start -->

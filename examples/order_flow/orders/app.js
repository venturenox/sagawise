const express = require('express');
const { Kafka } = require('kafkajs');
const { startInstance, updateInstance } = require('./sagawiseClient');

const TOPIC = process.env.KAFKA_EVENT_TOPIC;
const WORKFLOW_NAME = process.env.WORKFLOW_NAME || 'order_flow';

const kafka = new Kafka({ clientId: 'orders', brokers: [process.env.KAFKA_HOST] });
const producer = kafka.producer();

const app = express();
app.use(express.json());

app.post('/orders', async (req, res) => {
	const order_id = req.body.order_id || Date.now();

	// 1. Open the workflow instance.
	const workflow_instance_id = await startInstance(WORKFLOW_NAME);
	console.log(`[orders] started workflow ${workflow_instance_id} for order ${order_id}`);

	// 2. The instance id rides inside the message so downstream services
	//    can report against the same workflow.
	const payload = { event: 'order_created', order_id, workflow_instance_id };

	// 3. Announce the publish, then actually publish. Sagawise starts the
	//    timeout clock at step 3, so tell it before the message is in flight.
	await updateInstance('publish', {
		workflowInstanceId: workflow_instance_id,
		eventName: 'order_created',
		payload,
	});

	await producer.send({ topic: TOPIC, messages: [{ value: JSON.stringify(payload) }] });
	console.log(`[orders] published order_created for ${workflow_instance_id}`);

	res.status(201).json({ order_id, workflow_instance_id });
});

// Sagawise POSTs here when a task this service published fails or times out.
// This is where a compensating transaction would undo the order.
app.post('/v1/sagawise/failure_report', (req, res) => {
	console.log('[orders] FAILURE REPORT:', JSON.stringify(req.body));
	res.sendStatus(200);
});

producer.connect()
	.then(() => app.listen(process.env.PORT, () => console.log(`orders listening on ${process.env.PORT}`)))
	.catch((err) => { console.error('kafka connect failed', err); process.exit(1); });
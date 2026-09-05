const express = require('express');
const { Kafka } = require('kafkajs');
const { updateInstance } = require('./sagawiseClient');

const TOPIC = process.env.KAFKA_EVENT_TOPIC;
const SERVICE = 'payments';

const kafka = new Kafka({ clientId: SERVICE, brokers: [process.env.KAFKA_HOST] });
const consumer = kafka.consumer({ groupId: process.env.KAFKA_GROUP_ID });
const producer = kafka.producer();

const app = express();
app.use(express.json());

// Set FAIL_PAYMENTS=true in compose to watch the failure path instead.
const SHOULD_FAIL = process.env.FAIL_PAYMENTS === 'true';

async function handleOrderCreated(data) {
	const { workflow_instance_id, order_id } = data;
	console.log(`[payments] received order_created for ${workflow_instance_id}`);

	if (SHOULD_FAIL) {
		// Deliberate failure: tell sagawise this task will not complete.
		// Sagawise marks it FAILED immediately rather than waiting for the timeout.
		await updateInstance('fail', {
			workflowInstanceId: workflow_instance_id,
			eventName: 'order_created',
			serviceName: SERVICE,
		});
		console.log(`[payments] FAILED payment for order ${order_id}`);
		return;
	}

	// 1. Acknowledge the consume. This stops task 0's timeout clock.
	await updateInstance('consume', {
		workflowInstanceId: workflow_instance_id,
		eventName: 'order_created',
		serviceName: SERVICE,
	});

	// 2. Same instance id carries forward into the next task.
	const payload = { event: 'payment_done', order_id, workflow_instance_id };

	// 3. Announce the next publish, then produce it.
	await updateInstance('publish', {
		workflowInstanceId: workflow_instance_id,
		eventName: 'payment_done',
		payload,
	});

	await producer.send({ topic: TOPIC, messages: [{ value: JSON.stringify(payload) }] });
	console.log(`[payments] published payment_done for ${workflow_instance_id}`);
}

app.post('/v1/sagawise/failure_report', (req, res) => {
	console.log('[payments] FAILURE REPORT:', JSON.stringify(req.body));
	res.sendStatus(200);
});

async function main() {
	await producer.connect();
	await consumer.connect();
	await consumer.subscribe({ topic: TOPIC, fromBeginning: false });

	await consumer.run({
		eachMessage: async ({ message }) => {
			const data = JSON.parse(message.value.toString());
			// One topic carries every event, so filter by the event field.
			if (data.event !== 'order_created') return;
			try {
				await handleOrderCreated(data);
			} catch (err) {
				// Reporting to sagawise can legitimately fail -- the task may have
				// already timed out (409), or sagawise may be briefly unreachable.
				// Swallow it: one bad message must not kill the consumer for every
				// message behind it.
				console.error(`[payments] could not process ${data.workflow_instance_id}: ${err.message}`);
			}
		},
	});

	app.listen(process.env.PORT, () => console.log(`payments listening on ${process.env.PORT}`));
}

main().catch((err) => { console.error('payments failed to start', err); process.exit(1); });
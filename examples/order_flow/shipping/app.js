const express = require('express');
const { Kafka } = require('kafkajs');
const { updateInstance } = require('./sagawiseClient');

const TOPIC = process.env.KAFKA_EVENT_TOPIC;
const SERVICE = 'shipping';

const kafka = new Kafka({ clientId: SERVICE, brokers: [process.env.KAFKA_HOST] });
const consumer = kafka.consumer({ groupId: process.env.KAFKA_GROUP_ID });

const app = express();
app.use(express.json());

async function handlePaymentDone(data) {
	const { workflow_instance_id, order_id } = data;
	console.log(`[shipping] received payment_done for ${workflow_instance_id}`);

	// Terminal task: acknowledge the consume and stop. Nothing to publish.
	// Once this lands, every task is COMPLETED, so sagawise marks the whole
	// workflow COMPLETED and archives it to Postgres.
	await updateInstance('consume', {
		workflowInstanceId: workflow_instance_id,
		eventName: 'payment_done',
		serviceName: SERVICE,
	});

	console.log(`[shipping] shipped order ${order_id} -- workflow complete`);
}

// shipping never publishes, so it should never receive a failure report.
// The endpoint exists because services.json requires a failure_url per service.
app.post('/v1/sagawise/failure_report', (req, res) => {
	console.log('[shipping] FAILURE REPORT:', JSON.stringify(req.body));
	res.sendStatus(200);
});

async function main() {
	await consumer.connect();
	await consumer.subscribe({ topic: TOPIC, fromBeginning: false });

	await consumer.run({
		eachMessage: async ({ message }) => {
			const data = JSON.parse(message.value.toString());
			if (data.event !== 'payment_done') return;
			try {
				await handlePaymentDone(data);
			} catch (err) {
				// See payments/app.js -- a rejected report must not stop the consumer.
				console.error(`[shipping] could not process ${data.workflow_instance_id}: ${err.message}`);
			}
		},
	});

	app.listen(process.env.PORT, () => console.log(`shipping listening on ${process.env.PORT}`));
}

main().catch((err) => { console.error('shipping failed to start', err); process.exit(1); });
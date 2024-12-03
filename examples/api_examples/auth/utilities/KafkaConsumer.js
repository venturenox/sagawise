const Kafka = require('node-rdkafka');
const certifi = require('certifi');
const logger = require('./Logger');

let kc = null;

function createConsumer(onData, group, client) {
	const consumer = new Kafka.KafkaConsumer(
		{
			'client.id': client,
			'bootstrap.servers': process.env.KAFKA_HOST,
			'sasl.username': process.env.KAFKA_USER_NAME,
			'sasl.password': process.env.KAFKA_PASSWORD,
			'security.protocol': process.env.KAFKA_SECURITY_PROTOCOL,
			'sasl.mechanisms': 'PLAIN',
			'group.id': group,
			'enable.ssl.certificate.verification': 'false',
			'ssl.ca.location': certifi
		},
		{
			'auto.offset.reset': 'earliest',
		},
	);

	kc = consumer;

	return new Promise((resolve, reject) => {
		consumer
			.on('ready', () => resolve(consumer))
			.on('data', ({ key, value, partition, offset }) => {
				try {
					let json = JSON.parse(
						`{"key":"${key}","partition":${partition},"offset":${offset},"data":${value}}`,
					);
					onData(json);
				} catch (error) {
					logger.error({ error });
				}
			})
			.on('event.error', (error) => {
				reject(error);
			});

		consumer.connect();
	});
}

async function startConsumer(onData, { topic, group, client }) {
	const consumer = await createConsumer(onData, group, client);
	consumer.subscribe([topic]);
	consumer.consume();
	process.on('SIGINT', () => {
		console.log('\nDisconnecting consumer ...');
		consumer.disconnect();
	});
}

module.exports = { startConsumer, KafkaConsumer: { getInstance: () => kc } };

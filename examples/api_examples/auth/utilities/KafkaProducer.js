const certifi = require('certifi');

class KafkaProducer {
	constructor() {
		this._kafka = require('node-rdkafka');
		this._producer = new this._kafka.Producer({
			'bootstrap.servers': process.env.KAFKA_HOST,
			'message.send.max.retries': 10,
			'sasl.username': process.env.KAFKA_USER_NAME,
			'sasl.password': process.env.KAFKA_PASSWORD,
			'security.protocol': process.env.KAFKA_SECURITY_PROTOCOL,
			'sasl.mechanisms': 'PLAIN',
			dr_msg_cb: true,
			'enable.ssl.certificate.verification': 'false',
			'ssl.ca.location': certifi
		});
		this.readyProducer();
	}

	async connectProducer(onDeliveryReport) {
		return new Promise((resolve, reject) => {
			this._producer
				.on('ready', () => resolve(this._producer))
				.on('delivery-report', onDeliveryReport)
				.on('event.error', (err) => {
					console.warn('event.error', err);
					reject(err);
				});

			this._producer.connect();
		});
	}

	async readyProducer() {
		try {
			this._producer = await this.connectProducer((err, report) => {
				if (err) {
					return err;
				} else {
					return report;
				}
			});
		} catch (error) {
			console.log(error);
		}
	}


	async sendPayload(message, topic, partition) {
		try {
			const key = 'Data_To_Send';
			const value = Buffer.from(JSON.stringify(message));
			this._producer.poll();
			await this._producer.produce(
				topic,
				partition,
				value,
				key,
			);
			this._producer.flush(3000);
		} catch (e) {
			return e;
		}
	}

	disconnect() {
		try {
			this._producer.flush(3000);
			this._producer.disconnect();
		} catch (err) {
			console.log('Kafka Disconnect Error:', err);
		}
	}
}

module.exports = new KafkaProducer();

const { EventKey } = require("./../utility/KeyMaster");
const KafkaProducer = require('./KafkaProducer');
const axios = require('axios');

const event_functions = Object.freeze({

	[EventKey.USER_CREATED]: async (data) => {
		console.log("Event Consumed: ", data);

		// Sagawise Consume Event
		const resp = await axios({
			method: 'post',
			url: process.env.SAGAWISE_URL+'/update_instance',
			params: {
				workflow_instance_id: data.workflow_instance_id,
				workflow_version: '1.0',
				event_name: data.event,
				action_type: 'consume',
				service_name: 'notification',
				is_retry: true,
			}
		});
		console.log('consume user_created response: ', resp.status);
		

		// Sagawise Publish Next Event
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
		console.log('publish user_created_saga response: ', resp2.status);
		

		KafkaProducer.sendPayload(
			{
				...data,
				event: 'user_created_saga',
			},
			process.env.KAFKA_EVENT_TOPIC,
			0
		);
	},
});

exports.streamProcessor = async ({ data }) => {
	
	if (event_functions[data.event]) {
		await event_functions[data.event](data);
	}
};

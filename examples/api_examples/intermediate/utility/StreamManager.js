const { EventKey } = require("./KeyMaster");
const userProfileController = require("../controllers/UserProfileController");
const KafkaProducer = require('./KafkaProducer');
const axios = require('axios');

const event_functions = Object.freeze({

	[EventKey.USER_CREATED_SAGA]: async (data) => {
		console.log("Event Consumed: ", data);

		const userProfile = await userProfileController.create(data.properties);

		if (userProfile.error) {

			console.error('Fail... ', userProfile.error);

			// Sagawise Fail Event
			await axios({
				method: 'post',
				url: process.env.SAGAWISE_URL+'/update_instance',
				params: {
					workflow_instance_id: data.workflow_instance_id,
					workflow_version: '1.0',
					event_name: data.event,
					action_type: 'fail',
					service_name: 'intermediate',
					is_retry: true,
				}
			});

		} else if (userProfile.result) {
			
			console.info('Consume...');

			// Sagawise Consume Event
			const resp = await axios({
				method: 'post',
				url: process.env.SAGAWISE_URL+'/update_instance',
				params: {
					workflow_instance_id: data.workflow_instance_id,
					workflow_version: '1.0',
					event_name: data.event,
					action_type: 'consume',
					service_name: 'intermediate',
					is_retry: true,
				}
			});
			console.log('consume user_created_saga response: ', resp.status);
			

			// Sagawise Publish Next Event
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
			console.log('publish user_created_saga_final response: ', resp2.status);
			

			KafkaProducer.sendPayload(
				{
					...data,
					event: 'user_created_saga_final',
				},
				process.env.KAFKA_EVENT_TOPIC,
				0
			);
		}
	},
});

exports.streamProcessor = async ({ data }) => {
	
	if (event_functions[data.event]) {
		await event_functions[data.event](data);
	}
};

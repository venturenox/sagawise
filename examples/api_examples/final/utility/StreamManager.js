const { EventKey } = require("./KeyMaster");
const axios = require('axios');

const event_functions = Object.freeze({

	[EventKey.USER_CREATED_SAGA]: async (data) => {
		console.log("Event consumed: ", data);

		// Sagawise Consume Event
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
		console.log('consume user_created_saga response: ', resp.status);
		
	},

	[EventKey.USER_CREATED_SAGA_FINAL]: async (data) => {
		console.log("Event consumed: ", data);

		// Sagawise Consume Event
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
		console.log('consume user_created_saga_final response: ', resp.status);
		
	},
});

exports.streamProcessor = async ({ data }) => {
	
	if (event_functions[data.event]) {
		await event_functions[data.event](data);
	}
};

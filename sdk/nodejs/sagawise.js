const axios = require('axios');

const axios_instance = axios.create({
	baseURL: process.env.SAGAWISE_URL,
	timeout: 1000,
	withCredentials: false,
});

class sagawise {

/**
 * The function `start_workflow` asynchronously starts a workflow by sending an HTTP request to a
 * specified endpoint and returns the workflow instance ID.
 * @returns The function `start_workflow` is returning the `workflow_instance_id` from the data
 * received in the HTTP response after starting the workflow instance. If there is an error during the
 * process, it will log the error and return the error object.
 */
	async start_workflow({ workflow_name, workflow_version }) {
		try {
			if (workflow_name == '' || workflow_version == '') {
				throw new Error('workflow_name and workflow_version are required.');
			}

			// send HTTP request to Sagawise app
			const req = await axios_instance.request({
				url: '/start_instance',
				method: 'post',
				params: {
					workflow_name,
					workflow_version,
				}
			});
			
			return req.data.workflow_instance_id;
	
		} catch (error) {
			console.log('Error: ', error);
			return error;
		}
	}

	/**
	 * The function `publish_message` sends an HTTP request to update a workflow instance with specified
	 * parameters and payload, handling errors and validation checks.
	 * @returns The function `publish_message` will return an error object if any of the required keys
	 * (`workflow_instance_id`, `workflow_version`, `event_name`, `payload`) are missing or if the payload
	 * is an empty object. If an error occurs during the execution of the function (e.g., an error in the
	 * HTTP request), it will catch the error, log it to the console, and return the
	 */
	async publish_message({ workflow_instance_id, workflow_version, event_name, is_retry = false, payload }) {
		try {
			if ( workflow_instance_id == '' || workflow_version == '' || event_name == '' || is_retry == '' || payload == '' || Object.is(payload, {}) ) {
				throw new Error('Required keys: workflow_instance_id, workflow_version, event_name, payload');
			}
			
			// send HTTP request to Sagawise app
			await axios_instance.request({
				url: '/update_instance',
				method: 'post',
				params: {
					workflow_instance_id,
					workflow_version,
					event_name,
					action_type: 'publish',
					is_retry,
				},
				data: payload,
			});

		} catch (error) {
			console.log('Error: ', error);
			return error;
		}
	}

	/**
	 * The function `consume_message` asynchronously sends an HTTP request to update a workflow instance
	 * in a Sagawise app with specified parameters, handling errors and returning any encountered errors.
	 * @returns The `consume_message` function is returning the error object caught in the `catch` block
	 * if an error occurs during the execution of the function.
	 */
	async consume_message({ workflow_instance_id, workflow_version, event_name, service_name, is_retry = false }) {
		try {
			if ( workflow_instance_id == '' || workflow_version == '' || event_name == '' || is_retry == '' || service_name == '' ) {
				throw new Error('Required keys: workflow_instance_id, workflow_version, event_name, service_name');
			}

			// send HTTP request to Sagawise app
			await axios_instance.request({
				url: '/update_instance',
				method: 'post',
				params: {
					workflow_instance_id,
					workflow_version,
					event_name,
					action_type: 'consume',
					service_name,
					is_retry,
				}
			});
	
		} catch (error) {
			console.log('Error: ', error);
			return error;
		}
	}

	/**
	 * The function `fail_message` sends a POST request to update an instance in a Sagawise app with
	 * specified parameters and handles any errors that occur.
	 * @returns The `fail_message` function is returning the error object caught in the `catch` block.
	 */
	async fail_message({ workflow_instance_id, workflow_version, event_name, service_name, is_retry = false }) {
		try {
			if ( workflow_instance_id == '' || workflow_version == '' || event_name == '' || is_retry == '' || service_name == '' ) {
				throw new Error('Required keys: workflow_instance_id, workflow_version, event_name');
			}

			// send HTTP request to Sagawise app
			await axios_instance.request({
				url: '/update_instance',
				method: 'post',
				params: {
					workflow_instance_id,
					workflow_version,
					event_name,
					action_type: 'fail',
					service_name,
					is_retry,
				}
			});
	
		} catch (error) {
			console.log('Error: ', error);
			return error;
		}
	}
}

module.exports = new sagawise();

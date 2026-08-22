const axios = require('axios');

const SAGAWISE_URL = process.env.SAGAWISE_URL;
const WORKFLOW_VERSION = process.env.WORKFLOW_VERSION || '1.0';

// Open a new workflow instance. Returns the id that ties every later call together.
async function startInstance(workflowName) {
	const res = await axios.post(`${SAGAWISE_URL}/start_instance`, null, {
		params: {
			workflow_name: workflowName,
			workflow_version: WORKFLOW_VERSION,
		},
	});
	return res.data.workflow_instance_id;
}

// Report a task transition. action is 'publish' | 'consume' | 'fail'.
async function updateInstance(action, { workflowInstanceId, eventName, serviceName, payload }) {
	const params = {
		workflow_instance_id: workflowInstanceId,
		workflow_version: WORKFLOW_VERSION,
		event_name: eventName,
		action_type: action,
		is_retry: false,
	};
	if (serviceName) params.service_name = serviceName;

	const res = await axios.post(`${SAGAWISE_URL}/update_instance`, payload || null, { params });
	console.log(`[sagawise] ${action} ${eventName} -> ${res.data}`);
	return res.data;
}

module.exports = { startInstance, updateInstance };
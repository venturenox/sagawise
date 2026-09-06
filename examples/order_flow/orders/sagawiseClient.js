const axios = require('axios');

const SAGAWISE_URL = process.env.SAGAWISE_URL;
// Every call carries the API key (Sagawise refuses unauthenticated requests).
const headers = { Authorization: `Bearer ${process.env.SAGAWISE_API_KEY}` };

// Open a new workflow instance. Returns the id that ties every later call together.
async function startInstance(workflowName) {
	const res = await axios.post(`${SAGAWISE_URL}/start_instance`, null, {
		params: { workflow_name: workflowName },
		headers,
	});
	return res.data.workflow_instance_id;
}

// Report a task transition. action is 'publish' | 'consume' | 'fail'.
async function updateInstance(action, { workflowInstanceId, eventName, serviceName, payload }) {
	const params = {
		workflow_instance_id: workflowInstanceId,
		event_name: eventName,
		action_type: action,
		is_retry: false,
	};
	if (serviceName) params.service_name = serviceName;

	const res = await axios.post(`${SAGAWISE_URL}/update_instance`, payload || null, { params, headers });
	console.log(`[sagawise] ${action} ${eventName} -> ${res.data}`);
	return res.data;
}

module.exports = { startInstance, updateInstance };
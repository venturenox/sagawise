const axios = require('axios');

// Requests to Sagawise time out after this many milliseconds.
const DEFAULT_TIMEOUT_MS = 1000;

const axios_instance = axios.create({
	baseURL: process.env.SAGAWISE_URL,
	timeout: Number(process.env.SAGAWISE_TIMEOUT_MS) || DEFAULT_TIMEOUT_MS,
	withCredentials: false,
});

// requireKeys throws if any named key is missing or empty. Presence is
// checked explicitly (`undefined`, `null`, `''`), never with loose equality:
// `false == ''` is true in JavaScript, which is how `is_retry = false` once
// made every non-retry call a silent no-op.
function requireKeys(obj, keys) {
	const missing = keys.filter((k) => obj[k] === undefined || obj[k] === null || obj[k] === '');
	if (missing.length > 0) {
		throw new Error(`Required keys: ${missing.join(', ')}`);
	}
}

function requireBoolean(name, value) {
	if (typeof value !== 'boolean') {
		throw new Error(`${name} must be a boolean`);
	}
}

// Every method sends exactly the HTTP request it describes and lets any
// failure propagate as a rejected promise: a missing key, an unreachable
// Sagawise, or a non-2xx response (an axios error whose `response` carries
// the status and body). Nothing is caught and returned as a value.
class Sagawise {
	/**
	 * Starts a workflow instance and resolves to its `workflow_instance_id`.
	 * Required keys: `workflow_name`, `workflow_version`.
	 */
	async start_workflow({ workflow_name, workflow_version } = {}) {
		requireKeys({ workflow_name, workflow_version }, ['workflow_name', 'workflow_version']);

		const res = await axios_instance.request({
			url: '/start_instance',
			method: 'post',
			params: { workflow_name, workflow_version },
		});
		return res.data.workflow_instance_id;
	}

	/**
	 * Reports that a message was published on `event_name`. Required keys:
	 * `workflow_instance_id`, `workflow_version`, `event_name`, `payload`
	 * (a non-null object; it becomes the failure webhook body). Optional:
	 * `is_retry` (boolean, default false).
	 */
	async publish_message({ workflow_instance_id, workflow_version, event_name, is_retry = false, payload } = {}) {
		requireKeys({ workflow_instance_id, workflow_version, event_name }, ['workflow_instance_id', 'workflow_version', 'event_name']);
		requireBoolean('is_retry', is_retry);
		if (payload === undefined || payload === null || typeof payload !== 'object') {
			throw new Error('Required keys: payload (must be an object)');
		}

		await axios_instance.request({
			url: '/update_instance',
			method: 'post',
			params: {
				workflow_instance_id,
				workflow_version,
				event_name,
				action_type: 'publish',
				is_retry: String(is_retry),
			},
			data: payload,
		});
	}

	/**
	 * Reports that `service_name` consumed the message on `event_name`.
	 * Required keys: `workflow_instance_id`, `workflow_version`, `event_name`,
	 * `service_name`. Optional: `is_retry` (boolean, default false).
	 */
	async consume_message({ workflow_instance_id, workflow_version, event_name, service_name, is_retry = false } = {}) {
		requireKeys({ workflow_instance_id, workflow_version, event_name, service_name },
			['workflow_instance_id', 'workflow_version', 'event_name', 'service_name']);
		requireBoolean('is_retry', is_retry);

		await axios_instance.request({
			url: '/update_instance',
			method: 'post',
			params: {
				workflow_instance_id,
				workflow_version,
				event_name,
				action_type: 'consume',
				service_name,
				is_retry: String(is_retry),
			},
		});
	}

	/**
	 * Reports that `service_name` failed to process the message on
	 * `event_name`. Required keys: `workflow_instance_id`, `workflow_version`,
	 * `event_name`, `service_name`. Optional: `is_retry` (boolean, default false).
	 */
	async fail_message({ workflow_instance_id, workflow_version, event_name, service_name, is_retry = false } = {}) {
		requireKeys({ workflow_instance_id, workflow_version, event_name, service_name },
			['workflow_instance_id', 'workflow_version', 'event_name', 'service_name']);
		requireBoolean('is_retry', is_retry);

		await axios_instance.request({
			url: '/update_instance',
			method: 'post',
			params: {
				workflow_instance_id,
				workflow_version,
				event_name,
				action_type: 'fail',
				service_name,
				is_retry: String(is_retry),
			},
		});
	}
}

module.exports = new Sagawise();

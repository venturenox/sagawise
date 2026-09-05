const axios = require('axios');
const crypto = require('node:crypto');

// Requests to Sagawise time out after this many milliseconds.
const DEFAULT_TIMEOUT_MS = 1000;

// Every request carries the API key from SAGAWISE_API_KEY as a bearer
// token; Sagawise refuses requests without one (401 UNAUTHORIZED) unless it
// runs with SAGAWISE_AUTH=off.
const headers = {};
if (process.env.SAGAWISE_API_KEY) {
	headers.Authorization = `Bearer ${process.env.SAGAWISE_API_KEY}`;
}

const axios_instance = axios.create({
	baseURL: process.env.SAGAWISE_URL,
	timeout: Number(process.env.SAGAWISE_TIMEOUT_MS) || DEFAULT_TIMEOUT_MS,
	withCredentials: false,
	headers,
});

// How far a webhook's timestamp may be from this clock before it is
// treated as a replay (seconds).
const DEFAULT_TOLERANCE_SECONDS = 300;

/**
 * Verifies the signature of a failure webhook Sagawise delivered
 * (`X-Sagawise-Timestamp` and `X-Sagawise-Signature` headers, HMAC-SHA256
 * of `<timestamp>.<raw body>`). `rawBody` must be the body bytes as
 * received (a Buffer or string), before any JSON parsing. Returns true only
 * when the signature is valid and the timestamp is within `toleranceSeconds`
 * of now. With Express use `express.json({ verify: (req, res, buf) => { req.rawBody = buf; } })`
 * to keep the raw bytes.
 */
function verify_signature({ secret, headers: h, rawBody, toleranceSeconds = DEFAULT_TOLERANCE_SECONDS, now = Date.now() / 1000 } = {}) {
	if (!secret || !h || rawBody === undefined || rawBody === null) {
		return false;
	}
	const get = (name) => {
		const v = h[name] ?? h[name.toLowerCase()];
		return Array.isArray(v) ? v[0] : v;
	};
	const tsHeader = get('X-Sagawise-Timestamp');
	const sigHeader = get('X-Sagawise-Signature');
	if (!tsHeader || !sigHeader || !/^\s*\d+\s*$/.test(tsHeader)) {
		return false;
	}
	const ts = Number(tsHeader);
	if (Math.abs(Math.floor(now) - ts) > toleranceSeconds) {
		return false;
	}
	const expected = crypto.createHmac('sha256', secret).update(`${ts}.`).update(rawBody).digest();
	// Several v1= values may be present during a secret rotation.
	return sigHeader.split(',').some((part) => {
		const p = part.trim();
		if (!p.startsWith('v1=')) return false;
		let got;
		try {
			got = Buffer.from(p.slice(3), 'hex');
		} catch {
			return false;
		}
		return got.length === expected.length && crypto.timingSafeEqual(got, expected);
	});
}

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

const sagawise = new Sagawise();
sagawise.verify_signature = verify_signature;
module.exports = sagawise;

// Contract for the Node SDK (docs/contract.md, audit #15): every call sends
// the HTTP request it describes, and a failed request is surfaced to the
// caller as a rejection, never swallowed. Run with `npm test`.

const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');

const requests = [];
let server, sagawise;

before(async () => {
	server = http.createServer((req, res) => {
		let body = '';
		req.on('data', (c) => { body += c; });
		req.on('end', () => {
			requests.push({ method: req.method, url: new URL(req.url, 'http://x'), body, headers: req.headers });
			if (req.url.includes('boom')) {
				res.statusCode = 500;
				res.end('boom');
				return;
			}
			res.setHeader('content-type', 'application/json');
			res.end(JSON.stringify({ workflow_instance_id: 'abc123' }));
		});
	});
	await new Promise((r) => server.listen(0, '127.0.0.1', r));
	// The SDK builds its axios instance from SAGAWISE_URL at require time.
	process.env.SAGAWISE_URL = `http://127.0.0.1:${server.address().port}`;
	process.env.SAGAWISE_API_KEY = 'test-api-key';
	sagawise = require('../sagawise.js');
});

after(() => new Promise((r) => server.close(r)));

function last() {
	return requests[requests.length - 1];
}

test('start_workflow sends the request and returns the instance id', async () => {
	const n = requests.length;
	const id = await sagawise.start_workflow({ workflow_name: 'order_flow', workflow_version: '1.0' });
	assert.equal(id, 'abc123');
	assert.equal(requests.length, n + 1);
	assert.equal(last().url.pathname, '/start_instance');
	assert.equal(last().url.searchParams.get('workflow_name'), 'order_flow');
});

test('publish_message with default is_retry sends the request', async () => {
	const n = requests.length;
	await sagawise.publish_message({
		workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'order_created', payload: { order_id: 1 },
	});
	assert.equal(requests.length, n + 1, 'no HTTP request was sent');
	const r = last();
	assert.equal(r.url.pathname, '/update_instance');
	assert.equal(r.url.searchParams.get('action_type'), 'publish');
	assert.equal(r.url.searchParams.get('is_retry'), 'false');
	assert.deepEqual(JSON.parse(r.body), { order_id: 1 });
});

test('consume_message with default is_retry sends the request', async () => {
	const n = requests.length;
	await sagawise.consume_message({
		workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'order_created', service_name: 'payments',
	});
	assert.equal(requests.length, n + 1, 'no HTTP request was sent');
	assert.equal(last().url.searchParams.get('action_type'), 'consume');
	assert.equal(last().url.searchParams.get('service_name'), 'payments');
});

test('fail_message with default is_retry sends the request', async () => {
	const n = requests.length;
	await sagawise.fail_message({
		workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'order_created', service_name: 'payments',
	});
	assert.equal(requests.length, n + 1, 'no HTTP request was sent');
	assert.equal(last().url.searchParams.get('action_type'), 'fail');
});

test('explicit is_retry=true is sent through', async () => {
	const n = requests.length;
	await sagawise.publish_message({
		workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'order_created', is_retry: true, payload: { a: 1 },
	});
	assert.equal(requests.length, n + 1);
	assert.equal(last().url.searchParams.get('is_retry'), 'true');
});

test('a 5xx from Sagawise rejects instead of being returned', async () => {
	await assert.rejects(
		sagawise.consume_message({
			workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'boom', service_name: 'payments', is_retry: true,
		}),
	);
});

test('an unreachable Sagawise rejects start_workflow', async () => {
	const saved = process.env.SAGAWISE_URL;
	// The instance is bound at require time, so simulate by closing the server briefly.
	await new Promise((r) => server.close(r));
	try {
		await assert.rejects(sagawise.start_workflow({ workflow_name: 'x', workflow_version: '1.0' }));
	} finally {
		await new Promise((r) => server.listen(new URL(saved).port, '127.0.0.1', r));
	}
});

test('missing required keys reject without sending a request', async () => {
	const n = requests.length;
	await assert.rejects(sagawise.publish_message({ workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'x' }), /payload/);
	await assert.rejects(sagawise.consume_message({ workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'x' }), /service_name/);
	await assert.rejects(sagawise.start_workflow({}), /workflow_name/);
	await assert.rejects(sagawise.consume_message({
		workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'x', service_name: 's', is_retry: 'yes',
	}), /is_retry must be a boolean/);
	assert.equal(requests.length, n, 'a request was sent despite missing keys');
});

test('a 5xx rejection carries the response status', async () => {
	await assert.rejects(
		sagawise.fail_message({ workflow_instance_id: 'abc123', workflow_version: '1.0', event_name: 'boom', service_name: 'payments' }),
		(err) => err.response && err.response.status === 500,
	);
});

// Phase 8: every request carries the API key as a bearer token.
test('requests carry the API key', async () => {
	await sagawise.start_workflow({ workflow_name: 'order_flow', workflow_version: '1.0' });
	assert.equal(last().headers.authorization, 'Bearer test-api-key');
});

// Phase 8: webhook signature verification. The vector is shared with
// backend/webhooksig/webhooksig_test.go and the Python SDK test.
const vec = {
	secret: 'whsec_test_0123456789',
	ts: '1757000000',
	body: '{"order_id":42,"workflow_instance_id":"abc"}',
	sig: 'v1=ae24e8081e830be2781f7fdb0f89712f9ab9ba0519cf53d737f558bd3b6de8da',
};
const vecHeaders = (ts = vec.ts, sig = vec.sig) => ({ 'x-sagawise-timestamp': ts, 'x-sagawise-signature': sig });

test('verify_signature accepts the shared vector', () => {
	assert.equal(sagawise.verify_signature({ secret: vec.secret, headers: vecHeaders(), rawBody: vec.body, now: 1757000030 }), true);
	assert.equal(sagawise.verify_signature({ secret: vec.secret, headers: vecHeaders(), rawBody: Buffer.from(vec.body), now: 1757000030 }), true);
	assert.equal(sagawise.verify_signature({ secret: vec.secret, headers: vecHeaders(vec.ts, 'v1=00,' + vec.sig), rawBody: vec.body, now: 1757000030 }), true, 'rotation: any matching value');
});

test('verify_signature rejects tampering, wrong secret and replay', () => {
	const ok = (over) => sagawise.verify_signature({ secret: vec.secret, headers: vecHeaders(), rawBody: vec.body, now: 1757000030, ...over });
	assert.equal(ok({ rawBody: vec.body + ' ' }), false, 'body changed');
	assert.equal(ok({ secret: 'other' }), false, 'wrong secret');
	assert.equal(ok({ headers: vecHeaders('1757000001', vec.sig) }), false, 'timestamp changed');
	assert.equal(ok({ headers: vecHeaders(vec.ts, 'v1=zz') }), false, 'malformed hex');
	assert.equal(ok({ headers: { 'x-sagawise-timestamp': vec.ts } }), false, 'missing signature');
	assert.equal(ok({ headers: {} }), false, 'missing headers');
	assert.equal(ok({ now: 1757000000 + 6 * 60 }), false, 'replayed 6 minutes later');
	assert.equal(ok({ now: 1757000000 + 5 * 60 }), true, 'at the tolerance edge');
	assert.equal(ok({ now: 1757000000 + 90, toleranceSeconds: 60 }), false, 'custom tolerance');
});

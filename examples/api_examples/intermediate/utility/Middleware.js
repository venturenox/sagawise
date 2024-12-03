//Allow Cross-origin resource sharing
exports.crossOriginResource = async function (req, res, next) {

	// Website you wish to allow to connect
	if (req.headers.origin) {
		res.setHeader('Access-Control-Allow-Origin', req.headers.origin);
	}

	// Request methods you wish to allow
	res.setHeader(
		'Access-Control-Allow-Methods',
		'GET, POST, OPTIONS, PUT, PATCH, DELETE',
	);

	// Request headers you wish to allow
	res.setHeader(
		'Access-Control-Allow-Headers',
		'X-Requested-With, sentry-trace, content-type, authorization, accept',
	);

	// Set to true if you need the website to include cookies in the requests sent
	// to the API (e.g. in case you use sessions)
	res.setHeader('Access-Control-Allow-Credentials', true);

	// Pass to next layer of middleware
	if (req.method === 'OPTIONS') {
		res.sendStatus(200);
	} else {
		next();
	}
};

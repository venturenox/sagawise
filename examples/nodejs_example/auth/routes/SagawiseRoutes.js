const router = require('express').Router();
const { handleResponse } = require('../utilities/ResponseManager');
const logger = require('../utilities/Logger');

router.post('/failure_report', async (req, res) => {

	logger.error('Event Consumption Failure Reported...');
	logger.info('Event: ', req.body);

	const result = {
		status: 200,
		data: {}
	};

	handleResponse(res, { result });
});

module.exports = router;

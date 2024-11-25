const { StatusCodes } = require('http-status-codes');
const logger = require('./Logger');

module.exports = {
	/**
	 *
	 * @param {import ("express").Response} res
	 * @param {{result?, error?}} data
	 */
	handleResponse: (res, data) => {
		logger.debug({ data });
		const { result, error } = data;

		if (error) {
			res.status(error.status).json({ message: error.message, error_type: error.error_type });
		} else if (result) {
			res.status(result.status).json(result.data);
		} else {
			res.sendStatus(StatusCodes.INTERNAL_SERVER_ERROR);
		}
	},
};

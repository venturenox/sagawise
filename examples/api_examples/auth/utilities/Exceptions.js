const { StatusCodes } = require('http-status-codes');
const { JsonWebTokenError, TokenExpiredError } = require('jsonwebtoken');
const {
	ValidationError,
	NotFoundError,
	DBError,
	UniqueViolationError,
	NotNullViolationError,
} = require('objection');
const BadRequestError = require('../helpers/CustomErrors');
const logger = require('./Logger');
const { getStandardString } = require('./Utility');

/**
 * @summary Handles Objection exceptions
 * @param {object} err Objection js error object
 * @example getError(err : ValidationError)
 * @returns JavaScript object which includes message, error type, data
 */
const getError = function (err) {
	if (err instanceof UniqueViolationError) {
		return {
			status: 409,
			message:
				err.columns.length > 0
					? getStandardString(`${err.columns[0]} already registered`)
					: 'Unknown Error',
		};
	}

	if (err instanceof NotFoundError) {
		logger.debug(err);
		return {
			status: 404,
			message: err.data?.message || 'Not Found',
		};
	}

	if (err instanceof NotNullViolationError) {
		return {
			status: 400,
			message:
				err.columns.length > 0
					? getStandardString(`${err.columns[0]} cannot be null`)
					: 'Unknown Error',
		};
	}

	if (err instanceof JsonWebTokenError) {
		return {
			status: 403,
			message: 'Invalid Token',
		};
	}

	if (err instanceof TokenExpiredError) {
		return {
			status: 401,
			message: 'Token expired',
		};
	}

	if (err instanceof ValidationError) {
		return {
			status: 400,
			message: getStandardString(err.message),
		};
	}

	if (err instanceof BadRequestError) {
		logger.debug('Bad Request Error');
		return {
			status: StatusCodes.BAD_REQUEST,
			message: err.message,
		};
	}

	if (err instanceof Error) {
		if (err.name === 'BadRequestError') {
			return {
				status: StatusCodes.BAD_REQUEST,
				message: err.message,
			};
		}
	}

	if (err instanceof DBError) {
		return {
			status: StatusCodes.UNPROCESSABLE_ENTITY,
			message: err.nativeError,
		};
	}

	logger.debug('Unknown Error');

	return {
		status: 404,
		message: err.message,
	};
};

module.exports = { getError };

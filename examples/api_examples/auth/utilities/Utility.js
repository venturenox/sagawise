const bcrypt = require('bcrypt');
const UserRole = require('../database/models/UserRole');

/**
 *
 * @param {string} password Password to be validated
 */
const isPasswordValid = function (password) {
	const validationRegex =
		/^(?=.*[~!@#$%^&*()_+|\-=\\[\]{};':"\\|,.<>\\/?0-9])(?=.*[a-zA-Z]).{10,}$/g;
	return validationRegex.test(password);
};

/**
 *
 * @param {string} text plain text that has to be encrypted
 * @param {number} saltRounds number of rounds for salt
 * @returns {string} encrypted hash
 */
const getHash = async function (text, saltRounds) {
	return await bcrypt.hash(text, saltRounds);
};


/**
 *
 * @param {string} text Text that you want to capitalize
 * @example toCapitalizedCase(text)
 * @returns {string} Capitalized Text
 */

const getStandardString = function (text) {
	return text.replace(/_|:|-/g, ' ').replace(/ +(?= )/g, '');
};

/**
 *
 * @summary this function will map request path to object name
 * @param {string} requestPath Request url path
 * @returns
 */
const mapRequestPathToObject = function (requestPath) {
	return requestPath.replace('/v1/', '').split('/')[0];
};

/**
 *
 * @summary This function will map request methods to action names
 * @param {string} method Name of the request method
 * @returns
 */
const mapMethodToAction = function (method) {
	const methods = {
		POST: 'CREATE',
		GET: 'READ',
		PATCH: 'UPDATE',
		PUT: 'UPDATE',
		DELETE: 'DELETE',
	};

	return methods[method];
};

async function getOwner(tenant_id) {
	const owner = await UserRole.query()
		.findOne({ 'tenant_id': tenant_id, 'role_id': 'owner' })
		.withGraphFetched('[user, tenant]');

	return owner;
}

//helper function to remove filtered data
function filterSensitiveData(data) {
	const sensitiveFields = ['password', 'access_token', 'refresh_token', 'activation_token', 'reset_token', 'token','invite_token','googleOauthCode'];

	if (typeof data === 'object' && data !== null) {
		const filteredData = { ...data };
		for (const field of sensitiveFields) {
			if (filteredData.hasOwnProperty(field)) {
				filteredData[field] = '[**********]'
			}
		}
		return filteredData;
	}

	return data;
}
module.exports = {
	getHash,
	getStandardString,
	isPasswordValid,
	mapRequestPathToObject,
	mapMethodToAction,
	getOwner,
	filterSensitiveData
};

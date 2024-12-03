const Tenant = require('../database/models/Tenant');
const User = require('../database/models/User');
const {
	getHash,
	isPasswordValid,
} = require('../utilities/Utility');
const exceptionHandler = require('../utilities/Exceptions');
const {
	generateAccessToken,
	generateRefreshToken,
} = require('../utilities/Authentication');
const KafkaProducer = require('../utilities/KafkaProducer');
const Constants = require('../constants');
const { StatusCodes } = require('http-status-codes');
const logger = require('../utilities/Logger');
const axios = require('axios');

const register = async function (
	email,
	password,
	firstName,
	lastName,
	companyName,
) {
	
	if ( !password || !isPasswordValid(password) ) {
		return { error: { status: 400, message: 'Invalid Password' } };
	}

	if (!companyName) {
		return { error: { status: 400, message: 'Company name is required' } };
	}

	//Generates hash from plain text, using salt rounds from environment variable or default rounds 10
	const hashPassword = await getHash(
		password,
		process.env.ENCRYPTION_SALT_ROUNDS || 10
	);

	//Get domain from email
	const domain = email.split('@')[1].toLowerCase();

	let is_active = false;

	const trx = await Tenant.startTransaction();

	try {
		let tenant = await Tenant.query(trx).insert({ domain });

		const user = await User.query(trx)
			.insertGraph({
				email: email.toLowerCase(),
				password: hashPassword,
				user_role: {
					role_id: Constants.Roles.Owner,
					tenant_id: tenant.id,
				},
				is_active,
			})
			.withGraphFetched('role');

		await trx.commit();


		// Start Workflow
		const resp = await axios({
			method: 'post',
			url: process.env.SAGAWISE_URL+'/start_instance',
			params: {
				workflow_name: 'user_creation',
				workflow_version: '1.0',
			}
		});
		console.log('start_instance response: ', resp.status);
		

		const payload = {
			time_stamp: Date.now(),
			user_id: user.id,
			tenant_id: tenant.id,
			workflow_instance_id: resp.data.workflow_instance_id,
			event: process.env.USER_CREATED,
			properties: {
				id: user.id,
				first_name: firstName,
				last_name: lastName,
				email: user.email,
				user_role: user.user_role,
			},
		};

		// Publish event
		const resp2 = await axios({
			method: 'post',
			url: process.env.SAGAWISE_URL+'/update_instance',
			params: {
				workflow_instance_id: resp.data.workflow_instance_id,
				workflow_version: '1.0',
				event_name: payload.event,
				action_type: 'publish',
				is_retry: true,
			},
			data: payload
		});
		console.log('publish_event response: ', resp2.status);

		KafkaProducer.sendPayload(
			payload,
			process.env.KAFKA_EVENT_TOPIC,
			0
		);

		user['tenant_id'] = tenant.id;
		const { access_token, expiration_timestamp } =
			generateAccessToken(user);
		const refreshToken = generateRefreshToken(user);

		return {
			result: {
				status: StatusCodes.CREATED,
				data: {
					user_id: user.id,
					email:user.email,
					tenant_id: tenant.id,
					role: user.role,
					access_token: access_token,
					expiration_timestamp: expiration_timestamp,
					expires_at: expiration_timestamp,
					refresh_token: refreshToken,
					is_active: user.is_active,
				},
			},
		};
	} catch (error) {
		await trx.rollback();
		logger.error({ error });

		// check - domain conflict for custom error message
		if (
			error.name == 'UniqueViolationError' &&
			error.table == 'Tenant' &&
			error.columns?.includes('domain')
		) {
			return {
				error: {
					status: StatusCodes.CONFLICT,
					message:
						'A Company with this email domain already exists. Please ask an administrator to invite you. If you think this is an error, please reach out to our support.',
				},
			};
		}

		return { error: exceptionHandler.getError(error) };
	}
};

module.exports = { register };

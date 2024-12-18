const util = require('util');
const logger = require('./Logger');
const KafkaProducer = require('./KafkaProducer');
const Tenant = require('../database/models/Tenant');
const { Roles } = require('../constants');

const ASSESSMENT_STARTED = 'assessment_started';
const ASSESSMENT_CREATED = 'assessment_created';
const ASSESSMENT_PRECOMPLETED = 'assessment_precompleted'

const eventCallBack = async function (data) {
	logger.debug(util.inspect(data, false, null, true));
	switch (data.data.event) {
		case ASSESSMENT_PRECOMPLETED:
		case ASSESSMENT_STARTED:
			{
				const { tenant_id } = data.data.properties;
				// start an atomic query in tenant's context
				const transaction = await Tenant.startTransaction();

				try {
					const tenant = await Tenant.query(transaction).findById(
						tenant_id
					);

					if (tenant.free_assessments) {
						// use free assessment
						const updatedTenant = await Tenant.query(transaction)
							.findById(tenant_id)
							.decrement('free_assessments', 1)
							.returning('*')
							.first();

						// Update tenant status if required
						let tenantStatus = await calcTenantStatus(
							null,
							null,
							updatedTenant
						);
					}

					await transaction.commit();
				} catch (error) {
					await transaction.rollback();
					logger.error({ error });
				}
			}
			break;
		case ASSESSMENT_CREATED:
			try {
				const { user_id, tenant_id, is_active, email, assess_id } = data.data.properties;
				const invite_token = generateInviteToken(
					email,
					Roles.Candidate
				);
				const invite_link = `${process.env.TESTFUSE_WEB_ORIGIN}/assessment_invitation/?invite_token=${invite_token}&assess_id=${assess_id}`;

				if (is_active) {
					data.data.properties.activation_link = invite_link;

					KafkaProducer.sendPayload(
						{
							time_stamp: Date.now(),
							user_id: user_id,
							tenant_id: tenant_id,
							event: process.env.CANDIDATE_INVITED,
							properties: data.data.properties,
						},
						process.env.KAFKA_EVENT_TOPIC,
						0
					);
				}
				break;
			} catch (error) {
				logger.error({ error });
			}
			break;
	}
};

module.exports = { eventCallBack };

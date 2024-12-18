const { Model } = require('objection');
const path = require('path');

class Tenant extends Model {

	static get tableName() {
		return 'Tenant';
	}

	static get jsonSchema() {
		return {
			type: 'object',
			required: ['domain'],
			properties: {
				id: { type: 'integer' },
				domain: { type: 'string' },
				free_assessments: { type: 'integer', default: 0 },
				status: {
					type: 'string',
					enum: [
						'pre_onboard',
						'free',
						'pending',
						'trialing',
						'paused',
						'active',
						'past_due',
						'unpaid',
						'canceled',
						'incomplete',
						'incomplete_expired',
					],
					default: 'pre_onboard',
				},
			}
		};
	}

	static get relationMappings() {
		return {

			user_roles: {
				relation: Model.HasManyRelation,
				modelClass: path.join(__dirname, 'UserRole'),
				join: {
					from: 'Tenant.id',
					to: 'UserRole.tenant_id'
				}
			},

			users: {
				relation: Model.ManyToManyRelation,
				modelClass: path.join(__dirname, 'User'),
				join: {
					from: 'Tenant.id',
					through: {
						from: 'UserRole.tenant_id',
						to: 'UserRole.user_id',
					},
					to: 'User.id'
				}
			},

		};
	}

}

module.exports = Tenant;

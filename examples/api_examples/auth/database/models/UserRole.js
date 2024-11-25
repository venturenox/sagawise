const { Model } = require('objection');
const path = require('path');

class UserRole extends Model {
	static get tableName() {
		return 'UserRole';
	}

	static get jsonSchema() {
		return {
			type: 'object',
			required: ['role_id', 'user_id'],

			properties: {
				user_id: { type: 'number' },
				tenant_id: { type: 'number' },
				role_id: { type: 'string' }
			},
		};
	}

	static get relationMappings() {
		return {
			user: {
				relation: Model.BelongsToOneRelation,
				modelClass: path.join(__dirname, 'User'),
				join: {
					from: 'UserRole.user_id',
					to: 'User.id',
				},
			},

			tenant: {
				relation: Model.BelongsToOneRelation,
				modelClass: path.join(__dirname, 'Tenant'),
				join: {
					from: 'UserRole.tenant_id',
					to: 'Tenant.id',
				},
			},

			role: {
				relation: Model.BelongsToOneRelation,
				modelClass: path.join(__dirname, 'Role'),
				join: {
					from: 'UserRole.role_id',
					to: 'Role.id',
				},
			},

		};
	}
}

module.exports = UserRole;

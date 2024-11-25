const { Model, ValidationError } = require('objection');
const path = require('path');
const { isHashValid } = require('../../utilities/Utility');

class User extends Model {
	static get tableName() {
		return 'User';
	}

	static get jsonSchema() {
		return {
			type: 'object',
			required: ['email', 'password'],

			properties: {
				id: { type: 'integer' },
				email: { type: 'string' },
				password: { type: 'string', minLength: 6 },
				is_active: { type: 'boolean' },
				signup_source: {
					type: 'string',
					enum: ['regular', 'google'],
					default: 'regular'
				}
			},
		};
	}

	$afterInsert() {
		delete this.password;
	}

	$afterUpdate() {
		delete this.password;
	}

	async $afterFind(context) {
		if (!context.password) {
			delete this.password;
			return;
		}
		const isPasswordValid = await isHashValid(
			context.password,
			this.password
		);

		if (!isPasswordValid) {
			throw new ValidationError({ message: 'Invalid email or password' });
		}

		delete this.password;
	}

	static get relationMappings() {
		return {
			user_role: {
				relation: Model.HasOneRelation,
				modelClass: path.join(__dirname, 'UserRole'),
				join: {
					from: 'User.id',
					to: 'UserRole.user_id',
				},
			},

			tenant: {
				relation: Model.HasOneThroughRelation,
				modelClass: path.join(__dirname, 'Tenant'),
				join: {
					from: 'User.id',
					through: {
						from: 'UserRole.user_id',
						to: 'UserRole.tenant_id',
					},
					to: 'Tenant.id',
				},
			},

			role: {
				relation: Model.HasOneThroughRelation,
				modelClass: path.join(__dirname, 'Role'),
				join: {
					from: 'User.id',
					through: {
						from: 'UserRole.user_id',
						to: 'UserRole.role_id',
					},
					to: 'Role.id',
				},
			},

		};
	}
}

module.exports = User;

const { Model } = require('objection');
class Role extends Model {

	static get tableName() {
		return 'Role';
	}

	static get jsonSchema() {

		return {
			type: 'object',
			required: ['name'],

			properties: {
				id: { type: 'string' },
				name: { type: 'string' },
			}

		};

	}

}

module.exports = Role;
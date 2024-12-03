const { Model } = require("objection");
class UserProfile extends Model {
	static get tableName() {
		return "UserProfile";
	}

	static get idColumn() {
		return "user_id";
	}

	static get jsonSchema() {
		return {
			type: "object",
			required: ["user_id"],

			properties: {
				user_id: { type: "integer" },
				email: { type: "string" },
				first_name: { type: "string" },
				last_name: { type: "string" },
			},
		};
	}
}

module.exports = UserProfile;

const { Model } = require("objection");
class TenantProfile extends Model {
	static get tableName() {
		return "TenantProfile";
	}

	static get idColumn() {
		return "tenant_id";
	}

	static get jsonSchema() {
		return {
			type: "object",
			required: ["tenant_id", "company_name", "company_domain"],

			properties: {
				tenant_id: { type: "integer" },
				company_name: { type: "string" },
				company_domain: { type: "string" },
			},
		};
	}
}

module.exports = TenantProfile;

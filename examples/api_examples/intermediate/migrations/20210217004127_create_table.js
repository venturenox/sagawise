/**
 * @param { import("knex").Knex } knex
 * @returns { Promise<void> }
 */
exports.up = function (knex) {
	return knex.schema
		.createTable("TenantProfile", (t) => {
			t.integer("tenant_id").primary();
			t.string("company_name").defaultTo("");
			t.string("company_domain").defaultTo("");
			t.string("industry").defaultTo("");
			t.string("legal_address").defaultTo("");
			t.string("state").defaultTo("");
			t.string("country").defaultTo("");
			t.string("zip_code").defaultTo("");
			t.string("phone").defaultTo("");
			t.string("image_url").nullable();
		})
		.createTable("UserProfile", (t) => {
			t.integer("user_id").primary();
			t.string("email").defaultTo("");
			t.string("first_name").defaultTo("");
			t.string("last_name").defaultTo("");
			t.string("legal_address").defaultTo("");
			t.string("state").defaultTo("");
			t.string("country").defaultTo("");
			t.string("zip_code").defaultTo("");
			t.string("phone").defaultTo("");
			t.string("image_url").nullable();
			t.string("is_active").defaultTo(false);
		});
};

/**
 * @param { import("knex").Knex } knex
 * @returns { Promise<void> }
 */
exports.down = function (knex) {
	return knex.schema
		.dropTable("TenantProfile")
		.dropTable("UserProfile");
};

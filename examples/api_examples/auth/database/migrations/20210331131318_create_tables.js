/**
 *
 * @param {import ("knex").Knex} knex
 * @returns
 */
exports.up = function (knex) {
	return knex.schema
		.createTable('Tenant', (t) => {
			t.increments();
			t.string('domain').notNullable();
			t.timestamps(true, true, false);
			t.integer('free_assessments').defaultTo(0);
			t.string('status').defaultTo('pre_onboard');
		})
		.createTable('User', (t) => {
			t.increments();
			t.string('email').unique().notNullable();
			t.string('password').notNullable();
			t.boolean('is_active').notNullable().defaultTo(false);
			t.timestamps(true, true, false);
			t.enu('signup_source', ['regular', 'google'], {
				useNative: true,
				enumName: 'signup_source',
			}).defaultTo('regular');
		})
		.createTable('Role', (t) => {
			t.string('id').primary();
			t.string('name').unique().notNullable();
		})
		.createTable('UserRole', (t) => {
			t.increments();
			t.integer('user_id')
				.references('id')
				.inTable('User')
				.onDelete('CASCADE')
				.onUpdate('CASCADE')
				.notNullable();
			t.integer('tenant_id')
				.references('id')
				.inTable('Tenant')
				.onDelete('CASCADE')
				.onUpdate('CASCADE');
			t.string('role_id')
				.references('id')
				.inTable('Role')
				.onDelete('CASCADE')
				.onUpdate('CASCADE')
				.notNullable();
			t.unique(['user_id']);
			t.timestamps(true, true, false);
		});
};
exports.down = function (knex) {
	return knex.schema
		.dropTable('Tenant')
		.dropTable('User')
		.dropTable('Role')
		.dropTable('UserRole');
};

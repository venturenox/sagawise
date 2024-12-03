/**
 *
 * @param {import ("knex").Knex} knex
 */
exports.seed = async function (knex) {
	try {
		await knex('Tenant')
			.insert([{ id: 1000, domain: "testfuse.com" }, { id: 1001, domain: "imroz.com" }])
			.onConflict('id')
			.ignore();

		await knex.raw(
			'SELECT setval(\'"Tenant_id_seq"\', (SELECT max(id) from "Tenant"))'
		);
	} catch (err) {
		console.log(err);
	}
};

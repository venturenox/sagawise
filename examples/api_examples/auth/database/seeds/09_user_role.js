exports.seed = async function (knex) {
	try {
		await knex('UserRole')
			.insert([
				{
					id: 1000,
					user_id: 1000,
					tenant_id: 1001,
					role_id: 'super_admin'
				},
			])
			.onConflict('id')
			.ignore();

		await knex.raw(
			'SELECT setval(\'"UserRole_id_seq"\', (SELECT max(id) from "UserRole"))'
		);
	} catch (err) {
		console.log(err);
	}
};

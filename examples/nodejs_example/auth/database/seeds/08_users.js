/**
 *
 * @param {import ("knex").Knex} knex
 */
exports.seed = async function (knex) {
	try {
		await knex('User')
			.insert([
				{
					id: 1000,
					email: 'admin@testfuse.com',
					password:
						'$2b$10$kofHYn9TELcT.nPoM2D1AeF0.8N8TxqTXLRtYTDa3ozC3F/baVz.6',
				},
			])
			.onConflict('id')
			.merge();

		await knex.raw('SELECT setval(\'"User_id_seq"\', (SELECT max(id) from "User"))');

	} catch (err) {
		console.log(err);
	}
};

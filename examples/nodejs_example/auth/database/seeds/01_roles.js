exports.seed = async function (knex) {

	try {

		await knex('Role').insert([
			{ id: 'super_admin', name: 'Super Admin' },
			{ id: 'owner', name: 'Owner' },
			{ id: 'admin', name: 'Admin' },
			{ id: 'recruiter', name: 'Recruiter' },
			{ id: 'member', name: 'Member' },
			{ id: 'candidate', name: 'Candidate' },
			{ id: 'hiring_manager', name: 'Hiring Manager' },
			{ id: 'public', name: 'Public' },
		]).onConflict('id').merge();

	} catch (err) {
		console.log(err);
	}

};

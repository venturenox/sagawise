const FREE_ASSESSMENTS = 15;

const Roles = Object.freeze({
	'Super Admin': 'super_admin',
	Owner: 'owner',
	Admin: 'admin',
	Recruiter: 'recruiter',
	Hiring_manager: 'hiring_manager',
	Member: 'member',
	Candidate: 'candidate',
	Public: 'public',
});

module.exports = {
	Roles,
	FREE_ASSESSMENTS,
};

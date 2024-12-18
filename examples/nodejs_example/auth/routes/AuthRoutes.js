const router = require('express').Router();
const authController = require('../controllers/AuthController');
const { handleResponse } = require('../utilities/ResponseManager');

router.post('/register', async (req, res) => {
	const { email, password, first_name, last_name, company_name } = req.body;
	
	const data = await authController.register(
		email,
		password,
		first_name,
		last_name,
		company_name,
	);

	handleResponse(res, data);
});

module.exports = router;

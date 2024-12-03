const UserProfile = require("../models/UserProfile");
const { getError } = require("../utility/ErrorHandler");
const { StatusCode } = require("../utility/KeyMaster");

exports.create = async ({ id: user_id, email, first_name, last_name }) => {
	try {
		const data = await UserProfile.query().insert({
			user_id,
			email,
			first_name,
			last_name,
		});

		return {
			result: {
				status: StatusCode.CREATED,
				data: data,
			},
		};
	} catch (err) {
		return { error: getError(err) };
	}
};

exports.patch = async ({
	id: user_id,
	first_name,
	last_name,
	legal_address,
	state,
	country,
	zip_code,
	phone,
	image_url,
	is_active,
}) => {
	try {
		const user = {
			first_name,
			last_name,
			legal_address,
			state,
			country,
			zip_code,
			phone,
			image_url,
			is_active
		};

		const data = await UserProfile.query()
			.patchAndFetchById(user_id, user)
			.throwIfNotFound({ message: "Invalid user id" });

		return {
			result: {
				status: StatusCode.SUCCESS,
				data: data,
			},
		};
	} catch (err) {
		return { error: getError(err) };
	}
};

exports.delete = async (user_id) => {
	try {
		const data = await UserProfile.query().deleteById(user_id);

		return {
			result: {
				status: StatusCode.SUCCESS,
				data: data,
			},
		};
	} catch (err) {
		return { error: getError(err) };
	}
};

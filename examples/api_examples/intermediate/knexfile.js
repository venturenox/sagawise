module.exports = {
	development: {
		client: "pg",
		connection: "postgres://" + process.env.POSTGRES_USERNAME + ":" + process.env.POSTGRES_PASSWORD + "@"
			+ process.env.POSTGRES_HOST + "/" + process.env.POSTGRES_DATABASE,
		migrations: {
			directory: "./migrations",
		},
	},
};

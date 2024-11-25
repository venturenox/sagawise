module.exports = {
	client: 'pg',
	connection: `postgres://${process.env.POSTGRES_USERNAME}:${process.env.POSTGRES_PASSWORD}@${process.env.POSTGRES_HOST}/${process.env.POSTGRES_DATABASE}?timezone=utc`,
	migrations: {
		directory: __dirname + '/database/migrations',
	},
	seeds: {
		directory: __dirname + '/database/seeds',
	},
};

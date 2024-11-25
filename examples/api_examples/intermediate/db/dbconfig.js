const Knex = require("knex");
const knexConfig = require("../knexfile");
const { Model } = require("objection");
const knex = Knex(knexConfig.development);


exports.dbConnection = () => {
	Model.knex(knex);
	console.log("DB Created");
};
exports.destroyDB = () => {
	knex.destroy();
};

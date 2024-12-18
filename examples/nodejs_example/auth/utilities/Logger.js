const winston = require('winston');

const logger = winston.createLogger({
	// level: 'error',
	levels: {
		emerg: 0,
		alert: 1,
		crit: 2,
		error: 3,
		warn: 4,
		info: 5,
		http: 6,
		verbose: 7,
		debug: 8,
		trace: 9,
		silly: 10
	},
	format: winston.format.combine(
		winston.format.timestamp({ format: 'YYYY-MM-DD HH:mm:ss' }),
		winston.format.prettyPrint({ colorize: true }),
		winston.format.colorize({ all: true }),
		winston.format.printf((info) => {
			const {
				timestamp, level, message, ...args
			} = info;

			return `${timestamp} [${level}]: ${message} ${Object.keys(args).length ? JSON.stringify(args, null, 2) : ''}`;

			// if (process.env.LOG_TIMESTAMP === 'true') {
			// 	return `${timestamp} [${level}]: ${message} ${Object.keys(args).length ? JSON.stringify(args, null, 2) : ''}`;
			// } else {
			// 	return `[${level}]: ${message} ${Object.keys(args).length ? JSON.stringify(args, null, 2) : ''}`;
			// }

		}),
	),
	// format: winston.format.simple(),
	// defaultMeta: {service: 'auth'},
	transports: [
		new winston.transports.Console({ level: process.env.LOG_LEVEL || 'silly' }),
	]

});

global.logger = logger;

module.exports = logger;
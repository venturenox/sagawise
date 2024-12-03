const express = require('express');
const {	crossOriginResource } = require('./utilities/Middleware');
const dbConfig = require('./database/DatabaseConfig');
const routes = require('./routes');

const { startConsumer } = require('./utilities/KafkaConsumer');
const { eventCallBack } = require('./utilities/KafkaEvent');

const app = express();

//Initialize Database
dbConfig.initializeDB();

app.use(express.urlencoded({ extended: true }));

app.use(express.json({
	verify: (req, res, buf) => {
		req.rawBody = buf;
	}
}));

//app routes
app.use(crossOriginResource);
app.use(routes);

startConsumer(eventCallBack, {
	topic: process.env.KAFKA_EVENT_TOPIC,
	group: process.env.KAFKA_AUTH_GROUP_ID,
	client: 'omers_event_stream',
}).catch((err) => {
	console.error(`Kafka Consumer Error:${err}`);
});

//Configure app on port
app.listen(process.env.PORT, () => {
	console.log(`Server Started on Port ${process.env.PORT}`);
});

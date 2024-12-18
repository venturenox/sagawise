const express = require('express');

const { startConsumer } = require('./utility/Consumer');
const {
	streamProcessor,
} = require('./utility/StreamManager');
const {	crossOriginResource } = require('./utility/Middleware');
const routes = require('./routes');

const app = express();

app.use(express.urlencoded({ extended: false }));
app.use(express.json({
	verify: (req, res, buf) => {
		req.rawBody = buf;
	}
}));

//app routes
app.use(crossOriginResource);
app.use(routes);

//kafka consumer for eventStreme Topic
startConsumer(streamProcessor, {
	topic: process.env.KAFKA_EVENT_TOPIC,
	group: process.env.KAFKA_FINAL_GROUP_ID,
	client: process.env.KAFKA_FINAL_EVENT_CLIENT,
}).catch((err) => {
	console.error(`Kafka Consumer Error:${err}`);
	// process.exit(1);
});

app.listen(process.env.PORT, () => {
	console.log(`Server Started on Port ${process.env.PORT}`);
});

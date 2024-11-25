# Saga wise

![sagawise platform logo](sdk/sagawise-platform-logo-1024x641-removebg-preview.png)

**Saga wise** is a distributed transaction management tool based on the Saga pattern for managing long-running transactions. It helps coordinate the distributed workflow across services by tracking each task's status and ensuring fault tolerance using compensating transactions. The project is built using **Go-Lang**, **Redis**, and **PostgreSQL** to handle scalability and durability.

[Website](https://venturenox.com/work/sagawise/) • [Documentation](https://github.com/venturenox/wtfsaga/tree/main)

![Sagawise Example Visualization](https://venturenox.com/wp-content/uploads/2024/05/Sagawise-architecture-1024x592.png)

## Table of Contents
<!-- @NOTE: to be added after release of packages at NPM and PyPi -->
<!-- - [SDKs:](#)
	- [SDK - Node JS](#)
		- [NPM Installation](#)
	- [SDK - Python](#)
		- [pip installation](#)
	- [Node JS ](#)
	- [Python ](#) -->

- [Getting Started](#getting-started)
- [DSL Files](#dsl-files)
- [Services.json file](#servicesjson-file)
- [Docker Deployment](#docker-deployment)
- [Helm Chart](#helm-chart)
- [PostMan Collection](#postman-collection)
- [Examples](#examples)
	- [Raw API ](#raw-api)
- [Tech Stack](#tech-stack)
- [Roadmap](#roadmap)
- [License](#license)
- [Contributors](#contributors)

---

## Getting Started

1. **Set Environment Variables**
   - For the `dev` environment, configure variables inside the `.env` file.
   - For production and other environments, ensure variables are defined as per environment-specific settings.

2. **Define Workflows in DSL Files**
   - Workflow DSL files are stored in `/backend/sagawise/` as JSON files.
   - The format and rules for these files are explained below - [DSL Files](#dsl-files).

3. **Run the Project**
   - Start the containers and necessary services by using the `make` command.

4. **Start Workflow Instance**
   - Use the `start_instance` endpoint of **Saga wise** to initialize a workflow from the publishing service.

5. **Publish Event**
   - Inform **Saga wise** about a `publish` event by sending a request to the "Publish Event" endpoint from the publishing service.

6. **Consume Event**
   - Inform **Saga wise** about a `consume` event by sending a request to the "Consume Event" endpoint from the publishing service.

7. **Failure Reporting**
   - Ensure every service has a failure report webhook registered to handle task failures. This should follow the format: `/v1/sagawise/failure_report/`.

8. **Dashboard Monitoring**
   - Use **Saga wise** API dashboard to get an overview of workflows and track events.

9. **Logging and Debugging**
   - Access logs to debug and monitor runtime messages.

---

## DSL Files
The purpose of the DSL is to define a workflow(s) of Sagas among different services so that they can registered with SagaWise in order to monitored.

Consider the following requirements while creating your DSL Files:

1. Each Workflow should have it's own DSL file.
2. File naming convention should be: `workflow_name.json`
 	Note: Workflow names should NOT container spaces. Spaces must be converted into underscores `_`
4. First root key must be `workflow`
5. Inside `workflow` key, there must be following keys:
	1. `version` defines version of workflow.
	2. `schema_version` defines version of the schema to be used by the DSL files. Current value `1.0`
	3. `name` defines the name of workflow (without spaces).
6. Last key inside the root "workflow" object, must be the `tasks` key, which is an array of objects.
7. Inside each task object, there must be these keys:
	1. `topic` defines the Kafka topic on which this task is publishing and consuming messages.
	2. `from` defines the name of service which producer the message in this task (see below heading).
	3. `to` defines the name of service which (is supposed to) consume the message in this task (see below heading).
	4. `timeout` defines the timeout for the consuming service to consume task, or the time the task should be completed in. Value is integer type and time is in `milliseconds`.

---

## Services.json file

This file defines the participating services. Each object must follow this format:

1. Service names MUST be defined in `services.json` file as array of objects.
2. Each object defined in "services.json" file MUST follow this syntax:
	1. `service_name` defines the name of service.
	2. `failure_url` defines the url of failure endpoint.
3. Following rules apply to the service name:
	1. It should NOT contain **whitespaces**.
	2. It MUST be **small-case**.

---

## Docker Deployment

For docker based deployment check readme [here]()

---

## Helm Chart

For Helm based deployment check readme [here]()

---

## Postman Collection

PostMan collection with all the raw APIs and documentation are avaiable in the `docs` directory. [Collection](/docs/WTFSaga.postman_collection.json) - [Enviroment](/docs/WTFSaga-Env.postman_environment.json)

---

## Examples

### Raw API
Implementation with Raw API can be observered [here](https://github.com/venturenox/wtfsaga/tree/main/examples/api_examples)

---

## Tech Stack

- **Go-Lang**: Core programming language for building high-performance services.
- **Redis**: In-memory datastore for fast data access.
- **PostgreSQL**: Relational database for managing transactions and persistent data.

---

## License

This project is under Apache license. [license](/LICENSE.txt)

---

## Roadmap

The following features are currently in th pipeline:
- [ ] Dashboard Frontend
- [ ] AsyncAPI for Workflow definition
- [ ] zero trust service authentication with mTLS using spiffe protocol
- [ ] gRPC endpoints
- [ ] Integrate K8s service discovery layer
- [ ] Integration with schema registry
- [ ] Advanced dashboard

---

### Contributors

<div align="left">
  <a href="https://github.com/saad-akhtar26">
    <img src="https://avatars.githubusercontent.com/u/116262387?v=4" width="100" style="border-radius: 50%;" alt="Saad Akhtar">
  </a>
  <a href="https://github.com/AmmarSaqib">
    <img src="https://avatars.githubusercontent.com/u/22831978?v=4" width="100" style="border-radius: 50%;" alt="Ammar Saqib">
  </a>
  <a href="https://github.com/stingerpk">
    <img src="https://avatars.githubusercontent.com/u/9607103?v=4" width="100" style="border-radius: 50%;" alt="Stinger PK">
  </a>
  <a href="https://github.com/nob786">
    <img src="https://avatars.githubusercontent.com/u/44703244?v=4" width="100" style="border-radius: 50%;" alt="Nob 786">
  </a>
</div>
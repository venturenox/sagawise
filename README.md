# Saga wise

<p align="center">

![sagawise platform logo](sdk/sagawise-platform-logo-1024x641-removebg-preview.png)

</p>

<!-- <p align="center">

   [![Helm Chart](https://img.shields.io/badge/Helm-0F1689?logo=helm&logoColor=fff)](https://artifacthub.io/packages/helm/venturenox/sagawise)
   [![Join Slack](https://img.shields.io/badge/join%20slack-sagawise-brightgreen.svg)](https://venturenox.slack.com/)
   [![Documentation link](https://img.shields.io/badge/Documentation-link-blue?logo=gitbook)](https://github.com/venturenox/sagawise/tree/main)
   [![Docker Pulls](https://img.shields.io/docker/pulls/venturenox/sagawise)](https://hub.docker.com/r/venturenox/sagawise/tags)
   [![PyPI](https://img.shields.io/badge/PyPI-3775A9?logo=pypi&logoColor=fff)](https://pypi.org/project/postgres)
   [![Postgres](https://img.shields.io/pepy/dt/postgres)](https://pypi.org/project/postgres)
</p> -->

**Saga wise** is a distributed transaction management tool based on the Saga pattern for managing long-running transactions.
It helps coordinate the distributed workflow across services by tracking each task's status and ensuring fault tolerance using compensating transactions.
The project is built using **Go-Lang**, **Redis**, and **PostgreSQL** to handle scalability and durability.

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

   - Workflow DSL files are stored in [dsl files](/backend/sagawise/) as JSON files.
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
3. First root key must be `workflow`
4. Inside `workflow` key, there must be following keys:
   1. `version` defines version of workflow.
   2. `schema_version` defines version of the schema to be used by the DSL files. Current value `1.0`
   3. `name` defines the name of workflow (without spaces).
5. Last key inside the root "workflow" object, must be the `tasks` key, which is an array of objects.
6. Inside each task object, there must be these keys:
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

## Docker

### Quick Start

Instantly start the sagawise container by configuring environment variables of Redis and Postgres.

```bash
docker run --detach \
--name sagawise --port 5000:5000 \
--env=REDIS_HOST="redis host" \
--env=REDIS_PORT=6379 \
--env=REDIS_PASSWORD="redis password" \
--env=POSTGRES_HOST="postgres host" \
--env=POSTGRES_PORT=5432 \
--env=POSTGRES_USERNAME="postgres" \
--env=POSTGRES_PASSWORD="postgres password" \
--env=POSTGRES_DATABASE="sagawise" \
venturenox/sagawise:latest
```

Open a shell in the sagawise container

```bash
docker exec -it sagawise /bin/sh
```

Minimalistic docker compose configuration for running sagawise with self/externally managed databases.

```yaml
version: "3.8"
services:
  sagawise:
  build:
    context: .
    dockerfile: backend/Dockerfile
  container_name: sagawise
  restart: always
  ports:
    - 5000:5000
  environment:
    SERVER_ENV: development
    REDIS_HOST: $REDIS_HOST
    REDIS_PORT: $REDIS_PORT
    POSTGRES_USERNAME: $POSTGRES_USERNAME
    POSTGRES_PASSWORD: $POSTGRES_PASSWORD
    POSTGRES_HOST: postgres
    POSTGRES_PORT: $POSTGRES_PORT
    POSTGRES_DATABASEB: $POSTGRES_DATABASE

  adminer:
    image: adminer
    container_name: adminer
    restart: always
    ports:
      - $MACHINE_ADMINER_PORT:$ADMINER_PORT
    environment:
      ADMINER_DEFAULT_SERVER: postgres

  redisinsight:
    image: redis/redisinsight
    container_name: redosinsight
    restart: always
    ports:
      - $REDIS_INSIGHT_MACHINE_PORT:$REDIS_INSIGHT_PORT
```

## NOTE

If docker compose is used, the environment variables can be set from the **.env** file in the root directory.

### **Environment Variables**

```markdown
The image supports the following environment variables:

| Variable                  | Description                                                      | Default Values                           |
| ------------------------- | ---------------------------------------------------------------- | ---------------------------------------- |
| `REDIS_CONNECTION_STRING` | Connection string for Redis database                             | `redis://redis:6379`                     |
| `REDIS_HOST`              | Host for Redis database                                          | `redis`                                  |
| `REDIS_PORT`              | Port for Redis database                                          | `6379`                                   |
| `REDIS_PASSWORD`          | Password for Redis database                                      | `nill`                                   |
| `POSTGRES_HOST`           | Host for Postgres database                                       | `postgres`                               |
| `POSTGRES_PORT`           | Port for Postgres database                                       | `5432`                                   |
| `POSTGRES_USERNAME`       | Username for Postgres database                                   | `postgres`                               |
| `POSTGRES_PASSWORD`       | Password for Postgres database                                   | `password`                               |
| `POSTGRES_DATABASE`       | Database to be used sagawise                                     | `sagawise`                               |
| `SERVER_ENV`              | Specifies the environment running the Sagawise app ( optional ). | `development, preview,stagin,production` |
```

### **Volumes**

```markdown
The image supports mounting the following volumes:

| Database service in compose | Description             | Path in Container          |
| --------------------------- | ----------------------- | -------------------------- |
| Postgres                    | Stores application data | `/var/lib/postgresql/data` |
```

For quick deployment you can use the docker-compose file from the root directory [`docker-compose.yml`](./docker-compose.yml).

[Visit our official Dockerhub repository](https://hub.docker.com/r/venturenox/sagawise)

## Helm Chart

For Helm based deployment check [README](./kubernetes/helm/sagawise/README.md)

---

## Postman Collection

PostMan collection with all the raw APIs and documentation are avaiable in the [docs directory](./docs/).

---

## Examples

### Raw API

Implementation with Raw API can be observered in [README](./examples/api_examples/README.md)

---

## Tech Stack & Open Source tools

- [**Go-Lang**](https://github.com/golang/go): Core programming language for building high-performance services.
- [**Redis**](https://hub.docker.com/r/redis/redis-stack-server): In-memory datastore for fast data access.
- [**PostgreSQL**](https://hub.docker.com/r/bitnami/postgresql): Relational database for managing transactions and persistent data.
- [**Adminer**](https://hub.docker.com/_/adminer): Relational database management tool.
- [**Redis Insight**](https://hub.docker.com/r/redis/redisinsight): Dashboard for redis databases.

---

## License

This project is under Apache license. [license](/LICENSE.txt)

---

## Roadmap

The following features are currently in the pipeline:

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

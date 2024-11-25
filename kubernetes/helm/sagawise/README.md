<!--- app-name: Sagawise -->

# Venturenox package for Sagawise

Saga wise is a distributed transaction management tool based on the Saga pattern for managing long-running transactions. It helps coordinate the distributed workflow across services by tracking each task's status and ensuring fault tolerance using compensating transactions. The project is built using Go-Lang, Redis, and PostgreSQL to handle scalability and durability.

[Overview of Sagawise](https://github.com/venturenox/sagawise)

## TL;DR

```console

helm repo add "repo_name" "repo_address"

helm dependency update

helm upgrate --install my-release "registry-to-be-added" ( NEED TO UPDATE THIS )
```

## Introduction

This chart bootstraps [Sagawise](https://github.com/venturenox/sagawise) Deployment with [Redis](https://redis.io/) and [PostgreSQL](https://www.postgresql.org/) on a [Kubernetes](https://kubernetes.io) cluster using the [Helm](https://helm.sh) package manager.

## Prerequisites

- Kubernetes 1.23+
- Helm 3.8.0+
- PV provisioner support in the underlying infrastructure

## Dependencies

This Helm chart has the following dependencies:

1. **PostgreSQL**:

   - The chart can deploy an internal PostgreSQL instance or connect to an external PostgreSQL database. If you choose to use an internal database, make sure to configure the `postgresql.enabled` flag.
   - If using an external PostgreSQL instance, configure the `externalPostgresql` parameters.

2. **Redis**:
   - The chart can also deploy an internal Redis instance or connect to an external Redis server. If you choose to use an internal Redis instance, make sure to configure the `redis.enabled` flag.
   - If using an external Redis instance, configure the `externalRedis` parameters.

Make sure the following are set according to your use case:

- **Redis** and **PostgreSQL** settings in `values.yaml` (internal vs external).
- Network access and authentication credentials for connecting to external services.

## Installing the Chart

To install the chart with the release name `my-release`:

```console
helm install my-release oci://REGISTRY_NAME/REPOSITORY_NAME/sagawise ( NEED TO UPDATE THIS )
```

> Note: You need to substitute the placeholders `REGISTRY_NAME` and `REPOSITORY_NAME` with a reference to your Helm chart registry and repository. For example, in the case of Venturenox, you need to use `REGISTRY_NAME=registry-1.docker.io` and `REPOSITORY_NAME=bitnamicharts`. ( NEED TO UPDATE THIS )

These commands deploy sagawise on the Kubernetes cluster in the default configuration. The [Parameters](#parameters) section lists the parameters that can be configured during installation.

> **Tip**: List all releases using `helm list` or `helm ls --all-namespaces`

### Ingress

This chart provides support for Ingress resources. If you have an ingress controller installed on your cluster, such as [nginx-ingress-controller](https://github.com/bitnami/charts/tree/main/bitnami/nginx-ingress-controller) you can utilize the ingress controller to serve your application.To enable Ingress integration, set `ingress.enabled` to `true` for the http ingress.

The most common scenario is to have one host name mapped to the deployment. In this case, the `ingress.hostname` property can be used to set the host name. The `ingress.tls.secretName` parameter can be used to add the TLS configuration for this host.

[Learn more about Ingress controllers](https://kubernetes.io/docs/concepts/services-networking/ingress-controllers/).

### Example Quickstart Sagawise Configuration

```yaml
# Declare variables to be passed into your templates.

image:
  repository: "venturenox/sagawise"
  pullPolicy: "IfNotPresent"
  tag: "latest"

serviceAccount:
  create: false
  name: ""

service:
  type: ClusterIP
  port: 80

ingress:
  enabled: true
  className: "nginx"
  annotations:
    # kubernetes.io/ingress.class: nginx
    # cert-manager.io/cluster-issuer: letsencrypt-prod
    # cert-manager.io/acme-challenge-type: dns01
    # cert-manager.io/acme-dns01-provider: cloudflare
    # nginx.ingress.kubernetes.io/enable-cors: "false"
    # nginx.ingress.kubernetes.io/proxy-body-size: 50m
  hosts:
    - host: api-sagawise.example.com
      paths:
        - path: /
          pathType: ImplementationSpecific
  tls:
    - secretName: sagawise.tls
      hosts:
        - api-sagawise.example.com

postgresql:
  enabled: false
  auth:
    database: ""
    postgresPassword: ""

redis:
  enabled: false
  replica:
    replicaCount: 1

# External PostgreSQL Parameters
externalPostgresql:
  host: "db.example.com"
  username: "admin"
  password: "secretpassword"
  database: "sagawise"

# External Redis Parameters
externalRedis:
  host: "redis.example.com"
  password: "redispassword"
```

## Parameters

### Applicaton Parameters

| Name                    | Description                                            | Default Values         |
| ----------------------- | ------------------------------------------------------ | ---------------------- |
| `image.repository`      | Docker image repository for the Sagawise application   | `nventurenox/sagawise` |
| `image.pullPolicy`      | Docker image pull policy                               | `IfNotPresent`         |
| `image.tag`             | Tag for the Docker image                               | `latest`               |
| `serviceAccount.create` | Whether to create a service account for the deployment | `false`                |
| `serviceAccount.name`   | The name of the service account to use                 | `"nil`                 |

### Ingress Parameters

| Name                        | Description                                   | Default Values          |
| --------------------------- | --------------------------------------------- | ----------------------- |
| `ingress.enabled`           | Enable or disable ingress configuration       | `true`                  |
| `ingress.className`         | The ingress controller to use (e.g., "nginx") | `nginx`                 |
| `ingress.hosts[0].host`     | Hostname for the ingress                      | `subdomain.example.com` |
| `ingress.tls[0].secretName` | Name of the TLS secret for secure connections | `sagawise.tls`          |

### Redis Parameters

| Name                         | Description                                                    | Default Values |
| ---------------------------- | -------------------------------------------------------------- | -------------- |
| `redis.enabled`              | Enable or disable the deployment of an internal Redis instance | `true`         |
| `redis.replica.replicaCount` | Number of Redis replicas to deploy if `redis.enabled` is true  | `1`            |

### External Redis Parameters

| Name                     | Description                                                                                | Default Values |
| ------------------------ | ------------------------------------------------------------------------------------------ | -------------- |
| `externalRedis.host`     | Hostname or IP address of an external Redis instance                                       | `nil`          |
| `externalRedis.username` | Hostname or IP address of an external Redis instance                                       | `nil`          |
| `externalRedis.password` | Password for accessing the external Redis instance (required if authentication is enabled) | `nil`          |
| `externalRedis.database` | Hostname or IP address of an external Redis instance                                       | `sagawise`     |

### PostgreSQL parameters

| Name                                   | Description                                                          | Default Values |
| -------------------------------------- | -------------------------------------------------------------------- | -------------- |
| `postgresql.enabled`                   | Enable or disable the deployment of an internal PostgreSQL database. | `true`         |
| `postgresql.auth.database`             | Name of the database to create within PostgreSQL.                    | `sagawise`     |
| `postgresql.auth.postgresPassword`     | Password for the PostgreSQL superuser.                               | `password`     |
| `postgresql.passwordUpdateJob.enabled` | Job for updating the password for postgres user.                     | `true`         |

### External PostgreSQL parameters

| Name                          | Description                                                                    | Default Values |
| ----------------------------- | ------------------------------------------------------------------------------ | -------------- |
| `externalPostgresql.host`     | Hostname or IP address of an external PostgreSQL database                      | `nil`          |
| `externalPostgresql.username` | Username to access the external PostgreSQL database                            | `nil`          |
| `externalPostgresql.password` | Password for the specified username to access the external PostgreSQL database | `nil`          |
| `externalPostgresql.database` | Name of the database in the external PostgreSQL instance                       | `sagawise`     |

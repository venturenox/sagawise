# Development

Read this before changing code. It covers the repository layout, how to build, the test tiers and the rules they follow, CI, and the documentation site. What the code does is in the [architecture](architecture.md); what it must do is in the [contract](contract.md).

## Layout

| Path | Contents |
| --- | --- |
| `backend/` | The Go module (`wtfsaga`, the project's former name; imports read `wtfsaga/...`). `main.go` is the composition root. |
| `backend/sagawise/` | Workflow DSL files, one JSON file per workflow. |
| `services.json` | Service registry: service name to failure URL. |
| `sdk/nodejs/`, `sdk/python/` | Thin clients over the HTTP API, with webhook signature verification. |
| `examples/order_flow/` | Three services, two tasks, the smallest working saga. Read its README for the protocol. |
| `examples/api_examples/` | Four services with databases, a fan-out workflow. |
| `charts/sagawise/` | Helm chart. |
| `deploy/` | Operator artifacts outside the chart: Prometheus alert rules. |
| `benchmarks/` | Benchmark runs, comparisons and the plot script. Method in [benchmarks](benchmarks.md). |
| `docs/` | This site. |

## Running it

Everything runs through Docker Compose, driven by the root `Makefile`:

```bash
make start      # build and start sagawise :5000, postgres :5432, redis :6379, adminer :8090, redisinsight :5540
make stop       # stop
make restart    # stop, rebuild, start
make clean      # also remove volumes, local images and the network
make order_flow # core stack plus the three example services
```

Settings come from `.env` at the repository root; see [configuration](configuration.md). The image bakes in the DSL files and `services.json`, so **changing either needs `make restart`**, not a container restart. A change to the instance document layout needs `make clean`, since there is no migration of old documents.

Prerequisites: Docker with Compose, Go (the version in `backend/go.mod`), Node and Python 3 for the SDK tests, `helm` only if you touch the chart. A `.venv/` at the repository root, when present, is used for every Python step (docs site and SDK tests).

## Build and test

| Command | What runs | Needs |
| --- | --- | --- |
| `cd backend && go build ./...` | Compile check. | Go |
| `make test` | Unit tests with the race detector. The Docker build runs the same tests, so a failing unit test breaks `make start`. | Go |
| `make test-integration` | Unit plus integration and contract tests (`//go:build integration`) against the running stack on localhost. | `make start` |
| `make test-sdk` | Node (`node --test`) and Python (pytest) SDK tests. | Node, Python |
| `make test-status` | Lists contract tests still marked expected-to-fail, grouped by finding. Empty today. | `make start` |
| `make bench BENCH_LABEL=<label>` | Benchmark run. Required for any change that claims a performance effect; see [benchmarks](benchmarks.md). | `make start` |

Integration tests address the stores through the same `REDIS_*` and `POSTGRES_*` variables the binary reads, defaulting to localhost. `backend/startup_test.go` builds and runs the real binary on a free port to test start-up validation; the Postgres-unreachable case waits about 45 s and is skipped under `-short`. One startup test flips the shared Redis's `appendonly` setting and restores it.

Run fuzzers with `-parallel 1`: the harness cleanup deletes every instance of its workflow name, so parallel fuzz workers on one Redis delete each other's instances.

## The contract rule

Contract tests (`backend/instance_engine/contract_*_test.go`) are written against the [contract](contract.md), never against current behaviour. A test the code cannot pass yet is wrapped in `testx.XFail(t, "<finding>", body)` from `backend/internal/testx`: it runs on every build, shows as skipped with the finding, and becomes a hard failure the day the fix lands, at which point the wrapper is changed to `testx.Run`. `testx.XFailFlaky` is only for race tests that can pass by luck. **Never edit a contract test to match the code.** If the contract is wrong, change the contract first, in its own commit.

## The test harness

`backend/instance_engine/harness_test.go` is shared by every integration and contract test:

- **Fake clock**, anchored to real time at start and advanced by the test. Anchored, because the live `sagawise` container's reaper shares `task_deadlines` and would otherwise fail the test's tasks.
- **Webhook sink**: an HTTP server recording every delivery, raw body and headers included.
- **Fault hooks**: a go-redis hook that fails the next N commands of a given name, and a Postgres-down switch.
- **Own DSL**: each test loads workflows named `it_*` through the real templating path and deletes them and their instances on cleanup.
- **Workers not started.** After every report and reaper tick the harness drains the archive and webhook queues synchronously (`drain`), so a test observes side effects deterministically.

## CI

`.github/workflows/ci.yml` runs on every pull request: gofmt, `go vet`, staticcheck, golangci-lint, gosec (no exclusions), govulncheck, `go test -race` with the integration tag against service containers, the SDK tests, and the Docker image build. `.github/workflows/docs.yml` builds this site strictly on every pull request that touches it and publishes it from `main`.

Run the same locally before pushing:

```bash
make ci-tools   # once: the pinned linters, the docs toolchain, pytest for the Python SDK
make ci         # everything CI runs, including the strict docs build
```

Tool versions are pinned in the Makefile and the workflow; bump both together.

## Conventions

- Standard library `net/http`, no framework. Every response is JSON; errors use the stable codes of the contract.
- The `Engine` struct owns every dependency. No package-level state, no detached goroutines; loops run under a context and are stopped in order at shutdown.
- Every log line about an instance carries `instance_id`; handlers take the request logger from the context.
- The Lua transition script is the only place state changes. A new transition is a new branch there, plus a contract rule, plus a test.
- A RediSearch index schema change goes through the schema hash in `ensureIndex`; no manual migration steps.
- Helm changes: `helm lint charts/sagawise` and render with the feature on and off; bump `version` in `Chart.yaml`.

## This site

```bash
make docs-tools  # pip install into .venv, or the current Python
make docs        # live reload on http://127.0.0.1:8000/sagawise/
make docs-build  # strict build into site/; a broken link fails it
```

Rules for the pages: one fact lives in one file and everything else links to it; each page opens with who reads it and when; no roadmap phase numbers or audit finding numbers outside `history/`; diagrams are Mermaid in the Markdown; a page past about 200 lines is two pages.

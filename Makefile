.PHONY: start status stop restart clean api_examples order_flow ci ci-tools test test-integration test-sdk test-status bench bench-profile bench-compare

# --- CI (mirrors .github/workflows/ci.yml; bump versions in both places) ---
STATICCHECK_VERSION   = v0.8.1
GOLANGCI_LINT_VERSION = v2.13.2
GOSEC_VERSION         = v2.29.0
GOVULNCHECK_VERSION   = v1.7.0
# G706 (log injection: raw query params in logs) is tracked in docs/TODO.md phase 9
# (structured logging). Remove it from this list when that lands.
GOSEC_EXCLUDE         = G706
GOBIN                ?= $(shell go env GOPATH)/bin

start:
	docker network create shared_network || true
	UUID=$(shell whoami)$(shell hostname) docker compose up -d --build

status:
	docker ps

stop:
	docker compose down

restart:
	make stop && make start

clean:
	docker compose down -v --rmi local || true
	cd examples/api_examples && docker compose down -v --rmi local || true
	cd examples/order_flow && docker compose down -v --rmi local || true
	docker network remove shared_network || true

api_examples:
	make clean && docker network create shared_network && make start && cd examples/api_examples && docker compose up -d --build

order_flow:
	docker network create shared_network || true
	make start && cd examples/order_flow && UUID=$(shell whoami)$(shell hostname) docker compose up -d --build

ci-tools:
	cd backend && go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	cd backend && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	cd backend && go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	cd backend && go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

ci:
	cd backend && test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	cd backend && go vet -tags integration ./...
	cd backend && $(GOBIN)/staticcheck ./...
	cd backend && $(GOBIN)/golangci-lint run ./...
	cd backend && $(GOBIN)/gosec -quiet -exclude=$(GOSEC_EXCLUDE) ./...
	cd backend && $(GOBIN)/govulncheck ./...
	$(MAKE) test-integration
	$(MAKE) test-sdk
	docker build -f backend/Dockerfile -t sagawise:ci .

# Unit tests only (no external services). This is what the Docker build runs.
test:
	cd backend && go test -race -count=1 ./...

# Unit + integration tests. Needs redis-stack on :6379 and postgres on :5432
# (`make start`). Hosts default to localhost inside the tests; override with
# the same REDIS_*/POSTGRES_* variables the binary reads.
test-integration:
	cd backend && go test -race -count=1 -tags integration ./...

# SDK tests. Node: `node --test` (todo = known failing). Python: pytest (xfail = known failing).
test-sdk:
	cd sdk/nodejs && npm install --no-audit --no-fund --silent && npm test
	cd sdk/python && python3 -m pytest -q tests

# Contract status: which contract tests are still known-failing (XFAIL), grouped by audit finding.
# Every line here is a bug the roadmap still owes; the list shrinks as phases 5 and 6 land.
test-status:
	@cd backend && go test -count=1 -short -tags integration -v ./... 2>/dev/null \
		| grep -oE 'XFAIL [^ ]+ \(known failing' | grep -v self-test | sed 's/ (known failing//' | sort | uniq -c | sort -rn \
		| { grep . || echo "none: every contract test passes"; }

# --- Benchmarks (docs/benchmarks/README.md) ---
BENCH_LABEL ?= baseline
BENCH_ARGS  ?=
# Stops the sagawise container for the run: its reaper shares task_deadlines with the
# server under test and would steal the reaper-lag measurement.
bench:
	docker compose stop sagawise || true
	cd backend && go run ./cmd/bench run -label $(BENCH_LABEL) -out ../docs/benchmarks/runs $(BENCH_ARGS); \
	status=$$?; cd .. && (docker compose start sagawise || true); exit $$status

# Bottleneck hunt: saturation ramp, pprof at the knee, Redis command breakdown, scaling
# curves (instances in Redis, tasks per workflow, payload size, simultaneous timeouts),
# contention. Stored as runs/<date>_<sha>_profile-<label>/. ~10 minutes.
bench-profile:
	docker compose stop sagawise || true
	cd backend && go run ./cmd/bench profile -label $(BENCH_LABEL) -out ../docs/benchmarks/runs $(BENCH_ARGS); \
	status=$$?; cd .. && (docker compose start sagawise || true); exit $$status

# make bench-compare A=runs/<before> B=runs/<after>   (paths relative to docs/benchmarks)
# Works for two `bench` runs or two `bench-profile` runs.
bench-compare:
	cd backend && go run ./cmd/bench compare ../docs/benchmarks/$(A) ../docs/benchmarks/$(B) -out ../docs/benchmarks/comparisons

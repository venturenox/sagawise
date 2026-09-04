.PHONY: start status stop restart clean api_examples order_flow ci ci-tools test test-integration test-sdk test-status

# --- CI (mirrors .github/workflows/ci.yml; bump versions in both places) ---
STATICCHECK_VERSION   = v0.8.1
GOLANGCI_LINT_VERSION = v2.13.2
GOSEC_VERSION         = v2.29.0
GOVULNCHECK_VERSION   = v1.7.0
# G104 (unhandled errors) and G706 (log injection): see docs/correctness-audit-2026-08-29.md #7
# and docs/TODO.md phases 5, 6, 9. Remove from this list when those land.
GOSEC_EXCLUDE         = G104,G706
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
		| grep -oE 'XFAIL [^ ]+ \(known failing' | grep -v self-test | sed 's/ (known failing//' | sort | uniq -c | sort -rn

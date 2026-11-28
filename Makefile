.PHONY: help fmt lint dashboard-assets build test check clean run demo-agent demo-team demo-check examples-check live-check repository-provision-check docker compose-up compose-down release release-plan
.DEFAULT_GOAL := help
export bump

# Optional overrides; no secrets or environment files are created by make.
GO_TEST_FLAGS ?= -race -count=1
GO_TEST_PACKAGES ?= ./...
RUN_ARGS ?= --mcp
DEMO_ARGS ?=
LIVE_ARGS ?= -prompt-timeout 10m -seconds 900
IMAGE ?= agentiplex:dev

help:
	@printf '%s\n' \
		'Development:' \
		'  make fmt              Format tracked Go sources' \
		'  make lint             Run go vet' \
		'  make build            Build daemon and CLI into bin/' \
		'  make test             Race-enabled Go tests (GO_TEST_PACKAGES=./... by default)' \
		'  make check            Go tests, vet and release-script tests' \
		'  make clean            Remove generated binaries, dist and AMIS assets' \
		'  make run              Start local daemon with MCP' \
		'  make dashboard-assets Generate embedded AMIS assets' \
		'Examples:' \
		'  make demo-agent       Start a simulated agent' \
		'  make demo-team        Run simulated engineering team' \
		'  make demo-check       Noninteractive simulated demo' \
		'  Real-agent start/send/stop: see examples/README.md' \
		'  make examples-check   Provider-free native example checks (real daemon, simulated agents)' \
		'  make live-check       Verification: real PM+architect autonomous team (MODEL optional; Linux; see examples/README.md)' \
		'  make repository-provision-check Verification: start the repository team gateways, no model task' \
		'Deployment:' \
		'  make docker           Build local image (IMAGE=agentiplex:dev)' \
		'  make compose-up       Start TLS demo; generate missing demo secrets' \
		'  make compose-down     Stop demo; retain data and credentials' \
		'Release:' \
		'  make release-plan bump=MINOR  Preview version and Helm metadata' \
		'  make release bump=MINOR       Commit Helm metadata and atomically push main/tag (PATCH/MINOR/MAJOR)'

fmt:
	gofmt -w $$(git ls-files '*.go')

dashboard-assets:
	go generate ./internal/dashboard

build: dashboard-assets
	mkdir -p bin
	go build -mod=readonly -o bin/agentiplex ./cmd/agentiplex
	go build -mod=readonly -o bin/agentiplexd ./cmd/agentiplexd
	go build -mod=readonly ./examples/...

test: dashboard-assets
	go test $(GO_TEST_FLAGS) $(GO_TEST_PACKAGES)

lint: dashboard-assets
	go vet ./...

check: test lint
	bash scripts/test-release.sh

clean:
	rm -rf bin dist internal/dashboard/static/vendor

run: build
	./bin/agentiplexd $(RUN_ARGS)

demo-agent:
	go run ./examples/simulated-agent --name architect --skill architecture --port 8001

demo-team:
	bash examples/engineering-team/demo.sh $(DEMO_ARGS)

demo-check:
	bash examples/engineering-team/demo.sh --check $(DEMO_ARGS)

examples-check: dashboard-assets
	go test $(GO_TEST_FLAGS) ./tests/examples/

live-check: dashboard-assets
	go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$$' -args -live autonomous $(if $(MODEL),-model $(MODEL)) $(LIVE_ARGS)

repository-provision-check: dashboard-assets
	go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$$' -args -live provision

docker:
	docker build -t $(IMAGE) .

compose-up:
	@test -d .demo-secrets || bash scripts/demo-secrets.sh
	docker compose -f examples/engineering-team/compose.yaml up -d --build --wait

compose-down:
	docker compose -f examples/engineering-team/compose.yaml down

release-plan:
	bash scripts/release.sh --dry-run

release:
	bash scripts/release.sh

.PHONY: help fmt lint dashboard-assets build test check clean run demo-agent demo-team demo-check autonomous-check live-check repository-team repository-check docker compose-up compose-down release release-plan
.DEFAULT_GOAL := help
export bump WORKSPACE OBJECTIVE

# Optional overrides; no secrets or environment files are created by make.
GO_TEST_FLAGS ?= -race -count=1
RUN_ARGS ?= --mcp
DEMO_ARGS ?=
AUTONOMOUS_ARGS ?= --seconds 90
LIVE_ARGS ?= --model opencode/big-pickle --accept-free-model --headless --summary-only --prompt-timeout 10m --seconds 900
REPOSITORY_ARGS ?=
IMAGE ?= agentiplex:dev

help:
	@printf '%s\n' \
		'Development:' \
		'  make fmt              Format tracked Go sources' \
		'  make lint             Run go vet' \
		'  make build            Build daemon and CLI into bin/' \
		'  make test             Race-enabled Go tests' \
		'  make check            Tests, vet, Python and release-script tests' \
		'  make clean            Remove generated binaries, dist and AMIS assets' \
		'  make run              Start local daemon with MCP' \
		'  make dashboard-assets Generate embedded AMIS assets' \
		'Examples:' \
		'  make demo-agent       Start a simulated agent' \
		'  make demo-team        Run simulated engineering team' \
		'  make demo-check       Noninteractive simulated demo' \
		'  make autonomous-check Provider-free launcher check' \
		'  make live-check       Real OpenCode hybrid review (free model; Linux)' \
		'  make repository-team WORKSPACE=... OBJECTIVE=...  Shared repository team (opt-in)' \
		'  make repository-check Provider-free repository-team tests' \
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
	go test $(GO_TEST_FLAGS) ./...

lint: dashboard-assets
	go vet ./...

check: test lint repository-check
	python3 -B examples/autonomous-team/test_team.py
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

autonomous-check:
	python3 -B examples/autonomous-team/test_team.py
	python3 -B examples/autonomous-team/team.py --profile simulated --check --headless $(AUTONOMOUS_ARGS)

live-check:
	python3 -B examples/autonomous-team/team.py $(LIVE_ARGS)

repository-team: build
	python3 -B examples/repository-team/team.py --workspace "${WORKSPACE}" --objective "${OBJECTIVE}" $(REPOSITORY_ARGS)

repository-check:
	python3 -B examples/repository-team/test_team.py

docker:
	docker build -t $(IMAGE) .

compose-up:
	@test -d .demo-secrets || bash scripts/demo-secrets.sh
	docker compose up -d --build --wait

compose-down:
	docker compose down

release-plan:
	bash scripts/release.sh --dry-run

release:
	bash scripts/release.sh

.PHONY: help dashboard-assets build test check demo-agent demo-team demo-check autonomous-check release release-plan
.DEFAULT_GOAL := help
export bump

help:
	@printf '%s\n' 'make build             Build daemon and CLI' 'make test              Race-enabled Go tests' 'make check             Tests, vet and launcher/release tests' 'make demo-team         Simulated team' 'make autonomous-check Provider-free autonomous launcher' 'make release bump=MINOR (or PATCH/MAJOR): push next annotated version tag from clean, current main' 'make release-plan bump=MINOR: preview next tag'

dashboard-assets:
	go generate ./internal/dashboard

build: dashboard-assets
	mkdir -p bin
	go build -mod=readonly -o bin/agentiplex ./cmd/agentiplex
	go build -mod=readonly -o bin/agentiplexd ./cmd/agentiplexd

test: dashboard-assets
	go test -race ./...

check: test
	go vet ./...
	python3 -B examples/autonomous-team/test_team.py
	bash scripts/test-release.sh

demo-agent:
	go run ./examples/simulated-agent --name architect --skill architecture --port 8001

demo-team:
	bash examples/engineering-team/demo.sh

demo-check:
	bash examples/engineering-team/demo.sh --check

autonomous-check:
	python3 -B examples/autonomous-team/test_team.py
	python3 -B examples/autonomous-team/team.py --profile simulated --check --headless

release-plan:
	bash scripts/release.sh --dry-run

release:
	bash scripts/release.sh

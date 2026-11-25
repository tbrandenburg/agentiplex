# agentiplex

Lightweight, open-source middleware for AI agent collaboration. Agent discovery, pooling, durable messaging, broadcasting and persistent state — built in Go, with modular adapters for A2A, AHP, ACP and MCP.

**Status:** v0.1 MVP stabilization; one daemon per namespace, durable A2A/MCP collaboration, shared state and optional real OpenCode team. Distributed coordination and production HA are deferred.

- [Product requirements](docs/PRD.md)
- [Architecture](docs/ARCH.md)
- [Foundation scope, demo instructions and merge gates](docs/FOUNDATION.md)
- [Native A2A ingress contract and examples](docs/A2A.md)
- [Collaboration primitives and demo](docs/COLLABORATION.md)
- [Six-agent demonstrator and OpenCode integration](docs/DEMO.md)
- [Agent lifecycle and persistent application state](docs/STATE.md)
- [Configurable AMIS dashboard](docs/DASHBOARD.md)

Agentiplex reuses the official A2A SDK, AI Catalog SDK and NATS JetStream. A headless daemon (`agentiplexd`) owns infrastructure; a separate CLI (`agentiplex`) administers it remotely.

## Quick demo

With Go 1.26+, Bash, curl, jq and Node/npm for build-time asset packaging:

```sh
bash examples/engineering-team/demo.sh
```

Open http://127.0.0.1:18080/ui/. Six independent simulated A2A agents exercise the real collaboration infrastructure; Ctrl+C cleans up. Use `--check` for a noninteractive smoke test. See [DEMO.md](docs/DEMO.md) for replacing the Architect with OpenCode.

## Development

Go 1.26 or newer. Dependencies are locked in go.mod and go.sum; run `go generate ./internal/dashboard` before `go test -race ./...`. The provider-free demonstration uses an independently running A2A server. See the foundation guide for commands and current limitations.

Local demos are unauthenticated and must stay on loopback or isolated trusted networks. Authenticated TLS deployment is supported; see below.

Use `make build`, `make test`, `make check` and `make demo-team` (existing Taskfile remains available). `agentiplexd --workers 4` bounds parallel outbound operations; use one for serial execution. For a stuck accepted task, inspect `agentiplex messages status ID`; only after independently confirming the remote task stopped, use `agentiplex messages abandon ID DOWNSTREAM_TASK_ID --confirm-remote-stopped`. This retains uncertainty and never cancels or replays remote work.

### Release

From clean, current `main`, preview with `make release-plan bump=MINOR`, then publish with `make release bump=MINOR` (also PATCH or MAJOR). Highest stable vMAJOR.MINOR.PATCH tag is the baseline; no tags means v0.0.0, so the first MINOR release is v0.1.0. Prerelease tags are ignored. The command pushes only an annotated release tag; existing GitHub Actions tests, builds and publishes the release. A failed push retains the local tag for manual inspection. No release is created by ordinary CI.

## MCP collaboration tools

Enable the optional local MCP endpoint with `agentiplexd --mcp` and connect an MCP host to `http://127.0.0.1:8080/mcp`. See [docs/MCP.md](docs/MCP.md) for tool contracts, OpenCode configuration and the provider-free client demo.

## Secure deployment

See [deployment instructions](docs/DEPLOYMENT.md) for authenticated native TLS, the six-agent Compose stack, external NATS and Helm. The legacy local demonstration remains available. No public unauthenticated deployment is supported.

## Autonomous engineering team (optional live model)

See [examples/autonomous-team](examples/autonomous-team/README.md) for a configurable hybrid or all-real OpenCode team. The example provisions independent A2A gateways, submits one objective to the PM and observes model-issued collaboration through MCP. Real-model execution is explicitly opt-in locally; separate Live OpenCode CI uses the free model catalog without provider credentials. Deterministic Checks exercises startup and nested A2A/MCP interoperability.

# agentiplex

Lightweight, open-source middleware for AI agent collaboration. Agent discovery, pooling, durable messaging, broadcasting and persistent state — built in Go, with modular adapters for A2A, AHP, ACP and MCP.

**Status:** early foundation; native A2A direct-send, durable group broadcasting, pool dispatch, event subscriptions, agent lifecycle and revision-checked state are the current scope.

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

This development version is trusted-local only: it has no authentication. Do not expose it publicly.

## MCP collaboration tools

Enable the optional local MCP endpoint with `agentiplexd --mcp` and connect an MCP host to `http://127.0.0.1:8080/mcp`. See [docs/MCP.md](docs/MCP.md) for tool contracts, OpenCode configuration and the provider-free client demo.

## Secure deployment

See [deployment instructions](docs/DEPLOYMENT.md) for authenticated native TLS, the six-agent Compose stack, external NATS and Helm. The legacy local demonstration remains available. No public unauthenticated deployment is supported.

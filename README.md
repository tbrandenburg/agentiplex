# agentiplex

Lightweight, open-source middleware for AI agent collaboration. Agent discovery, pooling, durable messaging, broadcasting and persistent state — built in Go, with modular adapters for A2A, AHP, ACP and MCP.

**Status:** early foundation; native A2A direct-send ingress and outbound delivery are the current scope; additional collaboration features are planned.

- [Product requirements](docs/PRD.md)
- [Architecture](docs/ARCH.md)
- [Foundation scope, demo instructions and merge gates](docs/FOUNDATION.md)
- [Native A2A ingress contract and examples](docs/A2A.md)

Agentiplex reuses the official A2A SDK, AI Catalog SDK and NATS JetStream. A headless daemon (`agentiplexd`) owns infrastructure; a separate CLI (`agentiplex`) administers it remotely.

## Development

Go 1.26 or newer. Dependencies are locked in go.mod and go.sum; run `go test -race ./...`. The provider-free demonstration uses an independently running A2A server. See the foundation guide for commands and current limitations.

This development version is trusted-local only: it has no authentication. Do not expose it publicly.

# agentiplex

Lightweight, open-source middleware for AI agent collaboration. Agent discovery, pooling, durable messaging, broadcasting and persistent state — built in Go, with modular adapters for A2A, AHP, ACP and MCP.

**Status:** early foundation; features beyond the direct outbound slice are planned, not implemented or verified.

- [Product requirements](docs/PRD.md)
- [Architecture](docs/ARCH.md)
- [Foundation scope, demo instructions and merge gates](docs/FOUNDATION.md)

Agentiplex reuses the official A2A SDK, AI Catalog SDK and NATS JetStream. A headless daemon (`agentiplexd`) owns infrastructure; a separate CLI (`agentiplex`) administers it remotely.

## Development

Go 1.26 or newer. Resolve and lock dependencies with `go mod tidy`, then run `go test -race ./...`. The provider-free demonstration uses an independently running A2A server. See the foundation guide for commands and current limitations.

This development version is trusted-local only: it has no authentication. Do not expose it publicly.

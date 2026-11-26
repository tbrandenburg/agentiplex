# AGENTS.md

## Purpose
Agentiplex is lightweight Go middleware for agent collaboration: discovery, durable
delivery (NATS JetStream), shared state, and a dashboard. It is model- and
framework-agnostic and never decides what agents do. Product docs live in `README.md`
and `docs/` (start with `docs/ARCH.md`); do not duplicate them here.

## Layout
- Root `*.go`: core library. `cmd/`: `agentiplex` CLI and `agentiplexd` daemon.
- `internal/`: adapters, api, catalog, cli, daemon, dashboard, nats, security, simagent.
- `examples/`: operator-side demos, outside the core. Do not add example logic to the core.
- `tests/integration/`: integration tests.

## Workflow
- Use the Makefile: `make build`, `make test`, `make lint`, `make check`. Run `make check` before finishing.
- Keep it simple (KISS/DRY). Keep example launchers outside the middleware.
- Never present a specific example or agent runtime (e.g. OpenCode) as a major feature of Agentiplex. Keep them loosely coupled from the core and confined to `examples/`.
- Keep docs in `docs/` current; link them from the README instead of expanding it.

## Examples
Real-agent examples (`examples/repository-team`, `autonomous-team`) start full-access OpenCode
agents. Run them only on explicit user request, and read `examples/README.md` first
(setup, private-repo handling, known flaky startup, operating tips).

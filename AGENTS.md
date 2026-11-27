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
- Design for day-long durability (`docs/ARCH.md` section 5.5): the durable record is the source of truth, connections are disposable views, waits are re-attachable and event-driven, and no accepted work has a server-imposed wall-clock limit. Do not add timeout env vars or caps for waiting; prefer re-attach by deterministic key. A new wait or notification must survive a connection drop and a daemon restart, including an unclean one (SIGKILL, host crash): restarting the middleware must continue the team where it stopped, and acknowledged writes must be fsynced.
- Keep docs in `docs/` current; link them from the README instead of expanding it.

## Inference for testing
The owner allows inference (real model calls) for testing and validation, including the real-agent examples. Do not ask for permission before using it; keep runs bounded and report what was run.

## Examples
Real-agent examples (`examples/repository-team`, `autonomous-team`) start full-access OpenCode
agents. Running them for testing is allowed without asking; read `examples/README.md` first
(setup, private-repo handling, known flaky startup, operating tips).

## Lessons Learned
- 2026-11-27: Pitfall: `gh issue view N --comments` omits the issue body, so a first read missed the specs. Prevention rule/countermeasure: always also fetch `--json body -q .body`, and pass both to subagents.

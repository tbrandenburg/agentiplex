# AGENTS.md

## Purpose
Agentiplex is lightweight Go middleware for agent collaboration: discovery, durable
delivery (NATS JetStream), shared state, and a dashboard. It is model- and
framework-agnostic and never decides what agents do. Product docs live in `README.md`
and `docs/` (start with `docs/ARCH.md`); do not duplicate them here.

## Layout
- Root `*.go`: core library. `cmd/`: `agentiplex` CLI and `agentiplexd` daemon.
- `internal/`: adapters, api, catalog, cli, daemon, dashboard, nats, security, simagent.
- `deploy/`: sample daemon configuration, Helm and systemd assets.
- `examples/`: operator-side demos, outside the core. Do not add example logic to the core.
- `tests/integration/`: integration tests.

## Early-stage simplification
Agentiplex is pre-1.0. Breaking changes to its own APIs, configuration, storage
formats and tools, removal of obsolete features, and refactoring are explicitly
allowed when they reduce complexity.

- Prefer replacing inadequate mechanisms over maintaining parallel implementations.
- Do not add backward-compatibility layers, migration machinery or speculative
  abstractions without a demonstrated requirement.
- Preserve A2A/MCP standards interoperability, security and durable-work safety.
  Document breaking changes; drain or reconcile outstanding work before any
  explicit disposable-data reset. Never silently discard or replay work.
- Make net complexity reduction part of the definition of done: identify removed
  code paths, state, configuration and dependencies; justify unavoidable
  additions with concrete evidence. Do not optimize for line count alone.
- Defer future capabilities until demonstrated need warrants separate
  prioritization. A foreseeable future need is not a reason to implement it now.

## Workflow
- Use the Makefile: `make build`, `make test`, `make lint`, `make check`. Run `make check` before finishing.
- Keep it simple (KISS/DRY). Keep example launchers outside the middleware.
- Never present a specific example or agent runtime (e.g. OpenCode) as a major feature of Agentiplex. Keep them loosely coupled from the core and confined to `examples/`.
- Design for day-long durability (`docs/ARCH.md` section 5.5): the durable record is the source of truth, connections are disposable views, waits are re-attachable and event-driven, and no accepted work has a server-imposed wall-clock limit. Do not add timeout env vars or caps for waiting; prefer re-attach by stable, explicit request ID. A new wait or notification must survive a connection drop and a daemon restart, including an unclean one (SIGKILL, host crash): restarting the middleware must continue the team where it stopped, and acknowledged writes must be fsynced.
- Keep docs in `docs/` current; link them from the README instead of expanding it.

## Inference for testing
The owner allows inference (real model calls) for testing and validation, including the real-agent examples. Do not ask for permission before using it; keep runs bounded and report what was run.

## Examples
Real-agent examples (`examples/repository-team`, `autonomous-team`) start full-access OpenCode
agents. Running them for testing is allowed without asking; read `examples/README.md` first
(setup, private-repo handling, known flaky startup, operating tips).

## Lessons Learned
- 2026-11-27: Pitfall: `gh issue view N --comments` omits the issue body, so a first read missed the specs. Prevention rule/countermeasure: always also fetch `--json body -q .body`, and pass both to subagents.
- 2026-11-27: Pitfall: Fresh git worktrees lack gitignored generated assets (dashboard), so subagent builds/tests failed confusingly. Prevention: run `make dashboard-assets` first in every new worktree.
- 2026-11-27: Pitfall: Embedded JetStream refuses to start when free disk is below its bucket reservations (~560 MB), with an opaque error. Prevention: check `df` before E2E; use `TMPDIR=/dev/shm` when `/` is nearly full.

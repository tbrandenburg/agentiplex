# Examples: running real OpenCode agents

Applies to `repository-team` and `autonomous-team`. These start real, partly
full-access OpenCode agents with their **native commands**; each example has one `team.yaml` that is passed unchanged to
`agentiplexd --config team.yaml`. There is no launcher: follow the example README. The core middleware does not depend on any of this.
Ports are fixed loopback ports (daemon 19080, agents 19081 and up); both examples use 19080, so run one at a time and check `ss -ltn` first.
Trusted-local means unauthenticated loopback HTTP; for secure operation use an operator-owned copy of `team.yaml`.

## Setup
- Go 1.26+, Git, Make, Node/npm, and Bun 1.4.4 (the gateway needs `bun` on PATH). Python 3.10+ is only needed for the
  optional verification scripts in [tests/examples](../tests/examples).
- `opencode` (2.3.2 or newer) and `bun` must be on PATH, plus the gateway `opencode-a2a-lite` (0.9.0 or newer):
  ```sh
  npm install --global @tbrandenburg/opencode-a2a-lite
  ```
  (`npx @tbrandenburg/opencode-a2a-lite@^0.9.0 ...` also works, but stopping the npx wrapper can orphan the real gateway.)
- Gateway policy files (`--policy-file`) must be outside the workspace, owned by you and neither they nor any ancestor
  directory may be group/world-writable. If your checkout or umask makes them group-writable, use private copies as the READMEs do.
  "OpenCode 2.3.2 or newer" is accepted, but 2.4.1 is no more reliable than 2.3.2 (see below).

## Private repositories
- Pass `--pass-env <PROVIDER>_API_KEY` to the gateway and select an approved `model` in `opencode.json`. Only named
  variables are forwarded; `gh` login and OpenCode auth are not.
- Never point free models at private code: free-model providers may retain prompts.
- Clone the repository yourself into the disposable workspace (the gateways have no GitHub credentials).

## Collaboration model
- Agents collaborate through the MCP tools of the daemon. The coordinator (PM) discovers the team and delegates with the tools it is
  granted (typically the blocking `ask`); mechanics such as idempotency keys and re-attaching are in the tool contract, not the prompt. Findings are shared with `note`/`read_notes`.
- Each team is the `registry` block of its `team.yaml` (agents, groups, pools) plus one native runtime config per role and
  static gateway policy files. A new member is one more registry entry and one more native runtime.
- **Restart expectation:** if the daemon is killed (even SIGKILL) mid-run and restarted on the same storage and port, the run
  continues: the PM's failed `ask` is repeated and re-attaches to the same delivery, with no duplicate downstream
  execution. This relies on the restart policy of [DEPLOYMENT](../docs/DEPLOYMENT.md) (#31) or an operator restart.
  A model turn that was in flight in an agent is not resumed.
- Automated verification (not the normal usage path) lives in [tests/examples](../tests/examples): `make examples-check`,
  `make repository-provision-check`, `make live-check` (inference with a free model).

## Known issue: flaky startup preflight
The gateway checks the private policy with `opencode --standalone debug agents`. OpenCode
intermittently omits the policy, and the gateway retries only 3 times. Measured probe pass
rates: 5/12 on 2.3.2 with a reused data dir, 1/12 with a fresh one, 3/12 on 2.4.1. Roughly 30% of
launches pass on 2.3.2.

The error "capability policy was dropped, overridden or weakened by configuration" is therefore
often not a configuration problem. Retry the launch before changing configuration.

## Operating tips
- Gateway startup takes a minute or more (preflight). Run each process in its own terminal or detached; do not poll.
- Agents are asked not to commit or push. Review the workspace diff, then open the PR yourself.
- JetStream needs free disk (about 560 MB reserved): point `broker.storage` in `team.yaml` at a roomier or in-memory filesystem if needed.
- Stop only the processes you started, by PID; delete the run directory afterwards.

Guides: [repository-team](repository-team/README.md), [autonomous-team](autonomous-team/README.md).

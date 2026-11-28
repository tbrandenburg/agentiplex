# Autonomous engineering team

A disposable, trusted-local example: a real **PM** (the coordinator) and a real **Architect** (OpenCode behind
[opencode-a2a-lite](https://github.com/tbrandenburg/opencode-a2a-lite)) plus four simulated workers, reviewing a
fictional synthetic fixture. You start the agents with their native commands, start the daemon with
[team.yaml](team.yaml) unchanged and submit **one objective** to the PM. The PM discovers the team and delegates by
itself with the MCP tools it is granted. No launcher, no generated configuration.

| Agent | Runtime | Port | Native config |
|---|---|---|---|
| `pm` (coordinator) | OpenCode gateway, read-only, delegation tools | 19081 | [profiles/pm](profiles/pm/opencode.json), [policies/coordinator.json](policies/coordinator.json) |
| `architect` | OpenCode gateway, read-only | 19082 | [profiles/architect](profiles/architect/opencode.json), [policies/worker.json](policies/worker.json) |
| `developer`, `tester-1`, `tester-2`, `quality` | `simulated-agent` | 19083-19086 | native flags |
| daemon (dashboard `/ui/`, MCP `/mcp`) | `agentiplexd` | 19080 | [team.yaml](team.yaml) |

`team.yaml` also registers the group `engineering` and the pool `testers` (the PM is in neither). Everything listens on
**loopback without authentication (trusted-local)**. For authenticated TLS operation use an operator-owned copy of
`team.yaml` (see [docs/DEPLOYMENT.md](../../docs/DEPLOYMENT.md)). Never expose these ports.

## Prerequisites
Linux, Go 1.26+, Node 22+, Bun 1.4.4, OpenCode 2.3.2 or newer, `opencode-a2a-lite` 0.9.0 or newer
(`npm install --global @tbrandenburg/opencode-a2a-lite`). See [../README.md](../README.md). Ports 19080-19086 must be free
(check with `ss -ltn`); to use others, change them consistently in `team.yaml`, the commands below and the MCP `url` in
`profiles/*/opencode.json` (`http://127.0.0.1:19080/mcp`).
JetStream needs free disk: edit `broker.storage` in `team.yaml` (default `/tmp/agentiplex-autonomous/jetstream`) if needed.

## Quick start
`up.sh` and `down.sh` only run the commands of "Run it" below in the background (no other logic). The objective is the plain file
[objective.md](objective.md); the agents' role prompts are the `system` text in [profiles/](profiles/).

```sh
make autonomous-team-up                         # prints RUN=<dir>; the gateway preflight can take a minute or more
make autonomous-team-send                       # sends objective.md to the coordinator: bin/agentiplex send pm "$(cat objective.md)"
bin/agentiplex --server http://127.0.0.1:19080 messages status <id>   # until a terminal state; also http://127.0.0.1:19080/ui/
make autonomous-team-down RUN=<dir>             # stops exactly the recorded processes
```
Verified: the PM completed and delegated to the architect and the `testers` pool by itself (opencode 2.4.1, gateway 0.9.0, free model).

## Run it (by hand)
Use the commands from the repository root; run each long-lived process in its own terminal.

```sh
make build                                   # bin/agentiplexd, bin/agentiplex
go build -o bin/ ./examples/simulated-agent
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
EX=$PWD/examples/autonomous-team RUN=$(mktemp -d)

# private run directory (mode 0700): the gateway rejects policy files in group-writable directories, so use copies
cp -r $EX/policies $RUN/policies && chmod 700 $RUN/policies && chmod 600 $RUN/policies/*
# private workspaces: the profile config plus the synthetic fixture only
for r in pm architect; do mkdir $RUN/$r; cp $EX/profiles/$r/opencode.json $RUN/$r/; cp -r $EX/fixture $RUN/$r/; done

# 1. the four simulated workers
bin/simulated-agent --name developer --skill development --port 19083
bin/simulated-agent --name tester-1   --skill testing     --port 19084
bin/simulated-agent --name tester-2   --skill testing     --port 19085
bin/simulated-agent --name quality    --skill quality     --port 19086

# 2. the two real agents (identity via A2A_*; the startup preflight can take a minute or more)
A2A_NAME=pm A2A_DESCRIPTION="Read-only planning coordinator" A2A_TAGS=planning,real-opencode \
  opencode-a2a-lite --workspace $RUN/pm --port 19081 --access read --no-env \
  --policy-file $RUN/policies/coordinator.json --prompt-timeout 10m
A2A_NAME=architect A2A_DESCRIPTION="Read-only architecture reviewer" A2A_TAGS=architecture,real-opencode \
  opencode-a2a-lite --workspace $RUN/architect --port 19082 --access read --no-env \
  --policy-file $RUN/policies/worker.json --prompt-timeout 10m

# 3. the daemon registers all six endpoints, the group and the pool at startup
bin/agentiplexd --config $EX/team.yaml

# 4. discover, send ONE objective to the coordinator, inspect
bin/agentiplex agents list
bin/agentiplex send pm "$(cat $EX/objective.md)" --idempotency-key review
bin/agentiplex messages status <id-printed-by-send>   # repeat until a terminal state
bin/agentiplex state list review                      # notes and plans the agents wrote
```

Watch the live registry, deliveries and events at <http://127.0.0.1:19080/ui/>. The PM's reply and every delegation
are delivery records (`bin/agentiplex messages status <id>`); the agents collaborate themselves, you submit nothing else.

Use only the bundled synthetic fixture with free models: free-model providers may retain prompts. OpenCode picks its
default model; to choose one add `"model": {"providerID": "opencode", "model": "big-pickle"}` to the two
`$RUN/<role>/opencode.json` copies. For a credentialed model replace `--no-env` by `--pass-env <PROVIDER>_API_KEY`
(never put a key value in a file or command line).

**Cleanup is yours:** stop only the processes you started (Ctrl+C in each terminal, or `kill <pid>`), then delete `$RUN`
and the storage directory. The gateway is not an OS sandbox; use a disposable host or user.

**Known flaky startup:** the gateway preflight sometimes fails with "capability policy was dropped, overridden or weakened"
(see [../README.md](../README.md)). Retry the same command before changing configuration.

## Customize
- **Add a team member:** add `name: {url: ...}` under `registry.agents` (and to `groups`/`pools`) in `team.yaml`, start one more
  runtime on that port (another `simulated-agent`, or a gateway with its own `profiles/<role>/opencode.json`; identical
  roles such as `tester-1`/`tester-2` simply reuse the same files), then restart the daemon.
- **Make a simulated role real:** copy a profile, edit its `system` text and start a gateway with `policies/worker.json`.
- Only the coordinator's policy grants delegation (`ask`, `send_message`, `broadcast`, `dispatch`); workers can discover,
  read delivery status, use shared state and the append-only `note`/`read_notes`. Grants are least-privilege hygiene,
  not an OS boundary.

## Secure mode (verified HTTPS + per-agent tokens)
Not complete on stock gateway 0.9.0 (private CA cannot reach OpenCode); prerequisites, blocker, edits and evidence are in
[../README.md](../README.md#secure-mode-verified-https--per-agent-tokens). For this team: copy `team.yaml`, add the `server` TLS
fields and `security` tokens and a `token_file` for all six `registry.agents` entries (including the simulated ones; the
simulated agents are plain loopback HTTP endpoints and need no token themselves), use `https://localhost:19080/mcp` plus the
`oauth`/`headers` entries in the `mcp` block of both `$RUN/pm` and `$RUN/architect` copies of `opencode.json`, start the two gateways with their own
`AGENTIPLEX_TOKEN` and `--pass-env AGENTIPLEX_TOKEN` instead of `--no-env`, start `bin/agentiplexd --config <your copy>` and use the operator CLI flags.

## Provider-free verification (no model)
```sh
make examples-check                          # real daemon + simulated agents on each team.yaml, policy checks
make live-check LIVE_ARGS=--provision-only   # real PM + architect gateways registered, no model task
make live-check                              # real free-model run with independent evidence check (uses inference)
```
The harness in [tests/examples/autonomous-team](../../tests/examples/autonomous-team) starts the processes above and
verifies from the daemon's records that the PM completed, at least one real worker produced a result, no delivery is
unresolved and the PM and a worker persisted state. It is test infrastructure, not the normal usage path. Delivery records
show who was asked, not which MCP tool was used, and a model claiming a result without a completed delivery fails.
Review the PM's answer manually.

## Boundaries
The PM role text asks for at most six delegations and no recursion; this is a prompt-level cost guard, not an enforced
quota. There is no token or cost ceiling: use a provider-side budget for credentialed runs. Stopping a process is not
rollback and does not prove a provider request stopped. State and roles are not authenticated identities in trusted-local mode.

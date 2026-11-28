# Autonomous engineering team

A disposable, trusted-local example: a real **PM** (the coordinator) and a real **Architect** (OpenCode behind
[opencode-a2a-lite](https://github.com/tbrandenburg/opencode-a2a-lite)) plus four simulated workers, reviewing a
fictional synthetic fixture. You start the agents with their native commands, start the daemon with
[team.yaml](team.yaml) unchanged and submit **one objective** to the PM. The PM discovers the team and delegates by
itself with the MCP tools it is granted. No orchestration layer, no generated team roster.

| Agent | Runtime | Port | Native config |
|---|---|---|---|
| `pm` (coordinator) | OpenCode gateway, read-only, delegation tools | 19081 | [profiles/pm](profiles/pm/opencode.json), [policies/coordinator.json](policies/coordinator.json) |
| `architect` | OpenCode gateway, read-only | 19082 | [profiles/architect](profiles/architect/opencode.json), [policies/worker.json](policies/worker.json) |
| `developer`, `tester-1`, `tester-2`, `quality` | `simulated-agent` | 19083-19086 | native flags |
| daemon (dashboard `/ui/`, MCP `/mcp`) | `agentiplexd` | 19080 | [team.yaml](team.yaml) |

`team.yaml` also registers the group `engineering` and the pool `testers` (the PM is in neither). Everything listens on
**loopback without authentication (trusted-local)**. For authenticated TLS operation use an operator-owned copy of
`team.yaml` (see [docs/DEPLOYMENT.md](../../docs/DEPLOYMENT.md)). Never expose these ports.

## Quick start

Install the [common tools](../README.md#setup) once (OpenCode 2.4.1, gateway 0.9.3, Bun 1.4.4).
Use a disposable host, free ports 19080–19086 and sufficient disk for JetStream.
From the Agentiplex checkout:

```sh
bash examples/autonomous-team/up.sh
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
bin/agentiplex send pm "$(cat examples/autonomous-team/objective.md)" --idempotency-key review --wait
bin/agentiplex state get review pm
bin/agentiplex state get review architect
bin/agentiplex notes list review
```

The PM returns the assessment. The plan and architectural findings are stored as
ordinary state; additional append-only notes are optional. Inspect the
[dashboard](http://127.0.0.1:19080/ui/) for all deliveries. Ctrl-C during the wait
detaches; `bin/agentiplex messages wait DELIVERY_ID` reattaches.
The launcher prints a private `RUN` directory. Stop its processes with:

```sh
bash examples/down.sh /path/to/printed-run-directory
```

## Native commands (without the convenience launcher)

### Prepare once

From the Agentiplex checkout, put its `bin/` directory on PATH in all terminals.
Copy the prepared profiles, policies and synthetic fixture into a private directory:

```sh
make build
go build -o bin/ ./examples/simulated-agent
mkdir -m 700 ~/agentiplex-autonomous
cp -R examples/autonomous-team/. ~/agentiplex-autonomous/
cd ~/agentiplex-autonomous
cp -R fixture profiles/pm/
cp -R fixture profiles/architect/
```

The profiles select native primary agents and the MCP server. To select the CI
free model, add `"model": {"providerID": "opencode", "model": "big-pickle"}`
to both copied `opencode.json` files. Use only the bundled synthetic fixture with
free models. For an approved credentialed model add `--pass-env PROVIDER_API_KEY`
(the variable name, never its value). The gateway is not an OS sandbox;
all policies and ancestors must be trusted, canonical and not group/world-writable.

### Start the agents and daemon

Run each long-lived process in a separate terminal in `~/agentiplex-autonomous`.
Start the four simulated workers and two real gateways:

```sh
simulated-agent --name developer --skill development --port 19083
simulated-agent --name tester-1 --skill testing --port 19084
simulated-agent --name tester-2 --skill testing --port 19085
simulated-agent --name quality --skill quality --port 19086
opencode-a2a-lite --name pm --description "Read-only planning coordinator" --port 19081 --workspace profiles/pm --policy-file policies/coordinator.json --no-env --prompt-timeout 10m
opencode-a2a-lite --name architect --description "Read-only architecture reviewer" --port 19082 --workspace profiles/architect --policy-file policies/worker.json --no-env --prompt-timeout 10m
```

After all six endpoints are ready, start the daemon. The existing `team.yaml`
registers every endpoint, the engineering group and the testers pool at startup:

```sh
agentiplexd --config team.yaml --storage data
```

### Submit and inspect

After the daemon is ready, in another terminal in the same directory:

```sh
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
agentiplex send pm "$(cat objective.md)" --idempotency-key review --wait
agentiplex state get review pm
agentiplex state get review architect
agentiplex notes list review
```

Use fresh storage for a new objective. Stop only the processes you started
(Ctrl-C in each terminal), then delete the disposable directory after review.
If a preflight exits, inspect its error; see the [startup notes](../README.md#known-issue-flaky-startup-preflight).

## Customize
- **Add a team member:** add `name: {url: ...}` under `registry.agents` (and to `groups`/`pools`) in `team.yaml`, start one more
  runtime on that port (another `simulated-agent`, or a gateway with its own `profiles/<role>/opencode.json`; identical
  roles such as `tester-1`/`tester-2` simply reuse the same files), then restart the daemon.
- **Make a simulated role real:** copy a profile, edit its `system` text and start a gateway with `policies/worker.json`.
- Only the coordinator's policy grants delegation (`ask`, `send_message`, `broadcast`, `dispatch`); workers can discover,
  read delivery status, use shared state and the append-only `note`/`read_notes`. Grants are least-privilege hygiene,
  not an OS boundary.

## Secure mode (verified HTTPS + per-agent tokens)
Private-CA forwarding is now available upstream (0.9.1+), but secure real-agent acceptance on the released gateway remains open (#41); prerequisites and evidence are in
[../README.md](../README.md#secure-mode-verified-https--per-agent-tokens). For this team: copy `team.yaml`, add the `server` TLS
fields and `security` tokens and a `token_file` for all six `registry.agents` entries (including the simulated ones; the
simulated agents are plain loopback HTTP endpoints and need no token themselves), use `https://localhost:19080/mcp` plus the
`oauth`/`headers` entries in the `mcp` block of both copied profile `opencode.json` files, start the two gateways with their own
`AGENTIPLEX_TOKEN` and `--pass-env AGENTIPLEX_TOKEN` instead of `--no-env`, start `bin/agentiplexd --config <your copy>` and use the operator CLI flags.

## Verification
```sh
make examples-check                          # real daemon + simulated agents on each team.yaml, policy checks
make live-check LIVE_ARGS=-provision-only   # real PM + architect gateways registered, no model task
make live-check                              # real free-model run with independent evidence check (uses inference)
```
The [Live example CI workflow](https://github.com/tbrandenburg/agentiplex/actions/workflows/live-opencode.yaml) and harness in [tests/examples](../../tests/examples) use the same native runtime options and profiles (not the manual copy/launcher commands verbatim), and
verifies from the daemon's records that the PM completed, at least one real worker produced a result, no delivery is
unresolved and the PM and a worker persisted state. It is test infrastructure, not the normal usage path. Delivery records
show who was asked, not which MCP tool was used, and a model claiming a result without a completed delivery fails.
Review the PM's answer manually.

## Boundaries
The PM role text asks for at most six delegations and no recursion; this is a prompt-level cost guard, not an enforced
quota. There is no token or cost ceiling: use a provider-side budget for credentialed runs. Stopping a process is not
rollback and does not prove a provider request stopped. State and roles are not authenticated identities in trusted-local mode.

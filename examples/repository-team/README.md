# Repository engineering team

Real OpenCode agents (via [opencode-a2a-lite](https://github.com/tbrandenburg/opencode-a2a-lite)) collaborate on **one
explicitly selected Git workspace** and can produce real repository changes. You start five gateways with their native
commands, start the daemon with [team.yaml](team.yaml) unchanged and send **one objective** to the coordinator (`pm`).
The agents collaborate themselves with the Agentiplex MCP tools. Agentiplex only sees agent IDs, endpoints, messages and
shared state; no launcher, no generated configuration.

> **WARNING - FULL ACCESS:** `developer` and `tester` run with `--access danger-full-access`: arbitrary shell commands without
> per-command approval, edits to any file of your OS user, network access, forwarded provider credentials, Git operations,
> and they bypass MCP tool grants. There is no OS sandbox or rollback. Use a disposable VM/container/user with minimal
> credentials and a trusted checkout, never your everyday environment. All ports are loopback **without authentication
> (trusted-local)**: any local client reaching an agent port can invoke it. Never expose them. For authenticated TLS
> operation use an operator-owned copy of `team.yaml` (see [docs/DEPLOYMENT.md](../../docs/DEPLOYMENT.md)).

| Agent | Access | Policy | Port |
|---|---|---|---|
| `pm` (coordinator) | read | [coordinator.json](policies/coordinator.json): collaboration + delegation tools (`ask`, `send_message`, `broadcast`, `dispatch`) | 19081 |
| `architect`, `quality` | read | [reader.json](policies/reader.json): collaboration tools only | 19082, 19085 |
| `developer`, `tester` | danger-full-access | [writer.json](policies/writer.json): collaboration tools only | 19083, 19084 |
| daemon (dashboard `/ui/`, MCP `/mcp`) | - | [team.yaml](team.yaml) | 19080 |

Only the coordinator can delegate. Grants are least-privilege hygiene, not a boundary (see the warning).

## Prerequisites
Linux, Go 1.26+, Git, Node 22+, Bun 1.4.4, OpenCode 2.3.2 or newer, `opencode-a2a-lite` 0.9.0 or newer
(`npm install --global @tbrandenburg/opencode-a2a-lite`); see [../README.md](../README.md). Ports 19080-19085 must be free
(`ss -ltn`); to change them edit `team.yaml`, the commands below and the MCP `url` in [opencode.json](opencode.json).
JetStream needs free disk: edit `broker.storage` in `team.yaml` if needed.

## 1. Prepare a disposable workspace
The gateways need a trusted, standalone Git workspace with reviewed configuration (no untrusted project instructions,
escaping symlinks, secrets or plugins). A disposable clone is the simplest:

```sh
RUN=$(mktemp -d)                       # mode 0700; also holds the policy copies
git clone https://github.com/owner/repo.git $RUN/workspace   # or your own disposable checkout
EX=$PWD/examples/repository-team
cp -n $EX/opencode.json $RUN/workspace/opencode.json         # -n: never overwrite an operator-owned config
echo /opencode.json >> $RUN/workspace/.git/info/exclude      # keep Git clean; skip if you commit it
cp -r $EX/policies $RUN/policies && chmod 600 $RUN/policies/*   # the gateway rejects group-writable policy paths
```

[opencode.json](opencode.json) is the one shared native OpenCode config (Agentiplex MCP server and a 45 minute tool-call
timeout); it carries no credentials. It omits `model`, so OpenCode's default is used; to choose one add
`"model": {"providerID": "opencode", "model": "big-pickle"}`. If the workspace already has an `opencode.json`, merge the
`mcp` block by hand instead.

## 2. Start the agents, the daemon, send the objective
Run each long-lived process in its own terminal, from the repository root. Role and identity come from `A2A_*`.

```sh
export AGENTIPLEX_SERVER=http://127.0.0.1:19080 W=$RUN/workspace P=$RUN/policies
gw() { opencode-a2a-lite --workspace $W --port "$1" --access "$2" --policy-file $P/"$3".json --prompt-timeout 45m "${@:4}"; }

A2A_NAME=pm        A2A_DESCRIPTION="Coordinate the work: delegate concrete tasks with ask and report honestly" A2A_TAGS=pm,real-opencode gw 19081 read coordinator --no-env
A2A_NAME=architect A2A_DESCRIPTION="Analyze the repository and recommend a bounded implementation" A2A_TAGS=architect,real-opencode gw 19082 read reader --no-env
A2A_NAME=developer A2A_DESCRIPTION="Implement the agreed change, run builds and tests, fix failures" A2A_TAGS=developer,real-opencode gw 19083 danger-full-access writer
A2A_NAME=tester    A2A_DESCRIPTION="Independently run build, test and lint commands and report each command, exit code and outcome (or NOT RUN); do not edit source" A2A_TAGS=tester,real-opencode gw 19084 danger-full-access writer
A2A_NAME=quality   A2A_DESCRIPTION="Review the actual changes independently and identify risks or blockers" A2A_TAGS=quality,real-opencode gw 19085 read reader --no-env

make build && bin/agentiplexd --config $EX/team.yaml     # registers all five endpoints at startup

bin/agentiplex agents list
bin/agentiplex send pm "You are pm, coordinating a team in one shared Git workspace. Discover the available agents and delegate according to their capabilities. Have the change implemented, independently tested and reviewed before you finish, and share findings with the team (notes, namespace repository). Nobody commits, stages, pushes, merges, changes branches or touches credentials or files outside the project. Never invent results; report failures and blockers honestly.
Objective: Improve the README and correct broken relative links" --idempotency-key repository-objective
bin/agentiplex messages status <id-printed-by-send>   # repeat until a terminal state; the dashboard shows all deliveries
bin/agentiplex state list repository                  # shared notes
```

The startup preflight can take a minute or more and fails intermittently ("capability policy was dropped...", see
[../README.md](../README.md)); retry the same gateway command. Gateways start before the daemon because the daemon
registers them at startup; the gateway's MCP connection to the daemon is lazy.

Developer/tester gateways forward no provider keys unless you add `--pass-env <PROVIDER>_API_KEY` (an explicit variable name,
never a value). Use free models only on public or synthetic code: free-model providers may retain prompts. For private code
select an approved provider/model in `opencode.json`, pass only its key from your shell and use a provider-side budget.
Agents are asked not to commit or push, but nothing prevents it.

Timeouts: `--prompt-timeout` bounds one whole agent turn (the PM's turn, all `ask` calls summed, must fit); `timeout.execution`
(ms) in `opencode.json` bounds one tool call, e.g. one `ask`. The daemon's `delivery.timeout` only bounds one HTTP operation to
an agent, not agent execution (see [A2A](../../docs/A2A.md)). A typical run takes 5-20 minutes.

## 3. Inspect, verify, clean up
```sh
git -C $W status --short && git -C $W diff HEAD     # the actual change; review untracked files separately
python3 -B tests/examples/repository-team/verify.py --workspace $W --accept-run-code -- <your project's test command>
```
`verify.py` is an optional independent check: it runs **your** test command (an explicit argv, not a model-generated one)
in the workspace and fails on a nonzero exit, timeout or a change of the Git-visible state. It executes repository code
as your user; run it only after reviewing the diff. Agent-reported test results are not attested; review them.

Delivery records show who was asked and whether it completed, not which MCP tool was used or whether a command passed.

**Cleanup is yours:** stop only the processes you started (Ctrl+C in each terminal), review the diff, then delete `$RUN` and the
storage directory.

## Customize and restart
- **Add a team member** (a second tester, a docs reviewer): add `name: {url: ...}` to `registry.agents` in `team.yaml`, start
  another gateway on that port with its `A2A_*` identity and the `reader`/`writer` policy, restart the daemon. Only
  the coordinator needs the delegation policy.
- **Daemon restart:** if the daemon is killed (even SIGKILL) and restarted on the same storage and port, the PM's failed `ask`
  is repeated and re-attaches to the same delivery without duplicate downstream execution (see [MCP](../../docs/MCP.md),
  [ARCH 5.5](../../docs/ARCH.md), [DEPLOYMENT](../../docs/DEPLOYMENT.md)). A model turn in flight in an agent is not resumed.

## Verification
`make examples-check` (provider-free: real daemon with `team.yaml`, simulated agents, least-privilege policy checks) and
`make repository-provision-check` (real gateways with these static policies on a synthetic Git workspace, no model task)
live in [tests/examples](../../tests/examples).

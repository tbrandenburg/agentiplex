# Repository engineering team

Real OpenCode agents (via [opencode-a2a-lite](https://github.com/tbrandenburg/opencode-a2a-lite)) collaborate on **one
explicitly selected Git workspace** and can produce real repository changes. Start the daemon with [team.yaml](team.yaml), launch the five gateways with their native commands, register their endpoints using the native CLI, and send **one objective** to the coordinator (`pm`).
The agents collaborate themselves with the Agentiplex MCP tools. Agentiplex only sees agent IDs, endpoints, messages and
shared state; no orchestration layer, no generated team roster.

> **WARNING - FULL ACCESS:** `developer` and `tester` run with `--access danger-full-access`: arbitrary shell commands without
> per-command approval, edits to any file of your OS user, network access, any provider credentials you forward with `--pass-env`, Git operations,
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

Only the coordinator is granted delegation tools by the MCP policies. Workers independently inspect the objective, shared notes and repository and return findings; the PM can request corrections. The grants are least-privilege hygiene, not an OS boundary (see the warning).

## Prerequisites
Linux, Go 1.26+, Git, Node 22+, Bun 1.4.4, OpenCode 2.4.1 and `opencode-a2a-lite` 0.9.2 (the upstream verified native-role combination)
(`npm install --global @opencode/cli@2.4.1 @tbrandenburg/opencode-a2a-lite@0.9.2`); see [../README.md](../README.md). Ports 19080-19085 must be free
(`ss -ltn`); to change them edit `team.yaml`, the commands below and the MCP `url` in [opencode.json](opencode.json).
JetStream needs free disk: edit `broker.storage` in `team.yaml` if needed.

## Quick start
Run these commands from the repository root. Start the team only once and set `RUN` to the directory printed by the launcher before stopping it.
`up.sh` and `down.sh` only run the commands of sections 1 and 2 below in the background (no other logic); the prompts are the plain files in
[roles/](roles/) and [objective.md](objective.md). Edit `objective.md` to describe the desired outcome, scope and acceptance criteria. Use a fresh run for each distinct objective.

```sh
ACCEPT_FULL_ACCESS=yes bash examples/repository-team/up.sh /path/to/disposable/git/checkout  # prints RUN=<dir>
# Set RUN to the printed directory before using the stop command.
OBJECTIVE="$(cat examples/repository-team/objective.md)"
bin/agentiplex --server http://127.0.0.1:19080 state put repository objective "$OBJECTIVE" --revision 0 &&
  bin/agentiplex --server http://127.0.0.1:19080 send pm "$OBJECTIVE" --idempotency-key repository-objective
bin/agentiplex --server http://127.0.0.1:19080 messages status <id>   # until a terminal state; also http://127.0.0.1:19080/ui/
bash examples/down.sh "$RUN"                   # stops exactly the recorded processes
```
The earlier gateway 0.9.0 demonstration reported real delegation and a README link repair, but its role text was only Agent Card metadata. Gateway 0.9.2 [native-role evidence](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.9.2/docs/native-roles.md) independently verifies actual system prompts and unchanged user messages. The prompted team's live acceptance is separate (see Verification); startup alone is not evidence of collaboration.

## 1. Prepare a disposable workspace (by hand)
The gateways need a trusted, standalone Git workspace with reviewed configuration (no untrusted project instructions,
escaping symlinks, secrets or plugins). A disposable clone is the simplest:

```sh
RUN=$(mktemp -d)                       # mode 0700; also holds the policy copies
git clone https://github.com/owner/repo.git $RUN/workspace   # or your own disposable checkout
EX=$PWD/examples/repository-team
cp -n $EX/opencode.json $RUN/workspace/opencode.json         # -n: never overwrite an operator-owned config
echo /opencode.json >> $RUN/workspace/.git/info/exclude      # keep Git clean; skip if you commit it
cp -r $EX/policies $RUN/policies && chmod 700 $RUN/policies && chmod 600 $RUN/policies/*   # the gateway rejects group-writable policy paths
```

[opencode.json](opencode.json) is the one shared native OpenCode config (Agentiplex MCP server and a 45 minute tool-call
timeout); it carries no credentials. It omits `model`, so OpenCode's default is used; to choose one add
`"model": {"providerID": "opencode", "model": "big-pickle"}`. If the workspace already has an `opencode.json`, merge the
`mcp` block by hand instead.

## 2. Start the daemon, the agents, register and send the objective (by hand)
Each [role file](roles/) is an actual native OpenCode system prompt passed using `--system-prompt-file`; the short `A2A_DESCRIPTION` is only discovery metadata. The [objective](objective.md) contains only the desired outcome and acceptance criteria (4096-byte A2A submission limit). The operator publishes the exact objective into shared state `repository/objective` with the native create-only state command, then sends the same text to the PM. All agents read the shared objective before contributing and treat it as read-only. This avoids model transcription errors. A failed create blocks submission; use fresh storage for a new objective. Shared state is not a per-key security boundary. Notes use role topics in `repository`. The team chooses which roles are needed and may revise its plan; there is no scripted task sequence. Run one fresh daemon/storage and five gateways per objective. Run each long-lived process in its own terminal, from the repository root.

```sh
export AGENTIPLEX_SERVER=http://127.0.0.1:19080 W=$RUN/workspace P=$RUN/policies
make build && bin/agentiplexd --config "$EX/team.yaml" --storage "$RUN/jetstream" # fresh storage
# Wait for /readyz in another terminal before starting gateways.
gw() { opencode-a2a-lite --workspace "$W" --system-prompt-file "$EX/roles/$1.md" --port "$2" --access "$3" --policy-file "$P/$4.json" --prompt-timeout 45m "${@:5}"; }

A2A_NAME=pm A2A_DESCRIPTION="Engineering coordination and objective ownership" A2A_TAGS=pm,real-opencode gw pm 19081 read coordinator --no-env
A2A_NAME=architect A2A_DESCRIPTION="Architecture analysis and technical recommendations" A2A_TAGS=architect,real-opencode gw architect 19082 read reader --no-env
A2A_NAME=developer A2A_DESCRIPTION="Repository implementation and repair" A2A_TAGS=developer,real-opencode gw developer 19083 danger-full-access writer
A2A_NAME=tester A2A_DESCRIPTION="Independent executable verification" A2A_TAGS=tester,real-opencode gw tester 19084 danger-full-access writer
A2A_NAME=quality A2A_DESCRIPTION="Independent change and acceptance review" A2A_TAGS=quality,real-opencode gw quality 19085 read reader --no-env

# Once the five gateways have published their Agent Cards, register them:
bin/agentiplex register pm http://127.0.0.1:19081
bin/agentiplex register architect http://127.0.0.1:19082
bin/agentiplex register developer http://127.0.0.1:19083
bin/agentiplex register tester http://127.0.0.1:19084
bin/agentiplex register quality http://127.0.0.1:19085
bin/agentiplex agents list
OBJECTIVE="$(cat "$EX/objective.md")"
bin/agentiplex state put repository objective "$OBJECTIVE" --revision 0 &&
  bin/agentiplex send pm "$OBJECTIVE" --idempotency-key repository-objective
bin/agentiplex messages status <id-printed-by-send>   # repeat until a terminal state; the dashboard shows all deliveries
bin/agentiplex state get repository objective         # exact original objective
bin/agentiplex state list repository                  # shared notes
```

The startup preflight can take a minute or more and fails intermittently ("capability policy was dropped...", see
[../README.md](../README.md)); retry the same gateway command. Start the daemon **before** the gateways. With explicit native roles, gateway v0.9.2 creates a session during readiness verification; OpenCode can discover MCP tools at that point. A failed initial MCP connection may persist for the lifetime of that runtime. The native CLI registers each endpoint once it is ready, so no declarative roster or bootstrap retry is needed for this trusted-local example. The static registry configuration remains supported by the daemon for other examples.

Developer/tester gateways forward no provider keys unless you add `--pass-env <PROVIDER>_API_KEY` (an explicit variable name,
never a value). Use free models only on public or synthetic code: free-model providers may retain prompts. For private code
select an approved provider/model in `opencode.json`, pass only its key from your shell and use a provider-side budget.
By default agents leave a patch for the operator. An objective may explicitly authorize local branch/commit/merge operations: see [integration objective](objective.integration.md) for the synthetic CI fixture. Remote publication and history rewriting remain forbidden. The developer prepares and integrates an authorized candidate after the PM confirms independent verification and review; the tester diagnoses and verifies failures but never writes or merges. The developer repairs in-scope CI failures without weakening checks. Full-access gateways can bypass these agreements; enforce external publication restrictions with isolation and credentials, not prompts. Keep role files and runtime configuration trusted and stable during a run. For an already prepared workspace, replace the `--system-prompt-file` argument with `--agent <name>` to select a native primary agent (never both); the same daemon and policies work unchanged.

Timeouts: `--prompt-timeout` bounds one whole agent turn (the PM's turn, all `ask` calls summed, must fit); `timeout.execution`
(ms) in `opencode.json` bounds one tool call, e.g. one `ask`. The daemon's `delivery.timeout` only bounds one HTTP operation to
an agent, not agent execution (see [A2A](../../docs/A2A.md)). Live acceptance has a bounded 20-minute objective deadline; timing and success depend on model and host.

## 3. Inspect, verify, clean up
For a patch-only objective, inspect the working-tree diff as below. For an authorized integration objective, record the starting commit (`BASE=$(git -C "$W" rev-parse HEAD)`) before submission and inspect `git -C "$W" log --graph --oneline "$BASE"..HEAD` and `git -C "$W" diff "$BASE" HEAD` afterwards; a clean merged tree has no uncommitted diff.
```sh
git -C $W status --short && git -C $W diff HEAD     # the actual change; review untracked files separately
(cd "$W" && go test ./...) # example: independently run the project's own trusted checks
```
Run your own approved test command independently, not a command supplied by an agent. Inspect Git status and diff before and after the check; do not accept changes from the verification command without review. Executing repository code runs as your OS user. The automated synthetic-fixture acceptance tests perform the same checks in Go, including a bounded command timeout and Git-state comparison. Agent-reported results are not attested.

Delivery records show which recipient was asked and whether it completed, not which MCP tool was used or whether a command passed. In trusted-local mode note topics are not authenticated authors; the system prompt is not a security boundary.

**Cleanup is yours:** stop only the processes you started (Ctrl+C in each terminal), review the diff, then delete `$RUN` and the
storage directory.

## Secure mode (verified HTTPS + per-agent tokens)
Private-CA forwarding was added upstream in gateway 0.9.1; the authenticated real-team run with the released gateway is not yet verified here (#41). Prerequisites and evidence are in
[../README.md](../README.md#secure-mode-verified-https--per-agent-tokens). For this team, copy `team.yaml` and add the TLS fields and separate operator/viewer credentials. Put each agent's token-file path in the existing `security.agent_token_files` mapping (IDs pm, architect, developer, tester, quality), **not** in a static `registry.agents` roster. Credential identity is independent of endpoint registration. Point the MCP URL and header at the verified HTTPS daemon. Start the daemon first, then each gateway with its own `AGENTIPLEX_TOKEN`, `--pass-env AGENTIPLEX_TOKEN` and the operator-set `NODE_EXTRA_CA_CERTS`. Register the live endpoints with the operator CLI using `--server https://localhost:19080 --ca-file /secure/ca.crt --token-file /secure/operator`. Keep the operator token out of every agent process. This uses existing secure registration capabilities without introducing a startup cycle; the full authenticated real-team run is still unverified (#41).

## Customize and restart
- **Add a team member** (a second tester, a docs reviewer): start another gateway on an unused port with its own system prompt file, `A2A_*` identity and the `reader`/`writer` policy, then run `bin/agentiplex register NAME URL`. Only
  the coordinator needs the delegation policy.
- **Daemon restart:** if the daemon is killed (even SIGKILL) and restarted on the same storage and port, the PM's failed `ask`
  is repeated and re-attaches to the same delivery without duplicate downstream execution (see [MCP](../../docs/MCP.md),
  [ARCH 5.5](../../docs/ARCH.md), [DEPLOYMENT](../../docs/DEPLOYMENT.md)). A model turn in flight in an agent is not resumed.

## Verification
`make examples-check` (provider-free: real daemon with `team.yaml`, native endpoint registration of simulated agents, least-privilege policy checks) and
`make repository-provision-check` (five explicitly prompted real gateways on a synthetic Git workspace, no model task)
live in [tests/examples](../../tests/examples).

Full real-model acceptance is separate, in the path-filtered `Repository team live` workflow (also manually runnable). The same five roles and synthetic repository are used for three objectives, each with a fresh run: docs patch, defect patch, and an authorized local defect integration. No separate merge/CI agent is required; the PM coordinates review and integration, the developer fixes failures and integrates, and the tester and quality reviewer remain independent. The tests publish the exact objective using the operator CLI and submit only that objective to the PM, require a completed implementer and independent reviewer, verify the original shared objective text and revision remain unchanged, require relevant notes, and independently check the expected change, Git scope/history and (for defect/integration) fixed test cases. The integration case must start with a reproducibly failing check, finish on clean main without a remote, and contain a genuine two-parent merge of a retained feature branch descended from the original base; the operator reruns the original check and independent assertions on the merged tree. Both tester and quality results/notes are required for integration. The checker does not attest the order or content of model-reported pre-merge reviews or post-merge commands. They do not prove the semantic quality of every review or authenticate note authors. Raw evidence stays in the private run directory, not CI logs. Run locally only on a disposable host, with no secrets and an approved model:

```sh
make dashboard-assets
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$' -args -live docs -model opencode/big-pickle
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$' -args -live defect -model opencode/big-pickle
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$' -args -live integration -model opencode/big-pickle
```

A failed or timed-out run is not successful collaboration. Inspect the retained private evidence, actual Git diff and observed checks. These live scenarios are skipped by ordinary `make check`; the provider-free Go verifier cases still run there.

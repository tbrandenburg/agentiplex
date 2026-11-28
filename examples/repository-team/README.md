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
| `pm` (coordinator) | read | [coordinator.json](policies/coordinator.json): collaboration + blocking delegation (`ask`) | 19081 |
| `architect`, `quality` | read | [reader.json](policies/reader.json): collaboration tools only | 19082, 19085 |
| `developer`, `tester` | danger-full-access | [writer.json](policies/writer.json): collaboration tools only | 19083, 19084 |
| daemon (dashboard `/ui/`, MCP `/mcp`) | - | [team.yaml](team.yaml) | 19080 |

Only the coordinator has explicit delegation grants. In ordinary mode the full-access workers' wildcard tool permissions can expose other tools from the enabled MCP server; their no-delegation rule is a working agreement, not enforced isolation. Workers independently inspect the objective, shared notes and repository and return findings; the PM can request corrections. The grants are least-privilege hygiene, not an OS boundary (see the warning).

## Quick start

Install the [common tools](../README.md#setup) once (OpenCode 2.4.1, gateway 0.9.3, Bun 1.4.4).
Use a disposable host and checkout, with free ports 19080–19085 and sufficient disk for JetStream.
Review the workspace's configuration and select an approved model first. Free models are for public or synthetic code only.

From the Agentiplex checkout, edit [objective.md](objective.md) to describe the desired outcome, scope and acceptance criteria.
Start the team, then submit the original objective and read the results:

```sh
ACCEPT_FULL_ACCESS=yes bash examples/repository-team/up.sh /path/to/disposable/git/checkout
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
OBJECTIVE="$(cat examples/repository-team/objective.md)"
bin/agentiplex state put repository objective "$OBJECTIVE" --revision 0 &&
  bin/agentiplex send pm "$OBJECTIVE" --idempotency-key repository-objective --wait
bin/agentiplex notes list repository
```

The launcher prints a private `RUN` directory; save that path for cleanup:

```sh
bash examples/down.sh /path/to/printed-run-directory
```

Follow all deliveries on the [dashboard](http://127.0.0.1:19080/ui/). Ctrl-C during
the wait detaches; `bin/agentiplex messages wait DELIVERY_ID` reattaches on the
same daemon. Review the actual Git diff and run independent checks before accepting changes.

## Contract-driven mode (opt in)

Use a fresh disposable repository and fresh daemon storage. The existing five
agents and gateway are reused. Set `CONTRACT_MODE=yes` to append the PM's shared
[contract instructions](roles/contract-kernel.md) and launch developer and tester
with [no MCP exceptions](policies/contract-worker.json). Full-access workers
otherwise have wildcard tool permission, so omitting a grant alone is not a
reliable restriction once MCP is enabled. The PM passes the approved objective
with each request and persists the workers' returned evidence; the read-only
quality reviewer can inspect files and the completed deliveries via MCP.
This removes the normal MCP scheduling path, not OS-level network access.
Review [the approved objective](objective.contract.md) and
[the checklist](contract.json) before starting; this example is specifically
for the synthetic Clamp defect used in CI (see the fixture in
[team_verify_test.go](../../tests/examples/team_verify_test.go)).
Start the team using the convenience launcher (or the native setup below).
Put the checkout's `bin/` directory on PATH and enter the example directory:

```sh
ACCEPT_FULL_ACCESS=yes CONTRACT_MODE=yes bash examples/repository-team/up.sh /path/to/disposable/git/checkout
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
cd examples/repository-team
```

Review and approve the objective and checklist, then run these native commands.
Do not also submit the ordinary repository objective.

<!-- contract-quickstart:start -->
```sh
agentiplex state put repository objective --file objective.contract.md --revision 0
agentiplex state put repository contract --file contract.json --revision 0
agentiplex send pm --file objective.contract.md --idempotency-key repository-contract --until-done --max-turns 8 --timeout 20m --wait
```
<!-- contract-quickstart:end -->

In another terminal, inspect the living checklist with
`agentiplex state watch repository`, and the durable request with
`agentiplex messages list` or `agentiplex messages status MESSAGE_ID`.

Round one reproduces the failing check and checkpoints it before repair.
Subsequent activations work on open criteria and reverify after changes.
The daemon continues automatically after each safe completed turn without an
inactivity timer. The PM explicitly resolves the logical request only after
all criteria have evidence; the daemon waits for execution quiescence. It
pauses on intervention or the eight-turn/deadline limits. The operator must
still independently verify the actual patch. Checklist ownership is a
trusted-local working agreement, not enforced authorization.

Ctrl-C detaches the observer, not execution. Reattach with
`agentiplex messages wait MESSAGE_ID` or repeat the identical send with
the same idempotency key and options. Restart the daemon with the same storage
to recover the request. Never blindly replay uncertain work. This first
implementation requires a dedicated, initially idle daemon and one open
until-done request at a time; don't submit unrelated work or change the
approved state during execution. Full-access agents remain unsandboxed.

`make contract-check` runs provider-free broker lifecycle tests.
The real-model `contract` scenario in
[Examples / Live / Repository engineering team](https://github.com/tbrandenburg/agentiplex/actions/workflows/repository-team-live.yaml)
executes the native commands above and independently verifies the actual patch.

## Actor-owned contract (direct handovers)

An alternative contract-team setup uses ordinary asynchronous messages: the
[approved objective](objective.actors.md) and [living contract](contract.actors.json)
define artifact owners and evidence. Each actor updates only its owned criteria,
records the next responsible actor with a revision-checked write, then sends a
direct notification. The recipient re-reads the contract before acting. The PM
initiates and closes; it does not relay each handover. This is inspired by
[Syspilot's Contract Driven Development](https://github.com/enthali/syspilot/blob/main/docs/methodology.md);
the shared KV revision replaces a Git commit of the contract itself in this example.

Use a fresh disposable synthetic Clamp repository (same fixture as contract mode).
From the checkout, start the five gateways with the actor roles and policies:

```sh
ACCEPT_FULL_ACCESS=yes CONTRACT_MODE=actors bash examples/repository-team/up.sh /path/to/disposable/git/checkout
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
cd examples/repository-team
```

With bin/ on PATH and after reviewing the objective and checklist, run:

<!-- actors-quickstart:start -->
```sh
agentiplex state put repository objective --file objective.actors.md --revision 0
agentiplex state put repository actors-contract --file contract.actors.json --revision 0
agentiplex send pm --file objective.actors.md --idempotency-key actors-objective --wait
```
<!-- actors-quickstart:end -->

Observe `agentiplex state watch repository`, `agentiplex messages list` and the
dashboard. Here --wait observes only the initial PM delivery, not contract
closure. Wait for status done in actors-contract **and** all deliveries to
complete, then independently verify the actual patch. The tester records the
failed baseline and sends work directly to the developer; the independent tests
and quality review may be done in either useful order. Rework is driven by
evidence. The PM closes only after the owners have supplied current evidence.
There is no runner, until-done, new MCP primitive or middleware responsibility
record.

The [actor prompt](roles/contract-actors.md) and [read-only actor policy](policies/actors-reader.json)
grant direct messaging; the [executable actor policy](policies/actors-writer.json)
enables MCP for full-access developer and tester. The latter's wildcard access
and the trusted-local sender/ownership agreements are not security boundaries.
The launcher only provisions processes, not the work. For native setup without
the launcher, use the five gateway commands below with a separate system prompt
for each actor (prepend "You are actor NAME." to roles/contract-actors.md) and
the actors-reader / actors-writer policies instead of the default roles/policies.

**Recovery boundary:** state and ordinary messages survive a daemon restart with
the same storage; the revision write and notification are not one transaction.
If an actor stops between them, the contract may stall with the new owner named
but not activated. Inspect the last handoff and durable deliveries; if no
notification was admitted and execution is known safe, an operator may submit
the exact recorded target/text/key (`actors-handoff-N`) using ordinary send.
Never blindly replay uncertain work. No automatic wakeup, overall deadline or
native completion receipt is promised for this mode. This example tests
methodology-level collaboration, not full Syspilot/Jarvis feature equivalence.

The live workflow's `actors` scenario executes the approval and submission
commands above and observes revision changes, artifact ownership, actual
handoff deliveries, the failing baseline checkpoint and independent acceptance.
It independently verifies the patch and rejects extra, failed or pending
deliveries. No middleware changes are required.

## Foundations and related work

Here, contract-driven development means an operator-approved, persistent work
specification: scope, acceptance criteria, responsible roles and verification
evidence. Agents choose the next useful action within that scope. A completed
agent turn is not a fulfilled contract; closure additionally requires verified
criteria and no outstanding deliveries. Native until-done messaging supplies bounded continuation, not a workflow engine
or a formal verification system.

Relevant original research and practitioner work:

- Bertrand Meyer, [Applying Design by Contract](https://doi.org/10.1109/2.161279),
  IEEE Computer 25(10), 1992 — explicit obligations, guarantees and invariants.
- Bertrand Meyer, [Contract-Driven Development](https://doi.org/10.1007/978-3-540-71289-3_2),
  FASE 2007 — combines Design by Contract, test-driven development, formal methods
  and automated testing.
- Andreas Leitner, Ilinca Ciupa, Manuel Oriol, Bertrand Meyer and Arno Fiva,
  [Contract Driven Development = Test Driven Development – Writing Test Cases](https://doi.org/10.1145/1287624.1287685),
  ESEC/FSE 2007 — using executable contracts to support automated testing and
  regression detection.
- Gojko Adzic, [Specification by Example](https://www.manning.com/books/specification-by-example),
  Manning, 2011 — shared, concrete acceptance examples and living documentation.
- Reid G. Smith, [The Contract Net Protocol: High-Level Communication and Control in a Distributed Problem Solver](https://doi.org/10.1109/TC.1980.1675516),
  IEEE Transactions on Computers, 1980 — task allocation and coordination among
  distributed problem solvers.
- Edsger W. Dijkstra and Carel S. Scholten,
  [Termination Detection for Diffusing Computations](https://doi.org/10.1016/0020-0190(80)90021-6),
  Information Processing Letters 11(1), 1980 — distinguishing local completion
  from termination of distributed work.

The checklist applies the spirit of software contracts at the level of a
development task, rather than implementing language-level executable
preconditions, postconditions or invariants. The example uses explicit
delegation, not Contract Net bidding, and a bounded delivery-state check in a
dedicated collaboration environment, not the general Dijkstra–Scholten
termination-detection algorithm. These references provide conceptual context;
they are not claims of formal guarantees or protocol conformance.

## Native commands (without the convenience launcher)

### Prepare once

From the Agentiplex checkout, build the binaries and put its `bin/` directory on
PATH in all terminals. Make a fresh private directory and clone the trusted project into it:

```sh
make build
mkdir -m 700 ~/agentiplex-repository
cp -R examples/repository-team/. ~/agentiplex-repository/
cd ~/agentiplex-repository
git clone https://github.com/owner/repo.git workspace
cp -n opencode.json workspace/opencode.json
echo /opencode.json >> workspace/.git/info/exclude
```

Replace the clone URL with your disposable project. Do not overwrite an existing
OpenCode configuration: review it and merge the supplied `mcp` block manually.
Select an approved provider/model in `workspace/opencode.json` (for a public
synthetic project, `"model": {"providerID": "opencode", "model": "big-pickle"}`
is the CI configuration). Keep the role/policy files outside the workspace.
All policy paths and ancestors must be canonical, operator-owned and not
group/world-writable. Review and edit the copied `objective.md`.

### Start the team

Open six terminals in `~/agentiplex-repository`. Start the daemon and wait for its ready log:

```sh
agentiplexd --config team.yaml --storage data
```

Run one gateway per other terminal. **Developer and tester have full OS-user access**;
run them only on the disposable host described above. Wait for all five ready URLs.

```sh
opencode-a2a-lite --name pm --description "Engineering coordinator" --port 19081 --workspace workspace --system-prompt-file roles/pm.md --policy-file policies/coordinator.json --no-env --prompt-timeout 45m
opencode-a2a-lite --name architect --description "Architecture reviewer" --port 19082 --workspace workspace --system-prompt-file roles/architect.md --policy-file policies/reader.json --no-env --prompt-timeout 45m
opencode-a2a-lite --name developer --description "Repository implementation and repair" --port 19083 --workspace workspace --system-prompt-file roles/developer.md --policy-file policies/writer.json --access danger-full-access --no-env --prompt-timeout 45m
opencode-a2a-lite --name tester --description "Independent executable verification" --port 19084 --workspace workspace --system-prompt-file roles/tester.md --policy-file policies/writer.json --access danger-full-access --no-env --prompt-timeout 45m
opencode-a2a-lite --name quality --description "Independent acceptance review" --port 19085 --workspace workspace --system-prompt-file roles/quality.md --policy-file policies/reader.json --no-env --prompt-timeout 45m
```

### Submit and inspect

From another terminal in `~/agentiplex-repository`:

```sh
export AGENTIPLEX_SERVER=http://127.0.0.1:19080
agentiplex register pm http://127.0.0.1:19081
agentiplex register architect http://127.0.0.1:19082
agentiplex register developer http://127.0.0.1:19083
agentiplex register tester http://127.0.0.1:19084
agentiplex register quality http://127.0.0.1:19085
OBJECTIVE="$(cat objective.md)"
agentiplex state put repository objective "$OBJECTIVE" --revision 0 &&
  agentiplex send pm "$OBJECTIVE" --idempotency-key repository-objective --wait
agentiplex notes list repository
```

The [role files](roles/) are actual native system prompts; the card identity is
only descriptive metadata. The operator publishes the exact objective (max 4096
bytes for this shared-state value) once, then sends the same text to the PM.
A failed create blocks submission. The original objective is authoritative and
read-only by working agreement; shared state is not a per-key security boundary.
Use fresh storage for each distinct objective. The PM decides which roles to use;
there is no fixed delegation sequence. Start MCP before the prompted gateways,
because native-role startup opens an ACP session before reporting ready.
If preflight exits, inspect the error and retry the native command only when
appropriate; see the [startup notes](../README.md#known-issue-flaky-startup-preflight).

All five gateway commands explicitly suppress ambient provider keys. With the native commands you can add `--pass-env <PROVIDER>_API_KEY` (an explicit variable name, never a value). Use free models only on public or synthetic code: free-model providers may retain prompts. For private code
select an approved provider/model in the workspace's `opencode.json`, use the native gateway commands with only the required `--pass-env` variable and a provider-side budget.
By default agents leave a patch for the operator. An objective may explicitly authorize local branch/commit/merge operations: see [integration objective](objective.integration.md) for the synthetic CI fixture. Remote publication and history rewriting remain forbidden. The developer prepares and integrates an authorized candidate after the PM confirms independent verification and review; the tester diagnoses and verifies failures but never writes or merges. The developer repairs in-scope CI failures without weakening checks. Full-access gateways can bypass these agreements; enforce external publication restrictions with isolation and credentials, not prompts. Keep role files and runtime configuration trusted and stable during a run. For an already prepared workspace, replace the `--system-prompt-file` argument with `--agent <name>` to select a native primary agent (never both); the same daemon and policies work unchanged.

Timeouts: `--prompt-timeout` bounds one whole agent turn (the PM's turn, all `ask` calls summed, must fit); `timeout.execution`
(ms) in `opencode.json` bounds one tool call, e.g. one `ask`. The daemon's `delivery.timeout` only bounds one HTTP operation to
an agent, not agent execution (see [A2A](../../docs/A2A.md)). Live acceptance has a bounded 20-minute objective deadline; timing and success depend on model and host.

## Independently verify and clean up
Run these commands from the prepared directory (or substitute the launcher-selected checkout for `workspace`). For a patch-only objective, inspect the working-tree diff as below. For an authorized integration objective, record the starting commit (`BASE=$(git -C workspace rev-parse HEAD)`) before submission and inspect `git -C workspace log --graph --oneline "$BASE"..HEAD` and `git -C workspace diff "$BASE" HEAD` afterwards; a clean merged tree has no uncommitted diff.
```sh
git -C workspace status --short && git -C workspace diff HEAD     # the actual change; review untracked files separately
(cd workspace && go test ./...) # example: independently run the project's own trusted checks
```
Run your own approved test command independently, not a command supplied by an agent. Inspect Git status and diff before and after the check; do not accept changes from the verification command without review. Executing repository code runs as your OS user. The automated synthetic-fixture acceptance tests perform the same checks in Go, including a bounded command timeout and Git-state comparison. Agent-reported results are not attested.

Delivery records show which recipient was asked and whether it completed, not which MCP tool was used or whether a command passed. In trusted-local mode note topics are not authenticated authors; the system prompt is not a security boundary.

**Cleanup is yours:** stop only the processes you started (Ctrl+C in each terminal), review the diff, then delete the disposable run directory and storage only after review.

## Secure mode (verified HTTPS + per-agent tokens)
Private-CA forwarding was added upstream in gateway 0.9.1; the authenticated real-team run with the released gateway is not yet verified here (#41). Prerequisites and evidence are in
[../README.md](../README.md#secure-mode-verified-https--per-agent-tokens). For this team, copy `team.yaml` and add the TLS fields and separate operator/viewer credentials. Put each agent's token-file path in the existing `security.agent_token_files` mapping (IDs pm, architect, developer, tester, quality), **not** in a static `registry.agents` roster. Credential identity is independent of endpoint registration. Point the MCP URL and header at the verified HTTPS daemon. Start the daemon first, then each gateway with its own `AGENTIPLEX_TOKEN`, `--pass-env AGENTIPLEX_TOKEN` and the operator-set `NODE_EXTRA_CA_CERTS`. Register the live endpoints with the operator CLI using `--server https://localhost:19080 --ca-file /secure/ca.crt --token-file /secure/operator`. Keep the operator token out of every agent process. This uses existing secure registration capabilities without introducing a startup cycle; the full authenticated real-team run is still unverified (#41).

## Customize and restart
- **Add a team member** (a second tester, a docs reviewer): start another gateway on an unused port with its own system prompt file, `--name NAME` identity and the `reader`/`writer` policy, then run `bin/agentiplex register NAME URL`. Only
  the coordinator needs the delegation policy.
- **Daemon restart:** if the daemon is killed (even SIGKILL) and restarted on the same storage and port, the PM's failed `ask`
  is repeated and re-attaches to the same delivery without duplicate downstream execution (see [MCP](../../docs/MCP.md),
  [ARCH 5.5](../../docs/ARCH.md), [DEPLOYMENT](../../docs/DEPLOYMENT.md)). A model turn in flight in an agent is not resumed.

## Verification
`make examples-check` (provider-free: real daemon with `team.yaml`, native endpoint registration of simulated agents, least-privilege policy checks) and
`make repository-provision-check` (five explicitly prompted real gateways on a synthetic Git workspace, no model task)
live in [tests/examples](../../tests/examples).

Full real-model acceptance is separate, in the path-filtered [Repository team live workflow](https://github.com/tbrandenburg/agentiplex/actions/workflows/repository-team-live.yaml) (also manually runnable). It exercises the same native role options, policies, and objective contract on a synthetic fixture; the manual copy/launcher commands are not executed verbatim. The same five roles and synthetic repository are used for three objectives, each with a fresh run: docs patch, defect patch, and an authorized local defect integration. No separate merge/CI agent is required; the PM coordinates review and integration, the developer fixes failures and integrates, and the tester and quality reviewer remain independent. The tests publish the exact objective using the operator CLI and submit only that objective to the PM, require a completed implementer and independent reviewer, verify the original shared objective text and revision remain unchanged, require relevant notes, and independently check the expected change, Git scope/history and (for defect/integration) fixed test cases. The integration case must start with a reproducibly failing check, finish on clean main without a remote, and contain a genuine two-parent merge of a retained feature branch descended from the original base; the operator reruns the original check and independent assertions on the merged tree. Both tester and quality results/notes are required for integration. The checker does not attest the order or content of model-reported pre-merge reviews or post-merge commands. They do not prove the semantic quality of every review or authenticate note authors. Raw evidence stays in the private run directory; failed CI runs also retain bounded [diagnostics](../README.md#failed-repository-team-live-runs). Run locally only on a disposable host, with no secrets and an approved model:

```sh
make dashboard-assets
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$' -args -live docs -model opencode/big-pickle
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam$' -args -live defect -model opencode/big-pickle
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam```

A failed or timed-out run is not successful collaboration. Inspect the retained private evidence, actual Git diff and observed checks. These live scenarios are skipped by ordinary `make check`; the provider-free Go verifier cases still run there.
 -args -live integration -model opencode/big-pickle
go test -count=1 -v -timeout 45m ./tests/examples -run '^TestLiveTeam```

A failed or timed-out run is not successful collaboration. Inspect the retained private evidence, actual Git diff and observed checks. These live scenarios are skipped by ordinary `make check`; the provider-free Go verifier cases still run there.
 -args -live actors -model opencode/big-pickle
```

A failed or timed-out run is not successful collaboration. Inspect the retained private evidence, actual Git diff and observed checks. These live scenarios are skipped by ordinary `make check`; the provider-free Go verifier cases still run there.

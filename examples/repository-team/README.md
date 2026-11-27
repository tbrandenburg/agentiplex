# Repository engineering team

A third, optional example: independent real OpenCode A2A agents (five by default, see [team.json](team.json)) collaborate
on **one explicitly selected Git workspace** (a fresh scratch worktree or an existing checkout). Unlike the fixture-based
[autonomous-team](../autonomous-team/README.md), this example can produce real
repository changes. It is an operator-side launcher and observer, not a new
Agentiplex runtime, workflow engine, or workspace API. The CLI and daemon only
receive agent IDs, endpoints, collaboration messages and shared state.

> **WARNING — FULL ACCESS:** Agents with `"access": "full-access"` in `team.json` (Developer and Tester by default) can execute arbitrary shell
> commands without per-command approval, edit any files available to your OS user,
> use the network, access forwarded provider credentials, perform Git operations,
> and bypass MCP tool grants. There is no OS sandbox or rollback. Use a disposable
> isolated VM/container/user with minimal credentials, not your everyday development
> environment. The launcher requires explicit `--accept-full-access` even for startup-only checks.

## 1. Prepare a trusted workspace

Use Linux, Go 1.26+, Python 3.10+, Git, GNU Make, Node/npm, Bun 1.4.4, OpenCode
2.3.2 or newer and opencode-a2a-lite 0.9.0 on PATH (same pinned versions as the autonomous
demo). With REPO, the launcher creates its own independent bare clone and detached
clean worktree; with WORKSPACE, the operator supplies a clean checkout. The launcher
never switches branches, commits, pushes, merges, resets or cleans the source
repository or an existing checkout. Full-access agents could perform those
operations; the role instructions ask them not to, but cannot prevent them.

The gateway requires a trusted standalone workspace with reviewed configuration.
Do not use a workspace with untrusted project instructions, escaping symlinks,
unexpected ancestor config, secrets or plugins. Prefer an isolated VM or
container/user with minimal host permissions and credentials; the runtime
profiles are **not an OS sandbox**. All gateways and the dedicated daemon
listen on loopback, without authentication. Never expose them to other users or
a public proxy. The selected workspace is not copied to temporary fixtures.

### Option A: fresh scratch clone (recommended)

```sh
make repository-team REPO=https://github.com/owner/repo.git \
  REF=main \
  OBJECTIVE="Improve the README and check links" \
  REPOSITORY_ARGS="--accept-full-access --accept-provider-data --public-workspace"
```

`REF` is optional (branch or tag; default remote HEAD), `MODEL` is optional (omitted: the OpenCode default is used; CI pins `opencode/big-pickle`), and
`REPO` and `WORKSPACE` are mutually exclusive. HTTPS/SSH URLs without embedded
credentials or an absolute local repository path are accepted. Git uses the
operator's configured credentials for the clone (noninteractive terminal);
no provider keys are forwarded to clone. Use a disposable user with minimal
Git credentials: full-access agents may still reach credentials available to
that user. The clone is bounded to 300 seconds and does not initialize submodules.

Each run gets its own private `$TMPDIR/agentiplex-repository-*` root (default `/tmp`), bare
`repository.git` object database (no shared cache or hardlinks) and detached
`workspace/` linked worktree at the exact cloned commit. Source working-tree
changes are not copied. The launcher sets the scratch origin push URL to
`/dev/null` as an accident guard, not an access control. It creates only an absent
`opencode.json`, locally excluded via the scratch repository's `info/exclude`;
existing tracked or untracked OpenCode config causes a visible refusal, not an
overwrite. Use WORKSPACE with reviewed configuration for such projects.
Clone runs pick a free daemon port when none is given (the daemon starts last, so the port is
chosen up front and written into the workspace config); gateways select their own ports. Concurrent runs of the same repository do not share Git storage,
JetStream or ports. The actual dashboard URL and workspace are printed.
Explicit `--port` collisions fail rather than attach to another daemon.

Scratch clone/worktree and evidence are retained on success or failure; inspect
and optionally create a branch with `git switch -c your-branch` in the printed
workspace after review. Delete the entire private run root when finished, only
after its processes have stopped. No automatic commit or publication.

### Option B: existing local workspace

Prepare a common minimal OpenCode configuration at the workspace root,
`opencode.json` (commit it yourself or explicitly exclude it locally so Git
remains clean; do not overwrite existing configuration blindly). The launcher
reads it but never writes it. Choose the provider and model deliberately. Example
for public or synthetic data only:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "model": {"providerID": "opencode", "model": "big-pickle"},
  "mcp": {
    "timeout": {"execution": 2700000},
    "servers": {
      "agentiplex": {
        "type": "remote",
        "url": "http://127.0.0.1:19080/mcp",
        "protocol": "auto",
        "codemode": false
      }
    }
  }
}
```

No extra configuration fields or MCP servers are accepted by this example.
The gateway independently preflights effective OpenCode policy. Project role
instructions must be trusted and must not attempt to weaken that policy.
For a different launcher port, change the MCP URL to match `--port`. `timeout.execution` (milliseconds) must equal `--mcp-timeout` (default 45m = 2700000); the launcher refuses a different value. A clone run writes this config itself.
No provider credentials belong in this file.

## 2. Start and submit one objective

From the Agentiplex checkout:

```sh
# Optional startup-only check with real gateways; no model task:
make repository-team WORKSPACE=/absolute/trusted/repo \
  REPOSITORY_ARGS="--check --accept-full-access"

# A real edit on public/synthetic input only:
make repository-team WORKSPACE=/absolute/trusted/repo \
  OBJECTIVE="Improve the README and correct broken relative links" \
  REPOSITORY_ARGS="--accept-full-access --accept-provider-data --public-workspace"
```

For private code, select a provider/model approved for that data in
`opencode.json`, and supply only the explicitly approved provider key from
your shell (do not put key values in commands, files or logs):

```sh
make repository-team WORKSPACE=/absolute/trusted/repo \
  OBJECTIVE="Improve the README" \
  REPOSITORY_ARGS="--accept-full-access --accept-provider-data --pass-env OPENAI_API_KEY"
```

Provider/model availability and terms vary. Credential-free live runs require
an explicit OpenCode free model and an attestation that the workspace is safe
to disclose. Never send private/customer source or secrets to free models;
they may retain prompts. Explicit consent is not a privacy guarantee.
Use a dedicated key and provider-side budget. There is no token/cost ceiling
or paid fallback orchestrated by the launcher.

The launcher builds Agentiplex via `make build` under a cooperating source-directory
lock and copies binaries into the private run root before releasing it. It locks
the canonical workspace directory during provisioning, collaboration and final
evidence capture: a second launcher using the same existing checkout fails instead
of concurrently editing it. These advisory locks coordinate this example only,
not arbitrary Git processes or full-access agents. Existing workspace mode uses
port 19080 by default; choose a distinct `--port` and matching local MCP config
for simultaneous different existing workspaces. Timeouts are explicit launcher options, not hidden defaults:

| Option | Default | Meaning |
|---|---|---|
| `--prompt-timeout` | 45m | Gateway deadline for **one whole agent turn**. The PM's entire turn, all `ask` calls summed, must fit in it. |
| `--mcp-timeout` | 45m | OpenCode `mcp.timeout.execution` (OpenCode's own default is 12 h): the longest single tool call, i.e. one `ask`. |
| `--seconds` | 3600 | Launcher wall-clock guard (60–86400), including startup; must exceed `--prompt-timeout`. |

A typical run (architect, developer, tester, quality, one fix round) takes 5–20 minutes, so 45m leaves
headroom; raise all three together for a larger task (`--prompt-timeout 3h --mcp-timeout 3h --seconds 14400`).
The daemon's `AGENTIPLEX_DELIVERY_TIMEOUT=30s` is kept: it bounds one HTTP operation to a gateway, not agent
execution (see [A2A](../../docs/A2A.md)); `AGENTIPLEX_DELIVERY_MAX_ATTEMPTS=80` bounds only pre-submission reachability.
The dashboard URL is printed at startup (unless `--headless`).

## 3. Collaboration and write authority

The team is data: [team.json](team.json), one line per agent (`id`, `access`, `role`):

```json
{"agents": [
  {"id": "pm", "access": "read-only", "role": "Coordinate the work: ..."},
  {"id": "developer", "access": "full-access", "role": "Implement the agreed change ..."}
]}
```

To add an agent (a second tester, a docs reviewer) add a line; no Python changes. Use another file with
`--team path.json`. Rules the launcher derives from the data:

- **Coordinator:** the first agent. It is the only one that receives the delegation tools (`ask`, `send_message`,
  `broadcast`, `dispatch`) and the only one the objective is submitted to. There is no per-agent delegation flag.
- **Grants:** every agent gets the collaboration tools (discovery, delivery lookup, state, `note`, `read_notes`).
  `access` (`read-only` or `full-access`) is the only per-agent safety setting; it selects the gateway
  `--access` profile. MCP grants are least-privilege hygiene, not a boundary (see the warning).
- **Prompt:** the PM gets one short generic prompt (about 600 bytes): role, objective, "discover the available agents and
  delegate according to their capabilities", the independent test/review requirement and the no-commit rules. There is no
  roster, no keys, receipts, waiting or re-attach recipe: discovery and the MCP tool descriptions own those mechanics.
  Workers get no prompt of their own; they see their `role` as their A2A description and the PM's delegated text.
- **Registration:** the launcher starts every gateway first, then the daemon from a generated `daemon.yaml` whose `registry`
  block lists the actual gateway URLs (generated from `team.json`); no `agentiplex register` calls. The gateway preflight does
  not need the daemon (its MCP connection is lazy), which makes this order possible.
- **Gate roles:** every non-coordinator agent (a trailing `-<n>` is ignored, so `tester-1`/`tester-2` count as one `tester`) must produce a
  completed result.

All agents receive the exact same canonical workspace path as gateway `--workspace`; they have independent gateway
processes and sessions, and separate policy files outside the checkout.

For `full-access` agents the launcher passes `--access danger-full-access` after the operator acknowledges the warning.
This permits unrestricted shell execution, including Git, builds, tests, web access and potentially edits outside the
project. The PM's prompt asks that nobody stage, commit, push, merge or switch branches, and the tester role says not to
edit source. These are behavioral requests, not enforcement. The PM can have a failed test fixed and retested. Shared findings go through the
append-only `note`/`read_notes` tools (namespace `repository`). No custom shell MCP server or middleware change.

### Crash restart

Restarting the daemon mid-run continues the team to the PASS gate. The PM's in-flight `ask` fails; the PM repeats it, and it
re-attaches to the same delivery without a second downstream execution (see [MCP](../../docs/MCP.md) and
[ARCH 5.5](../../docs/ARCH.md)). This relies on restarting the daemon on the same storage and port, which is what the
[restart policy](../../docs/DEPLOYMENT.md) of #31 does in a deployment. For this launcher, pass `--restart-daemon` and it
performs that restart for you if the daemon dies (e.g. `kill -9` of the daemon PID in `daemon.log`'s run directory). Without the flag a
dead daemon ends the run as BLOCKED. The agents themselves (the PM's own model turn) are not resumed by the middleware.

The launcher observes authoritative delivery receipts and state. It requires a
completed real PM plus a real result from every other agent (Architect, Developer, Tester, Quality by default),
shared notes, no unresolved or failed deliveries, **durable evidence that the PM delegated work**
(see below) and a Git-visible change. It does not decide detailed engineering work, replay
ambiguous requests, or claim code correctness. If the PM returns before a worker finishes, the
observer prints a warning and waits for the admitted work within the same deadline, and the PASS gate still requires every
delivery to complete. A delivery completing and a tester report existing do not independently
prove that a command ran or passed. Agent-reported commands, exit codes and outputs
must be reviewed; the PASS gate attests collaboration and Git-visible changes, not
correctness. Full-access agents can also modify evidence on disk.

Tool-call evidence: the gateway logs show only the policy grant, so the launcher derives call evidence from the daemon's
delivery records (`evidence.json` → `toolCalls`, printed as "PM tool-call evidence"). A delivery to a worker exists only
because someone submitted it: `delegations`/`delegationCalls` count deliveries per target, `delegationsCompleted` the
completed ones, `callers` the authenticated submitter IDs (`unauthenticated` in the trusted-local mode these examples run in)
and `coordinatorTurns` the PM turns. The delivery record does **not** store which tool created it, so `ask` cannot be
told apart from `send_message`/`dispatch`/`broadcast` and the removed `returnedAt` is not replaced. It is not a transcript of
every PM tool call (note/read_notes calls are visible as `note-` state keys).

## 4. Inspect and actually verify

The launcher prints a private run directory under
`/tmp/agentiplex-repository-*`. It stops only processes it started, retains
JetStream, gateway logs, `evidence.json` (actual receipts and shared state),
`workspace.json`, `git-status.txt`, and `changes.diff` (tracked diff relative
to HEAD). New untracked paths appear in Git status, not in the patch; inspect
their contents separately. It checks final HEAD, branch and index against the initial
state; it cannot prevent or prove the absence of transient Git operations or remote pushes.
Ignored files and untracked file contents are not attested; the check is not
a security boundary. Failed runs can leave partial edits; there is no rollback.

Inspect the actual workspace and evidence:

```sh
git -C /absolute/trusted/repo status --short
git -C /absolute/trusted/repo diff HEAD
# Inspect any new untracked files and review potentially executable changes.
```

The agents can run tests during the collaborative session. After human review, an
operator can optionally independently rerun the project’s **actual** test command
using the separate verification helper. It accepts an explicit argv command
(not a model-generated command or shell eval), compares the tracked Git evidence
with the recorded run, captures real stdout/stderr and exit code in a new
private verification directory, and fails on nonzero exit, timeout, or changed
Git-visible evidence. Example for a Python project with unittest tests:

```sh
python3 -B examples/repository-team/verify.py \
  --workspace /absolute/trusted/repo \
  --run-dir /tmp/agentiplex-repository-REPLACE_WITH_ACTUAL_RUN \
  --accept-run-code -- python3 -m unittest discover
```

Use the actual repository's test command instead. This runs code as the invoking
user, so only approve after reviewing the diff and use an isolated environment.
The verification helper forwards a minimal environment, not provider keys.
A command that changes tracked or untracked Git status is reported as blocked;
inspect generated artifacts separately. Successful exit is evidence of that
command only, not proof all requirements passed. The verification log is
operator-owned and not automatically sent back to the stopped agents.

The original fixture examples remain unchanged. No new middleware features or
third-party Python dependencies are introduced. `make repository-check` runs
provider-free regression tests for the team data file, derived grants, shared workspace binding,
registration, Git evidence, acceptance and operator test execution. Live
repository collaboration is explicitly opt-in and not run against private source
in CI. The Live OpenCode workflow additionally provisions the real gateways on a
clean synthetic Git checkout in both existing-workspace and scratch-clone modes
with explicit full-access acknowledgement and no model submission. Review/redact run data before sharing and delete it yourself when done.

Gateway reference: [v0.9.0 capability profiles](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.9.0/README.md#access-levels)
and [operator policy](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.9.0/docs/operator-policy-state.md).

## 5. Measure it

`measure.py` repeats the whole team N times on its default settings and records PASS/BLOCKED, wall time, PM turns, PM
delegation calls and whether Quality ran. Everything after `--` goes to `team.py` unchanged (do not pass `--seconds` or
objective text that tells the PM when to wait; the point is the defaults):

```sh
make repository-measure N=10 MEASURE_ARGS="--repo /abs/path/or/url --model openai/gpt-5.4-mini --pass-env OPENAI_API_KEY \
  --accept-full-access --accept-provider-data --headless --objective 'Add a subtract function with a test'"
```

Results go to a private directory (`results.md`, `results.json`, one log per attempt). A run that fails before the team is ready
(the known flaky gateway preflight in [../README.md](../README.md)) is relaunched up to `--startup-retries` times and is
reported as `startupAttempts`, not as BLOCKED. Baseline for comparison (polling flow, `openai/gpt-5.4-mini`, 3 runs): 1 BLOCKED (the PM stopped
polling a tester that finished a second later, so Quality never ran), 2 PASS only with `--seconds 1800` and an instruction to wait for the tester.

# Repository engineering team

A third, optional example: six independent real OpenCode A2A agents collaborate
on **one explicitly selected Git workspace** (a fresh scratch worktree or an existing checkout). Unlike the fixture-based
[autonomous-team](../autonomous-team/README.md), this example can produce real
repository changes. It is an operator-side launcher and observer, not a new
Agentiplex runtime, workflow engine, or workspace API. The CLI and daemon only
receive agent IDs, endpoints, collaboration messages and shared state.

> **WARNING — FULL ACCESS:** Developer and both Testers can execute arbitrary shell
> commands without per-command approval, edit any files available to your OS user,
> use the network, access forwarded provider credentials, perform Git operations,
> and bypass MCP role grants. There is no OS sandbox or rollback. Use a disposable
> isolated VM/container/user with minimal credentials, not your everyday development
> environment. The launcher requires explicit `--accept-full-access` even for startup-only checks.

## 1. Prepare a trusted workspace

Use Linux, Go 1.26+, Python 3.10+, Git, GNU Make, Node/npm, Bun 1.4.4, OpenCode
2.3.2 and opencode-a2a-lite 0.8.2 on PATH (same pinned versions as the autonomous
demo). With REPO, the launcher creates its own independent bare clone and detached
clean worktree; with WORKSPACE, the operator supplies a clean checkout. The launcher
never switches branches, commits, pushes, merges, resets or cleans the source
repository or an existing checkout. Full-access agents could perform those
operations; the role instructions ask them not to, but cannot prevent them.

The gateway requires a trusted standalone workspace with reviewed configuration.
Do not use a workspace with untrusted project instructions, escaping symlinks,
unexpected ancestor config, secrets or plugins. Prefer an isolated VM or
container/user with minimal host permissions and credentials; the runtime
profiles are **not an OS sandbox**. All six gateways and the dedicated daemon
listen on loopback, without authentication. Never expose them to other users or
a public proxy. The selected workspace is not copied to temporary fixtures.

### Option A: fresh scratch clone (recommended)

```sh
make repository-team REPO=https://github.com/owner/repo.git \
  REF=main MODEL=opencode/big-pickle \
  OBJECTIVE="Improve the README and check links" \
  REPOSITORY_ARGS="--accept-full-access --accept-provider-data --public-workspace"
```

`REF` is optional (branch or tag; default remote HEAD), `MODEL` is required, and
`REPO` and `WORKSPACE` are mutually exclusive. HTTPS/SSH URLs without embedded
credentials or an absolute local repository path are accepted. Git uses the
operator's configured credentials for the clone (noninteractive terminal);
no provider keys are forwarded to clone. Use a disposable user with minimal
Git credentials: full-access agents may still reach credentials available to
that user. The clone is bounded to 300 seconds and does not initialize submodules.

Each run gets its own private `/tmp/agentiplex-repository-*` root, bare
`repository.git` object database (no shared cache or hardlinks) and detached
`workspace/` linked worktree at the exact cloned commit. Source working-tree
changes are not copied. The launcher sets the scratch origin push URL to
`/dev/null` as an accident guard, not an access control. It creates only an absent
`opencode.json`, locally excluded via the scratch repository's `info/exclude`;
existing tracked or untracked OpenCode config causes a visible refusal, not an
overwrite. Use WORKSPACE with reviewed configuration for such projects.
Clone runs default to a kernel-assigned daemon port; gateways also select their
own ports. Concurrent runs of the same repository do not share Git storage,
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
For a different launcher port, change the MCP URL to match `--port`.
No provider credentials belong in this file.

## 2. Start and submit one objective

From the Agentiplex checkout:

```sh
# Optional startup-only check with six real gateways; no model task:
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
for simultaneous different existing workspaces. Default team deadline 1200s,
gateway prompt timeout 10m; override using `REPOSITORY_ARGS` (e.g. `--seconds 1800`).
The dashboard URL is printed at startup (unless `--headless`).

## 3. Collaboration and write authority

| Agent | Runtime profile | Responsibility |
|---|---|---|
| PM | read-only | Discover, delegate, wait, consolidate |
| Architect | read-only | Analyze project, propose change |
| Developer | full-access | Implement, build, run tests and fix failures |
| Tester 1 / Tester 2 | full-access | Independently execute build/test/lint commands and report results |
| Quality | read-only | Independent review |

All six receive the exact same canonical workspace path as gateway `--workspace`; they have
independent gateway processes and sessions, and separate policy files outside
the checkout. Names and skills are advertised through A2A. The PM receives a
single objective and role guidance, and chooses the details of its delegations.
Only PM receives explicit send/broadcast/dispatch MCP grants. Workers can inspect
receipts and share bounded KV notes. Full-access workers can bypass the MCP grants
through shell/network access to the unauthenticated daemon. Role instructions are
not authenticated identities, filesystem permissions or enforceable ownership.

For Developer and both Testers, the launcher selects gateway `full-access` and
passes `--dangerously-skip-permissions` after the operator acknowledges the warning.
This permits unrestricted shell execution, including Git, builds, tests, debugging,
web access and potentially edits outside the project. Testers are instructed not
to edit source and all agents are instructed not to stage, commit, push, merge or
switch branches. These are behavioral requests, not enforcement. Developer and
testers must sequence work; the PM can delegate a fix and retest after a failure
within the existing six-delegation budget. PM, Architect and Quality retain their
read-only gateway profiles. No custom shell MCP server or middleware change.

The launcher observes authoritative delivery receipts and state. It requires a
completed real PM plus real Architect, Developer, at least one Tester and Quality
result, PM/developer shared-state notes, a completed tester’s shared-state verification
report, no unresolved or failed deliveries,
and a Git-visible change. It does not decide detailed engineering work, replay
ambiguous requests, or claim code correctness. If the PM returns before a worker
finishes, the observer waits within the same bounded deadline. The unused tester
may remain idle. A delivery completing and a tester report existing do not independently
prove that a command ran or passed. Agent-reported commands, exit codes and outputs
must be reviewed; the PASS gate attests collaboration and Git-visible changes, not
correctness. Full-access agents can also modify evidence on disk.

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
provider-free regression tests for shared workspace binding, role grants,
registration, Git evidence, acceptance and operator test execution. Live
repository collaboration is explicitly opt-in and not run against private source
in CI. The Live OpenCode workflow additionally provisions six real gateways on a
clean synthetic Git checkout in both existing-workspace and scratch-clone modes
with explicit full-access acknowledgement and no model submission. Review/redact run data before sharing and delete it yourself when done.

Gateway reference: [v0.8.2 capability profiles](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.8.2/README.md#capability-profiles)
and [operator policy](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.8.2/docs/operator-policy-state.md).

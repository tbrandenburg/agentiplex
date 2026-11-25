# Repository engineering team

A third, optional example: six independent real OpenCode A2A agents collaborate
on **one explicitly selected existing Git workspace**. Unlike the fixture-based
[autonomous-team](../autonomous-team/README.md), this example can produce real
repository changes. It is an operator-side launcher and observer, not a new
Agentiplex runtime, workflow engine, or workspace API. The CLI and daemon only
receive agent IDs, endpoints, collaboration messages and shared state.

## 1. Prepare a trusted workspace

Use Linux, Go 1.26+, Python 3.10+, Git, GNU Make, Node/npm, Bun 1.4.4, OpenCode
2.3.2 and opencode-a2a-lite 0.8.2 on PATH (same pinned versions as the autonomous
demo). The operator selects and prepares a clean, disposable checkout or
worktree on an appropriate branch. The launcher never clones, switches branches,
commits, pushes, merges, resets or cleans your repository.

The gateway requires a trusted standalone workspace with reviewed configuration.
Do not use a workspace with untrusted project instructions, escaping symlinks,
unexpected ancestor config, secrets or plugins. Prefer an isolated VM or
container/user with minimal host permissions and credentials; the runtime
profiles are **not an OS sandbox**. All six gateways and the dedicated daemon
listen on loopback, without authentication. Never expose them to other users or
a public proxy. The selected workspace is not copied to temporary fixtures.

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
make repository-team WORKSPACE=/absolute/trusted/repo REPOSITORY_ARGS=--check

# A real edit on public/synthetic input only:
make repository-team WORKSPACE=/absolute/trusted/repo \
  OBJECTIVE="Improve the README and correct broken relative links" \
  REPOSITORY_ARGS="--accept-provider-data --public-workspace"
```

For private code, select a provider/model approved for that data in
`opencode.json`, and supply only the explicitly approved provider key from
your shell (do not put key values in commands, files or logs):

```sh
make repository-team WORKSPACE=/absolute/trusted/repo \
  OBJECTIVE="Improve the README" \
  REPOSITORY_ARGS="--accept-provider-data --pass-env OPENAI_API_KEY"
```

Provider/model availability and terms vary. Credential-free live runs require
an explicit OpenCode free model and an attestation that the workspace is safe
to disclose. Never send private/customer source or secrets to free models;
they may retain prompts. Explicit consent is not a privacy guarantee.
Use a dedicated key and provider-side budget. There is no token/cost ceiling
or paid fallback orchestrated by the launcher.

The target builds Agentiplex first. Alternatively, after `make build`, use
`python3 -B examples/repository-team/team.py --workspace ... --objective ...`
with the same flags. Default team deadline 1200s, gateway prompt timeout 10m;
override using `REPOSITORY_ARGS` (e.g. `--seconds 1800`). Dashboard:
http://127.0.0.1:19080/ui/ (unless `--headless`).

## 3. Collaboration and write authority

| Agent | Runtime profile | Responsibility |
|---|---|---|
| PM | read-only | Discover, delegate, wait, consolidate |
| Architect | read-only | Analyze project, propose change |
| Developer | edit | Implement change in the actual workspace |
| Tester 1 / Tester 2 | read-only | Inspect actual code/test changes, recommend verification |
| Quality | read-only | Independent review |

All six receive the exact same canonical path as `--workspace`; they have
independent gateway processes and sessions, and separate policy files outside
the checkout. Names and skills are advertised through A2A. The PM receives a
single objective and role guidance, and chooses the details of its delegations.
Only PM has send/broadcast/dispatch grants. Workers can inspect receipts and
share bounded KV notes; role labels and the PM's instructions are not
authenticated identities or filesystem permissions.

The edit profile does not allow shell, Git or test execution. The gateway's
full-access profile would grant unrestricted shell and host-user authority;
this example **never enables it**. Tester agents perform review, not executable
tests, and must label suggested commands NOT RUN. This is an intentional
limitation, not a simulated successful test. The launcher does not add a custom
shell MCP server or bypass hard policy denials. Review all changes before
executing repository code; edits can affect hooks, scripts or future builds.

The launcher observes authoritative delivery receipts and state. It requires a
completed real PM plus real Architect, Developer, at least one Tester and Quality
result, PM/developer shared-state notes, no unresolved or failed deliveries,
and a Git-visible change. It does not decide detailed engineering work, replay
ambiguous requests, or claim code correctness. If the PM returns before a worker
finishes, the observer waits within the same bounded deadline. The unused tester
may remain idle. Only the Developer receives edit capability; this is tool
hardening, not enforceable artifact ownership or OS isolation.

## 4. Inspect and actually verify

The launcher prints a private run directory under
`/tmp/agentiplex-repository-*`. It stops only processes it started, retains
JetStream, gateway logs, `evidence.json` (actual receipts and shared state),
`workspace.json`, `git-status.txt`, and `changes.diff` (tracked diff relative
to HEAD). New untracked paths appear in Git status, not in the patch; inspect
their contents separately. It checks HEAD, branch and index did not change.
Ignored files and untracked file contents are not attested; the check is not
a security boundary. Failed runs can leave partial edits; there is no rollback.

Inspect the actual workspace and evidence:

```sh
git -C /absolute/trusted/repo status --short
git -C /absolute/trusted/repo diff HEAD
# Inspect any new untracked files and review potentially executable changes.
```

After human review, an operator can run the project's **actual** test command
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
in CI. Review/redact run data before sharing and delete it yourself when done.

Gateway reference: [v0.8.2 capability profiles](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.8.2/README.md#capability-profiles)
and [operator policy](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.8.2/docs/operator-policy-state.md).

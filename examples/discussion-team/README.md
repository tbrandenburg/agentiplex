# Three-agent discussion

Follow the [main README quickstart](../../README.md#three-real-agents).
The executable command blocks live there, not in a second set of instructions.

[![Three-agent quickstart](https://github.com/tbrandenburg/agentiplex/actions/workflows/discussion-quickstart.yaml/badge.svg?branch=main)](https://github.com/tbrandenburg/agentiplex/actions/workflows/discussion-quickstart.yaml)

This directory contains the prepared native OpenCode configuration, three actual
system prompts (`roles/`) and narrowly scoped read-only policies (`policies/`).
Agentiplex and OpenCode remain independent. No team launcher, extra daemon
configuration or orchestration code is needed.

## Operating notes

- Use the pinned gateway 0.9.3, OpenCode 2.4.1 and Bun 1.4.4 from the quickstart.
  Wait for successful readiness, not just preflight. If startup exits, inspect
  the failure and retry the native command only when appropriate.
- No provider credentials are forwarded. The free model may retain prompts:
  use only public or synthetic topics in a trusted, disposable environment.
  A read-only profile is not an OS sandbox. Policies must be outside the
  workspace, operator-owned, canonical and not group/world-writable.
- For another discussion, use a fresh private copy and storage, or choose
  a different note namespace in all role prompts and use a new idempotency key.
  Repeating an identical key/payload while retained reattaches to the same
  work; it does not start a new discussion.
- `agentiplex notes list discussion expert` filters notes by topic;
  `--json` on notes or `messages wait` gives machine-readable output.
  Trusted-local note topics are not authenticated authors.

## Continue until the objective is done

With the same three running read-only agents and after ordinary deliveries have
completed, review the synthetic [decision contract](contract.md) in the private
copy created by the quickstart. From that directory, run:

<!-- discussion-contract:start -->
```sh
agentiplex send lead --file contract.md --idempotency-key discussion-contract-1 --until-done --max-turns 6 --timeout 12m --wait
```
<!-- discussion-contract:end -->

No runner or inactivity nudge is required. The initial incomplete analysis and
independent critique are recorded in state; the lead returns from the first
turn, then native continuation reactivates it to resolve the findings. In
another terminal inspect `agentiplex state watch discussion-contract` and
`agentiplex messages list`. Ctrl-C detaches; `messages wait MESSAGE_ID`
reattaches. Use a dedicated daemon, only one open until-done request, and
public/synthetic input. The lead has state and resolution tools for this mode;
expert and critic retain their original read-only policies.

## Verification

The [dedicated workflow](../../.github/workflows/discussion-quickstart.yaml)
installs the pinned runtime and builds the binaries. The opt-in Go test reads
the actual setup-copy, daemon, gateway, registration, send/wait and notes
commands from the main README, runs them in an isolated private home and
verifies the durable deliveries and notes plus readable CLI output.
It requires real results from the lead, expert and critic, and no failed
or pending deliveries. The same live test then executes the until-done command above, checks the recorded incomplete checkpoint, multiple coordinator activations, distinct expert and critic evidence before and after revision, and explicit final resolution. Startup alone is not a pass. It does not attest
individual tool calls, note authorship or semantic correctness.

After the README setup's build and install commands, run locally on a
disposable Linux host with free ports (uses real model inference):

```sh
go test -count=1 -v -timeout 35m ./tests/examples -run '^TestLiveTeam$' -args -live discussion
```

Ordinary `make check` remains provider-free. CI records bounded output and
delivery/notes summaries; private process logs stay in the test run directory.
A red badge is a failed live acceptance run, not necessarily a middleware bug.

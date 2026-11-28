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

Follow the [contract-driven collaboration quickstart](../../README.md#contract-driven-collaboration) in the main README. It owns the executable command; review the [decision contract](contract.md) before submitting it.

No runner or inactivity nudge is required. The initial incomplete analysis and
independent critique are recorded in state; the lead returns from the first
turn, then native continuation reactivates it to resolve the findings. In
another terminal inspect `agentiplex state watch discussion-contract` and
`agentiplex messages list`. Ctrl-C detaches; `messages wait MESSAGE_ID`
reattaches. Use a dedicated daemon, only one open until-done request, and
public/synthetic input. The lead has state and resolution tools for this mode;
expert and critic retain read-only OS profiles. Their direct messaging and state grants are also used by the actor-owned example below.

## Actor-owned contract (direct handovers)

Follow the [native commands in the main README](../../README.md#actor-owned-contract-direct-handovers).
There is no contract-mode setting, new daemon, gateway restart or launcher.
The initial objective and subsequent contract-reference messages select the
actor-owned behavior in the same three role prompts. The expert owns the
draft and critic owns the review. The immutable objective is separate from the
living Markdown checklist. Each actor works on unchecked criteria it owns and
blocking findings, checks a box only with evidence, and updates the document
with its observed revision before sending the next actor a normal
asynchronous message. An intentional incomplete draft makes the critic
reject it and return work directly to the expert; the lead closes after
independent acceptance of the revised draft.

This demonstrates the collaboration pattern of
[Syspilot's Contract Driven Development](https://github.com/enthali/syspilot/blob/main/docs/methodology.md)
using shared KV rather than Git commits of the contract. Artifact ownership
and sender identity are working agreements in trusted-local mode; the read-only
policies now allow state writes and ordinary send_message for all three agents,
but only lead has blocking ask and resolve_request grants. No OS write access
is added.

The initial send --wait is not an overall completion receipt. Watch contract
status and delivery records; require all five boxes checked, status done and
all deliveries completed. The local Markdown file is only the initial template;
inspect the actual shared document with `agentiplex state get discussion-actors contract`.
A contract write and its subsequent message admission are separate durable
operations. If the sender crashes between them, the new owner may not be
activated. Inspect the last handoff and durable receipts; only if the
notification was not admitted and execution is known safe, submit its exact
target/text/key (discussion-actors-handoff-N) using ordinary send. Never blindly
replay uncertain work. No automatic wakeup or middleware-managed ownership is
promised. This is methodology-level evidence, not full Jarvis/Syspilot parity.

## Verification

The [dedicated workflow](../../.github/workflows/discussion-quickstart.yaml)
installs the pinned runtime and builds the binaries. The opt-in Go test reads
the actual setup-copy, daemon, gateway, registration, send/wait and notes
commands from the main README, runs them in an isolated private home and
verifies the durable deliveries and notes plus readable CLI output.
It requires real results from the lead, expert and critic, and no failed
or pending deliveries. The same live test then executes the until-done command from the main README, checks the recorded incomplete checkpoint, multiple coordinator activations, distinct expert and critic evidence before and after revision, and explicit final resolution. The same agents then execute the actor-owned commands and the observer checks every Markdown revision, immutable criterion labels, ownership of checkbox changes, evidence-backed checks, direct expert-to-critic-to-expert rework, accepted current draft and all distinct ordinary delivery receipts, without scheduling any work. Startup alone is not a pass. It does not attest
individual tool calls, note authorship or semantic correctness.

After the README setup's build and install commands, run locally on a
disposable Linux host with free ports (uses real model inference):

```sh
go test -count=1 -v -timeout 50m ./tests/examples -run '^TestLiveTeam$' -args -live discussion
```

Ordinary `make check` remains provider-free. CI records bounded output and
delivery/notes summaries; private process logs stay in the test run directory.
A red badge is a failed live acceptance run, not necessarily a middleware bug.

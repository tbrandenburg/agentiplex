# Three-agent discussion

Three read-only agents, one topic, and durable shared notes. No launcher or
orchestration script. Agentiplex and OpenCode remain independent.

## Setup once

Build this revision with `make build` and put the checkout's `bin/` on PATH
(or install a release containing the new wait/notes commands). Install Bun 1.4.4, Node 22+ and the verified
runtime pair:

```sh
npm install -g @opencode/cli@2.4.1 @tbrandenburg/opencode-a2a-lite@0.9.2
mkdir -m 700 ~/agentiplex-discussion
cp -R examples/discussion-team/. ~/agentiplex-discussion/
```

Run the copy command from the Agentiplex checkout. Use a fresh private directory
with non-group/world-writable ancestors, canonical paths (no symlink components), and review the copied workspace,
role files and policies. The config uses a free model; only discuss public or
synthetic topics. No provider credentials are forwarded. The gateway's read
access is not an OS sandbox. The local HTTP ports are unauthenticated; do not
expose them. Ports 8080 and 8001–8003 must be free.

## Start the team

Open four terminals, each in `~/agentiplex-discussion`. Run the daemon first
and wait for its ready log:

```sh
agentiplexd --mcp --storage data
```

In the other three terminals, run one gateway command each. Wait for all three
to print their ready URLs; retry an exited preflight as described in
[setup](../README.md#known-issue-flaky-startup-preflight).

```sh
A2A_NAME=lead A2A_DESCRIPTION="Discussion coordinator" opencode-a2a-lite --workspace "$PWD/workspace" --port 8001 --system-prompt-file roles/lead.md --policy-file "$PWD/policies/coordinator.json" --no-env --prompt-timeout 10m
A2A_NAME=expert A2A_DESCRIPTION="Independent expert" opencode-a2a-lite --workspace "$PWD/workspace" --port 8002 --system-prompt-file roles/expert.md --policy-file "$PWD/policies/reader.json" --no-env --prompt-timeout 10m
A2A_NAME=critic A2A_DESCRIPTION="Independent critic" opencode-a2a-lite --workspace "$PWD/workspace" --port 8003 --system-prompt-file roles/critic.md --policy-file "$PWD/policies/reader.json" --no-env --prompt-timeout 10m
```

## Discuss

In another terminal (using the default local daemon address):

```sh
agentiplex register lead http://127.0.0.1:8001
agentiplex register expert http://127.0.0.1:8002
agentiplex register critic http://127.0.0.1:8003
agentiplex send lead "Should we use event sourcing?" --idempotency-key discussion-1 --wait
agentiplex notes list discussion
```

The lead decides how to coordinate the topic, the expert and critic record
their independent notes, and the lead returns a verdict. The wait prints the
delivery ID before blocking; Ctrl-C only detaches. Reattach with
`agentiplex messages wait DELIVERY_ID` (or repeat the identical send with the
same key while retained). Use `--json` on wait/notes for machine-readable data.
Notes are shared, recent and persistent; for a different discussion use fresh
storage or explicitly separate note namespaces in the prompts. A delivery
completing is not proof of a sound verdict; review the actual notes.

See the live dashboard at http://127.0.0.1:8080/ui/. Stop gateways and then the
daemon with Ctrl-C in their terminals; the data remains for restart.

This is a documented native-command example, not a new runtime or a scripted
team. Three-real-agent inference has not yet been validated for this exact
example. The pinned gateway still requires explicit policy and role flags.

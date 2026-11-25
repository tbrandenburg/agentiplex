# Autonomous engineering team (PR #8)

An optional, disposable integration example, not a new agent runtime or workflow engine. The existing simulated engineering team remains the default CI demo. Here the launcher provisions the team, sends **one objective** to the PM through Agentiplex, then reads delivery/state records; it does not pick downstream collaborators or relay their responses.

## Team profiles

- `configured` (default): real PM and Architect, four simulated workers.
- `real`: six independent real OpenCode instances.
- `simulated`: six deterministic fixtures, provisioning-only (`--check`); not autonomous.

Edit team.json to choose individual runtimes, roles, group and pool memberships. The format is example-local JSON (Python standard library, no YAML dependency), not a core agent definition. PM is the only delegator and is excluded from collections. Real workers receive exact MCP permissions for discovery, delivery inspection and shared KV state read/write; no send/broadcast/dispatch. They can contribute and consult shared state independently. This first demo intentionally restricts delegation depth to one, not arbitrary peer-to-peer synchronous delegation. Policies are generated from the role configuration outside each workspace; the gateway's existing read-only profile and exact operator exceptions are reused. Do not bypass permissions.

## Provider-free setup validation

Go 1.26+, Python 3.10+, Linux/macOS for simulator-only. For the UI, Node/npm is needed only to package AMIS at build time.

```sh
python3 -B examples/autonomous-team/test_team.py
python3 -B examples/autonomous-team/team.py --profile simulated --check --headless
# or task autonomous-check
```

This validates generated configuration, explicit per-role grants, registration and collection setup, and process cleanup. It never calls a model. Go integration tests separately exercise an accepted A2A task initiating MCP discovery/delegation and awaiting a second A2A response while the original task is active, then persisting the actual receipt through MCP.

## Real-model acceptance (live, potentially billable)

On a disposable trusted Linux host, install the exact reviewed versions: OpenCode **2.3.2**, Bun **1.4.4**, Node **22+**, and [opencode-a2a-lite **0.8.1**](https://github.com/tbrandenburg/opencode-a2a-lite/releases/tag/v0.8.1) on PATH. The gateway CLI must report 0.8.1 and OpenCode must report opencode v2.3.2. Use a standalone workspace environment without managed /etc/opencode or inherited project configuration; see the upstream [operator policy](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.8.1/docs/operator-policy-state.md). The launcher creates private standalone workspaces under /tmp, each containing only the fictional fixture and generated role/config. It does not copy your real repository or personal files.

Set an explicitly approved provider API key in your shell (do not paste it into files or command arguments). Configure a valid provider/model ID for that account. Optional gateway startup-only preflight (no intended inference):

```sh
python3 -B examples/autonomous-team/team.py --model openai/YOUR_MODEL \
  --pass-env OPENAI_API_KEY --check
```

Then run the actual hybrid team:

```sh
python3 -B examples/autonomous-team/team.py --model openai/YOUR_MODEL \
  --pass-env OPENAI_API_KEY --accept-live-cost --seconds 600
```

Replace the example model identifier with an available model. For six live workers add `--profile real` (higher cost). A custom objective can be passed with `--objective`. Only explicitly named nonempty provider *_API_KEY variables are passed to the gateway. The launcher does not send provider secrets to Agentiplex/simulators. No fallback to simulation on real gateway failure. The gateway is not an OS sandbox; use an isolated host/user with minimal credentials, reviewed trusted binaries and no untrusted local clients. All ports are loopback; do not publish/proxy them. For TLS network deployments see the separate deployment documentation.

The PM receives an open-ended review objective about the fictional controller fixture. It chooses agents based on their advertised capabilities. Workers inspect the fixture, return evidence, and write shared review notes via native MCP state tools. No edits, shell, external access, plugins, unrestricted subagents, automatic session resume, or release approval. The model's choices may fail to meet the acceptance conditions, in which case the run is BLOCKED, not a synthetic success.

During the run open http://127.0.0.1:19080/ui/ (or chosen --port) to inspect real registry, deliveries and events. At completion, the observer saves a private evidence.json containing the original PM result, actual child/direct/dispatch receipts and shared state values/revisions. The PM result must cite an actual completed real worker receipt. The gate requires a real PM and at least one completed independent real worker, no unresolved deliveries, and actual persisted PM and worker state. Manually review output for file evidence, honest simulation labels, useful conclusions and absence of invented results. State and roles are not authenticated identities; a controlled fresh local run with no other callers is required to attribute activity to the team. This does not prove model quality or safety compliance.

## Boundaries and cleanup

The wall-clock deadline (60–1800 seconds, including service startup, excluding bounded builds/version probes) stops owned processes. Role instructions request at most six delegated operations and twelve receipt polls per operation; the read-only observer stops on more than six observed top-level delegations, more than sixteen observed recipients, or any delegation back to PM. The observation is a guardrail, not an atomic admission quota; in-flight work may exceed it. There is no guaranteed token or cost ceiling; configure a provider-side budget and use a dedicated key. Workers cannot delegate via their configured gateway MCP policy. This is not generalized core loop detection, authenticated attribution, or an OS security boundary. Cancellation is not rollback, and stopping a process does not prove a provider request stopped immediately.

The launcher stops its own foreground children on exit or Ctrl+C; the private run directory, logs, JetStream and evidence remain for operator inspection. Do not publish them without redaction. Delete the printed directory yourself after review. A failed or timed-out run preserves its logs and state; no automatic replay. Live model acceptance is not part of ordinary CI and must be performed with the operator's permission and credentials.

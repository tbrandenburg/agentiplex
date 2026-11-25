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
# or make autonomous-check
```

This validates generated configuration, explicit per-role grants, registration and collection setup, and process cleanup. It never calls a model. Go integration tests separately exercise an accepted A2A task initiating MCP discovery/delegation and awaiting a second A2A response while the original task is active, then persisting the actual receipt through MCP.

## Real-model acceptance (free or credentialed)

`make live-check` runs the credential-free full hybrid scenario of Live OpenCode CI, using the OpenCode default model unless `MODEL=provider/model` is set (CI sets `MODEL=opencode/big-pickle`). Install the pinned runtimes below first. Override `LIVE_ARGS` for a different approved local scenario; no paid provider credentials are forwarded by default.

On a disposable trusted Linux host, install the exact reviewed versions: OpenCode **2.3.2 or newer**, Bun **1.4.4**, Node **22+**, and [opencode-a2a-lite **0.8.3**](https://github.com/tbrandenburg/opencode-a2a-lite/releases/tag/v0.8.3) on PATH. The gateway CLI must report 0.8.3 and OpenCode must report a stable opencode v2.3.2 or newer. Use a standalone workspace environment without managed /etc/opencode or inherited project configuration; see the upstream [operator policy](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.8.3/docs/operator-policy-state.md). The launcher creates private standalone workspaces under /tmp, each containing only the fictional fixture and generated role/config. It does not copy your real repository or personal files.

OpenCode offers [rotating free models](https://opencode.ai/docs/console/models/) including Big Pickle at the time of writing. For an isolated credential-free run, omit --model to use OpenCode's default, or explicitly select an available free model. No provider variable is forwarded. The free catalog, rate limits and service availability may change. Use only the bundled synthetic fixture, never private or customer source: free-model providers may retain prompts or use them to improve models.

```sh
# Native OpenCode default selection, without any API key:
python3 -B examples/autonomous-team/team.py --accept-free-model
# Reproducible explicit request (Live OpenCode CI sets MODEL=opencode/big-pickle):
python3 -B examples/autonomous-team/team.py --model opencode/big-pickle --accept-free-model --headless --summary-only --prompt-timeout 10m --seconds 900
```

Gateway v0.8.2 exposes --prompt-timeout and defaults to 60 minutes (upstream issue #98). The launcher forwards that option to every real gateway. The CI workflow now attempts the original full hybrid review with a ten-minute gateway prompt deadline and a separate fifteen-minute team deadline. The optional --smoke profile retains all six registrations but narrows the live objective to one real architecture consultation, using reduced tool permissions and concise role prompts. The PM is instructed to discover the actual worker, submit the real task, read the actual result, and store and cite its receipt; the worker is instructed to read the fixture and persist its finding. CI verifies actual completed delivery records and persisted state, rather than relying on the model reproducing a receipt ID. The optional smoke is a live protocol check, not acceptance of the full open-ended team review. CI runs the full objective; pass --smoke for the narrower test. The separate **Live OpenCode** workflow runs this hybrid acceptance on PR changes to the example, its relevant interfaces or the workflow; it also supports manual runs. It installs exact runtime versions, has read-only repository permissions, uses no provider secrets, limits execution, and fails visibly if the free service, gateway or model collaboration fails. Keep it separate from required deterministic Checks because upstream free inference is nondeterministic. CI records terminal states, state keys/revisions, verified real worker receipt IDs, advisory citation checks and known gateway failure diagnostics, but does not publish full prompts, state values or logs. The initial v0.8.1 live run reached four downstream completions including the real Architect, but the PM failed near the former 120-second gateway deadline; its exact terminal reason was not retained. A later short failure occurred before delegation, so a longer timeout is not a guarantee of provider/model availability. A requested model identifier is not a verified runtime model receipt; the gateway does not expose authoritative model selection in A2A results.

For credentialed mode, set an explicitly approved provider API key in your shell (do not paste it into files or command arguments). Configure a valid provider/model ID for that account. Optional gateway startup-only preflight (no intended inference):

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

During the run open http://127.0.0.1:19080/ui/ (or chosen --port) to inspect real registry, deliveries and events. At completion, the observer saves a private evidence.json containing the original PM result, actual child/direct/dispatch receipts and shared state values/revisions. The gate requires a real PM and at least one completed independent real worker with stored results, no unresolved deliveries, and actual persisted PM and worker state. The observer records the verified real worker receipt IDs directly from the delivery records and stores them in evidence.json. Whether the PM prose contains an exact receipt ID is an advisory quality check only: a missing citation emits a warning, but does not fail an otherwise successful infrastructure run. A model claiming a receipt without an actual completed worker delivery still fails. Manually review output for file evidence, honest simulation labels, useful conclusions and absence of invented results. State and roles are not authenticated identities; a controlled fresh local run with no other callers is required to attribute activity to the team. This does not prove model quality or safety compliance.

## Boundaries and cleanup

The wall-clock deadline (60–1800 seconds, including service startup, excluding bounded builds/version probes) stops owned processes independently of the gateway's --prompt-timeout (default 60m; finite duration or explicit unlimited). An unlimited gateway deadline does not disable this launcher guard. CI uses --summary-only to avoid printing raw model output. Role instructions request at most six delegated operations and twelve receipt polls per operation; the read-only observer stops on more than six observed top-level delegations, more than sixteen observed recipients, or any delegation back to PM. The observation is a guardrail, not an atomic admission quota; in-flight work may exceed it. There is no guaranteed token or cost ceiling; configure a provider-side budget and use a dedicated key. Workers cannot delegate via their configured gateway MCP policy. This is not generalized core loop detection, authenticated attribution, or an OS security boundary. Cancellation is not rollback, and stopping a process does not prove a provider request stopped immediately.

The launcher stops its own foreground children on exit or Ctrl+C; the private run directory, logs, JetStream and evidence remain for operator inspection. Do not publish them without redaction. Delete the printed directory yourself after review. A failed or timed-out run preserves its logs and state; no automatic replay. Credential-free live acceptance runs in the separate Live OpenCode workflow. Credentialed runs remain operator-only. No paid fallback or API keys in that workflow; a failed live run is not a provider-free success.

### Completion and outstanding work

The observer does not stop the team solely because the PM returns. After a completed PM result it waits for all already-admitted deliveries (including fan-out children) to reach terminal states within the original team deadline. Intermediate evidence is retained if that deadline expires. Failed, uncertain or incomplete deliveries still fail acceptance; no tasks are automatically replayed. If a worker finishes after the PM, the evidence and warning distinguish completed transport from a PM assessment that may not include the late result. Review that result separately.

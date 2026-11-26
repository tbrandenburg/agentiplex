# Engineering-team demonstrator

## Run the provider-free team

Requires Go 1.26+, Bash, curl and jq, plus Node/npm for first-time AMIS asset packaging. No models, credentials, Docker or IDE required. Built daemon binaries need no Node, CDN or network for the UI.

```sh
bash examples/engineering-team/demo.sh
# or: make demo-team
```

Open **http://127.0.0.1:18080/ui/**. The driver builds the daemon, CLI and simulator, creates a temporary JetStream data directory, and starts independent loopback A2A processes. It executes the scenario below, then leaves the services running until Ctrl+C. Use `--check` to exit after assertions or `--port 19080` to shift the contiguous seven-port range. The temporary state and owned processes are cleaned up on exit; do not use this script for durable production state. To see changes as they happen, open the UI when the driver prints its URL. On any failed assertion the driver exits nonzero.

| Agent ID | Advertised skill | Role-specific deterministic fixture |
|---|---|---|
| pm | planning | Assign reviews |
| architect | architecture | Identify traceability and verification considerations |
| developer | development | Propose implementation approach, no edits |
| tester-1 | testing | Propose timing/restart test plan |
| tester-2 | testing | Same capability, independent endpoint |
| quality | quality | Identify evidence and approval requirements |

All simulated responses and cards identify themselves as simulated. They are not model reasoning, executed tests, code changes, measured timing or safety evidence. The demo driver supplies the routing decisions; Agentiplex itself does not orchestrate workflows.

## Scenario

A fictional requirement asks that a motor stop within 100 ms of communication loss and prohibit automatic restart. The seven top-level operations are a PM plan, architecture review (native A2A ingress), developer handoff including actual returned architecture text, broadcast, two pool dispatches, and a Quality delivery while Quality is offline. The driver validates catalog/discovery, fan-out cardinality and completed child states, two distinct pool recipients, and the same queued delivery surviving daemon restart and completing when Quality restarts. These are communication tests, not safety validation.

The dashboard is a read-only live view: registered agents (not online claims), groups/pools, recent top-level states with aggregate child status, selected original stored detail, and recent retained events. Counts are limited to the latest 50 top-level deliveries. Uncertain/partial outcomes are not success. AMIS owns all visual components and automatic polling. The existing SSE API remains available for CLI and external consumers. Disable it with `--dashboard=false` or observability.dashboard=false in YAML. The trusted-local API is not authenticated; do not expose publicly.

## Optional real A2A Architect

Any independently hosted A2A endpoint can replace the simulated Architect. The example uses OpenCode behind the [opencode-a2a-lite v0.9.0](https://github.com/tbrandenburg/opencode-a2a-lite/releases/tag/v0.9.0) gateway purely as a driver (verified upstream release and documented interface; the separate autonomous-team example tests live model interoperability in CI; this individual driver path is not separately live-verified).

On a supported Linux host, with Node 22+, Bun 1.4.4, OpenCode >=2.2.7 on PATH, and a reviewed standalone disposable workspace with configured provider/model:

```sh
# Separate terminal, outside a gateway repository checkout.
# Export a provider key yourself; do not paste it into this repository.
A2A_NAME=Architect npx --yes @tbrandenburg/opencode-a2a-lite@0.9.0 \
  --workspace /absolute/path/to/trusted/disposable/workspace \
  --port 41241 --pass-env OPENAI_API_KEY --access read
```

Use its actual ready URL, then in the Agentiplex checkout:

```sh
bash examples/engineering-team/demo.sh --opencode-url http://127.0.0.1:41241
```

The driver registers the real gateway as architect and leaves the other five agents simulated. It invokes the gateway only for the explicit read-only review and excludes it from the broadcast to avoid repeated model calls. The actual response is forwarded to the simulated developer and displayed; any live failure fails the demo rather than falling back to a fixture. The gateway process is not started or stopped by Agentiplex. No provider credentials are passed to Agentiplex. Review gateway configuration and limitations: its read-only profile is not an OS sandbox; use minimal credentials and an isolated workspace, and never expose its unauthenticated loopback port through a proxy. The normal gateway CLI currently fails closed on macOS per upstream docs. Do not enable activity, resume, edit or permission bypass for this demo.

## Validation

Checks builds and tests the existing integration suite and executes the entire provider-free scenario with real processes and persistent JetStream. An optional `--browser-check` uses Node 22+ and installed Google Chrome (override CHROME_BIN) via the browser's DevTools protocol to verify the actual rendered counts, live event stream and detail view; CI uses this option. No model credentials are needed. The optional real-agent run is a separate manual acceptance gate and is not represented as verified by simulated CI.

## Configure the dashboard

See [DASHBOARD.md](DASHBOARD.md) for AMIS schema, build-time assets, themes and refresh configuration. The browser test asserts the real AMIS-rendered interface and automatic refresh after a group update.

## Container team

See [DEPLOYMENT.md](DEPLOYMENT.md) for the six independent simulators on an isolated Docker Compose network with authenticated Agentiplex, TLS and a persistent external JetStream volume. The native scripted demonstration is unchanged.

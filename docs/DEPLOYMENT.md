# Deployment and secure connectivity

## Supported modes

- Native trusted-local: embedded JetStream, loopback, optional MCP. Existing demo works unchanged.
- Native secured: verified HTTPS and mounted static role credentials; embedded or external JetStream.
- Compose: one secured daemon, official NATS 2.15.3-alpine with TLS/token and persistent named volume, six separate simulator containers. Only the daemon HTTPS port is published, to host loopback.
- Helm: one daemon Deployment (Recreate, non-root, read-only filesystem) and ClusterIP service; external NATS installed independently with its official Helm chart, persistent PVC. No custom controller or Kubernetes-specific core code.

These are single-daemon deployments, not HA. Never run a second daemon in the same JetStream namespace. Network policies/firewalls, resource sizing, backup, broker accounts/ACLs, certificate issuance, secret rotation and operational hardening remain operator responsibilities.

## Distributed deployment (agents on other hosts)

```
 agent host A..N                               daemon host
 +-----------------------+   HTTPS  MCP/A2A   +---------------------------+
 | independent A2A agent | -----------------> | agentiplexd (one)         |
 | + MCP/A2A client      | <----------------- |  - public_url, TLS        |
 +-----------------------+  HTTPS  A2A send   |  - embedded or external   |
                            (+ GetTask polls)  |    JetStream (private)    |
                                               +---------------------------+
```

- One daemon per namespace, one JetStream (embedded, or external on a private network). Agents never connect to the broker; the embedded broker listens on a loopback ephemeral port only.
- Two HTTPS paths are required and independent: caller to daemon (`/mcp`, `/a2a`, REST; the agent's own bearer token) and daemon to every recipient (card fetch, SendMessage, GetTask; per-origin outbound bearer token). One administrative trust domain; shared state is shared (see principal model).
- While the daemon is down nothing is admitted and callers must retry; there is no HA, failover or second daemon. Accepted work continues on the recipients and is observed again after restart.

**Prerequisites**
1. `server.public_url`: the HTTPS origin the agents resolve, with a certificate whose SAN matches it. Agents trust the issuing CA explicitly (`--ca-file`, MCP client `--ca-file`, curl `--cacert`; system roots such as `SSL_CERT_FILE` work for Go clients that use the default pool).
2. Recipients: each agent's card must advertise an origin the daemon can reach (`https://agent-a.internal:8443/...`, never `localhost`, `127.0.0.1` or a laptop-only address: on the daemon host that is the daemon itself). Add each origin to `delivery.allowed_origins` (explicit; no wildcard; loopback origins are always allowed, so an allowlist is only exercised with non-loopback origins), trust the agents' CA with `delivery.ca_file`, and give each origin one bearer token (`registry.agents.<id>.outbound_token_file` or `delivery.token_files`). TLS skip-verify does not exist; do not put credentials in URLs, cards or logs.
3. Callers: one `registry.agents.<id>.token_file` per agent, distinct from every other credential. Example (all values are files, nothing secret appears in YAML):

```yaml
server: {listen: "0.0.0.0:8443", public_url: "https://agentiplex.internal:8443", tls_cert_file: /run/secrets/tls.crt, tls_key_file: /run/secrets/tls.key}
protocols: {mcp: {enabled: true}}
security: {operator_token_file: /run/secrets/operator}
registry:
  agents:
    pm:      {url: "https://pm.internal:8443",      token_file: /run/secrets/pm,      outbound_token_file: /run/secrets/pm-recipient}
    builder: {url: "https://builder.internal:8443", token_file: /run/secrets/builder, outbound_token_file: /run/secrets/builder-recipient}
  groups: {team: [pm, builder]}
delivery:
  allowed_origins: ["https://pm.internal:8443", "https://builder.internal:8443"]
  ca_file: /run/secrets/agent-ca.crt
```

4. Reachability checks, run **from the daemon host/network** before registering: `curl --cacert agent-ca.crt https://pm.internal:8443/.well-known/agent-card.json` and the same card URL must list a `supportedInterfaces` URL on the same origin. From each agent host: `agentiplex --server https://agentiplex.internal:8443 --ca-file ca.crt --token-file <own token> agents list`. A startup failure for an unresolvable declared agent is deliberate (start the agents first).
5. VPN, tunnel or routing is the operator's: it must provide **both directions**. A tunnel that only lets agents reach the daemon is not enough, because the daemon opens the connection to each recipient. A target behind NAT with no permitted inbound path (even through the tunnel/VPN) cannot be a recipient with today's protocol; it can still be a caller. No pull connector or reverse delivery exists.
6. Retry contract: every submission needs an `idempotencyKey` (MCP/REST) or a stable `messageId` (A2A). The CLI generates a key when omitted and prints it before submission; reuse it with `--idempotency-key` for a retry. After a dropped connection, timeout, daemon restart or SIGKILL, repeat the same key with the same target and text; an accepted task is observed again and never sent twice.

**Constraints and decision**
- Latency adds one round trip per daemon-to-agent HTTP operation; `delivery.timeout` is per HTTP operation, not per task. Pre-submission failures are retried `delivery.max_attempts` times at `delivery.retry_delay`; accepted tasks are polled indefinitely.
- Proxies and load balancers between hosts: nginx `proxy_read_timeout` defaults to 60 s between reads and AWS ALB idle timeout to 60 s (1-4000 s). Blocking MCP `ask` and blocking A2A SendMessage hold one response open without bytes; a shorter proxy limit drops that connection (the work is not cancelled). Raise the idle timeouts on the path, or prefer `send_message`/`returnImmediately` plus polling.
- Not claimed: HA, host or storage failure survival with embedded storage on a non-durable volume, tenant isolation, OAuth, mTLS.
- Decision: the current architecture is sufficient for routed or tunneled networks with bidirectional HTTPS reachability. Nothing in this validation justified a new transport.

**Client support** (verified): the `agentiplex` CLI, `examples/mcp-client` and curl set the bearer header and CA trust; the daemon's own outbound client uses the official A2A Go SDK with origin-scoped tokens. Hosts that cannot send a static `Authorization` header or trust a private CA cannot be callers; no real model gateway was exercised (simulator only), so none is claimed.

**Validated scenarios and limitations.** Run on a single machine, not on multiple hosts: one daemon, three independent HTTPS recipient processes on distinct loopback addresses (127.0.0.x), verified private CA, per-agent tokens and the YAML above (embedded broker on tmpfs because the host disk was nearly full, so host-crash durability was not tested). Passed: discovery, direct work by MCP and A2A, group and pool operations, a recipient killed before delivery and restarted (delivery queued, 5 attempts, executed once), healthy agents progressing meanwhile, a slow task surviving daemon SIGKILL and restart and collected with the original key/messageId with exactly one execution, no admission while the daemon was down, broker bound to loopback with only the daemon connected, an unapproved non-loopback origin rejected, a certificate from an untrusted CA rejected (x509 unknown authority), a wrong token 401. Not covered: real network partitions, NAT, proxies, latency, real gateways, multi-host routing.

## Static credentials and TLS

Generate distinct high-entropy URL-safe secrets (32–512 characters; e.g. openssl rand -hex 32). Mount one file per role and configure paths, not values:

```yaml
server:
  listen: "0.0.0.0:8080"
  public_url: "https://agentiplex.example:8080"
  tls_cert_file: /run/secrets/tls.crt
  tls_key_file: /run/secrets/tls.key
security:
  operator_token_file: /run/secrets/operator
  agent_token_files:          # logical agent ID -> its own credential file
    pm: /run/secrets/agent-pm
    architect: /run/secrets/agent-architect
  viewer_token_file: /run/secrets/viewer
protocols:
  mcp:
    enabled: true
```

The operator file is required in secure mode; viewer is required if the dashboard is enabled; each named agent gets its own file in `registry.agents.<id>.token_file` or `security.agent_token_files` (environment: a JSON object in `AGENTIPLEX_SECURITY_AGENT_TOKEN_FILES`). Every token (operator, viewer, each agent) must be distinct, 32-512 URL-safe characters, in a bounded file; agent IDs must be valid logical IDs (not `operator`/`viewer`). Only SHA-256 digests are held in memory; raw tokens are never stored in cards, KV, events, logs or responses. TLS and explicit HTTPS origin are mandatory when authentication is enabled; no forwarded-header trust or plaintext authenticated proxy hop. Use native TLS, a TLS-passthrough gateway or a gateway that re-encrypts/verifies the backend connection. The trusted-network flag remains for isolated unauthenticated demonstrations only; it is not a secure-mode fallback and never to be exposed publicly. The OCI image defaults to loopback inside the container.

### Static registry (declarative provisioning)

A static team can start from configuration alone. Optional maps under `registry` (config file only, no environment override):

```yaml
registry:
  namespace: engineering
  agents:
    pm:        {url: https://pm.example.org,        token_file: /run/secrets/pm}
    architect: {url: https://architect.example.org, token_file: /run/secrets/architect,
                outbound_token_file: /run/secrets/architect-recipient}
  groups: {engineering: [pm, architect]}
  pools:  {reviewers: [architect]}
```

- `url` (required): A2A base URL or Agent Card URL. Descriptions and skills come from the native card, never from YAML.
- `token_file`: inbound bearer credential bound to that agent ID through the same principal model as `security.agent_token_files`; required in secure mode. Use `security.agent_token_files` only for credential IDs that are not declared here; an ID credentialed in both places is rejected.
- `outbound_token_file`: Agentiplex-to-recipient bearer credential, merged into the `delivery.token_files` mechanism by the agent's HTTPS origin (the origin must be allowed as usual). Agents sharing an origin must use the same secret; different secrets for one origin, or a conflict with `delivery.token_files`, are rejected. Allowlist, same-origin card validation, TLS verification and no-redirect rules are unchanged.
- `groups` / `pools`: ID to member agent IDs (declared here or already registered).

Unknown fields, duplicate YAML keys, invalid IDs, missing URLs, repeated or unknown members and inconsistent credentials fail startup before any write.

**Startup order:** open storage, auth and transport; validate and apply the registry (agents sorted by ID, then groups, then pools); only then listen. `/readyz` and the "ready" log line therefore imply the whole declaration was applied; any failure exits non-zero.

**Semantics (additive, not authoritative):**
- An identical existing registration is a no-op (no new record or event); if that endpoint is temporarily offline its stored card snapshot is reused.
- A different URL for an already registered ID fails with an explicit error; work is never silently retargeted.
- A new agent whose card cannot be resolved within `delivery.timeout` fails startup ("start the endpoint first, then restart"); no placeholder record or background retry exists. Agents registered before the failure remain; restart reapplies idempotently (there is no cross-record transaction).
- Collections are written only when membership differs and affect new admissions only; queued or accepted deliveries keep their snapshots.
- Omitted agents, groups and pools stay untouched; nothing is deleted. Runtime CLI/API edits to a declared collection are replaced by the declared membership on the next restart. Dynamic registration through the CLI/API stays available.

### Principal model and access matrix

The bearer token is resolved once, at the HTTP boundary, to a principal (`operator`, `viewer`, or `agent` plus its agent ID). A2A, MCP and REST share it; `sender`/`author` fields and MCP client names are ignored or rejected. Unknown, missing or removed tokens get 401; a valid token without permission gets 403.

| Principal | Access |
|---|---|
| Operator | Everything: registry/collection administration, state deletion, abandon, all deliveries, global delivery list and event feed; start/nudge until-done requests (resolution is recipient-owned) |
| Agent `<id>` | Discovery/catalog and collection reads; send/broadcast/dispatch/ask via REST, A2A, MCP; read or wait for **only its own** deliveries, broadcast children and results (others look "not found"); read and revision-checked write of shared state; read shared notes and append notes via MCP (author = its ID); propose resolution through MCP only for a current until-done activation targeted at its own agent ID. No registry/collection mutation, state deletion, abandon, until-done admission/nudging, global delivery list or event feed |
| Viewer (browser Basic user `viewer`, or bearer) | Read-only dashboard incl. explicit delivery detail; no collaboration, raw state or mutations |
| Anonymous | Health/readiness and the public Agent Card only |

Team state and notes are intentionally shared inside the namespace. This is one trust domain, not tenant isolation: a malicious but authenticated agent can overwrite shared state.

Each delivery, broadcast child, event and note records the immutable authenticated caller ID (operator submissions record none). Idempotency keys and A2A `messageId`s are scoped by caller: the same key and text from two agents are two independent deliveries; a retry by the same agent resolves to its existing delivery.

### Provisioning, rotation, revocation

1. Create one random token file per agent (for example `openssl rand -hex 32`), mode 0400/0440, and list it under `registry.agents.<id>.token_file` (or `security.agent_token_files` for an undeclared ID). Give the agent only its own file.
2. Start (or restart) the daemon. The operator registers the agent's A2A endpoint as before; credential identity and registry registration are independent (a caller need not be a registered recipient).
3. Rotate by replacing the file content and restarting; the old token is rejected immediately and the new token reads the agent's existing records (ownership follows the agent ID). Revoke by removing the entry and restarting; the agent's records stay readable by the operator.

### Breaking changes and upgrade boundary

The shared collaborator role (`security.collaborator_token_file`, `AGENTIPLEX_SECURITY_COLLABORATOR_TOKEN_FILE`, the `collaborator` Compose secret and Helm key) is removed with no fallback; the daemon refuses to start unless an operator token is configured. Delivery IDs derived from idempotency keys/messageIds changed (caller scope), and records written before this change have no caller: named agents cannot read them, the operator still can. Drain or reconcile outstanding work first (operator: list deliveries until all are terminal, abandon the rest), then reset only disposable data. Never replay old work under a new identity.

Use the CLI without placing credentials in arguments:

```sh
agentiplex --server https://agentiplex.example:8080 --ca-file /path/ca.crt --token-file /path/agent-pm agents list
```

Environment AGENTIPLEX_TOKEN_FILE and AGENTIPLEX_CA_FILE are equivalent. Browser users open /ui/ and authenticate as viewer. Secure requests use TLS 1.2 or newer; client trust uses system roots plus optional custom CA. No insecure skip-verify option. Static tokens are read on startup; restart after rotation. Browser-origin mutations and MCP browser Origins are rejected. Public probes disclose only readiness/aliveness.

Outbound A2A uses the existing origin allowlist. For secured recipients:

```yaml
delivery:
  allowed_origins: ["https://architect.example:8443"]
  ca_file: /run/secrets/agent-ca.crt
  token_files:
    "https://architect.example:8443": /run/secrets/architect-token
```

Credential origin keys in `delivery.token_files` must be HTTPS origins (no endpoint path, credentials, query or fragment; an optional `/` is allowed). Host case, equivalent numeric ports (including the implicit HTTPS port 443), and IPv6 literal spellings are normalized; distinct hosts and ports remain distinct. All aliases for one origin, including `registry.agents.<id>.outbound_token_file`, must resolve to the same readable token. Conflicting secrets, invalid origins or unreadable token files fail startup before any recipient request. The optional `AGENTIPLEX_DELIVERY_TOKEN_FILES` JSON object replaces the file's map; duplicate keys or ambiguous configuration fields are rejected, and origin key spelling is preserved until validation. Use explicit string-to-string entries under `delivery.token_files`; YAML merge keys, aliases within that map, and non-string values are rejected. Scoped IPv6 addresses and malformed ports are rejected. DNS aliases are not merged. Rotate tokens while the daemon is stopped and restart after editing the files.

Tokens are attached only to the approved HTTPS origin (card resolution, send and task polling); redirects are not followed, and the interface must match the card's origin. URLs containing credentials are rejected. The public Agentiplex card advertises bearer auth when secured. MCP hosts may supply a static bearer header if supported; OAuth discovery, cross-tenant boundaries, mTLS client authentication and enterprise SSO are not implemented.

Embedded broker durability: `broker.sync` (default `always`) fsyncs every acknowledged JetStream write, so a host crash or power loss does not roll back accepted work; `kill -9` of the daemon is covered by `tests/integration/kill_restart_test.go`. A duration such as `2m` selects the nats-server background fsync interval instead: faster, but a host crash may lose up to that window of acknowledged writes (a process kill still loses nothing). Valid values are `always` or 1s–1h. Indicative cost on a low-end 2-core Celeron under concurrent load (`go test ./tests/integration -run xxx -bench BrokerSync -benchtime 30x`, local simulated agent): Send latency 16.8 ms with `always` vs 4.5 ms with `2m`; end-to-end delivery throughput 21 vs 68 deliveries/s. Agent traffic is low, so this is accepted; re-measure on your storage. For external or replicated NATS, `broker.sync` is ignored and durability follows the cluster configuration (server `sync_interval`, JetStream replication and storage).

External NATS:
```yaml
broker:
  mode: external
  url: "tls://nats.example:4222"
  token_file: /run/secrets/nats-token
  # credentials_file: /run/secrets/nats.creds # mutually exclusive with token_file
  ca_file: /run/secrets/nats-ca.crt
  replicas: 1
```

The native NATS client performs TLS verification and credential authentication. Credentials in broker URLs are rejected. Credentialed NATS connections require tls://; unauthenticated nats:// is only suitable for isolated trusted local testing. Provide a dedicated broker account with necessary JetStream provisioning and subject permissions, not a shared administrator credential. No secrets in Agent Cards, persistent state, source control or default logs. Credential paths and cert files are operator-controlled. Agent-produced content itself can contain sensitive data; dashboard detail and agents and viewers can read it.

## Compose

The static fixture is [examples/engineering-team/compose.yaml](../examples/engineering-team/compose.yaml) (see its [README](../examples/engineering-team/README.md)). From the repository root, with Docker Compose v2 and OpenSSL:

```sh
bash scripts/demo-secrets.sh
docker compose -f examples/engineering-team/compose.yaml up -d --build --wait
# Or: make compose-up (generates demo secrets only if absent)
```

The development script creates unique role and broker credentials plus a seven-day demo CA and separate daemon/broker certificates under ignored .demo-secrets. The directory is 0700; mounted files are read-only and readable by service UIDs. Never reuse these demo certificates in production. Trust the CA explicitly in curl/CLI or your browser (only in a disposable profile). The simulators use unauthenticated HTTP **only** within the demo bridge network; no simulator or broker ports are published. Do not attach untrusted containers.

The team (agents, per-agent credentials, group `engineering`, pool `testers`) is declared in [examples/engineering-team/compose-registry.yaml](../examples/engineering-team/compose-registry.yaml) and applied at daemon startup; no registration commands are needed. Use the operator credential to try it:

```sh
export AGENTIPLEX_SERVER=https://localhost:18443
export AGENTIPLEX_CA_FILE=.demo-secrets/ca.crt
export AGENTIPLEX_TOKEN_FILE=.demo-secrets/operator
agentiplex agents list
agentiplex broadcast engineering "Contract update" --idempotency-key release-1
```

Use the CLI built from the checkout (or docker compose exec agentiplex agentiplex with mounted credential paths). Dashboard: https://localhost:18443/ui/. Agent tasks use the separate `agent-demo` credential (agent ID `demo`). `make compose-down` (Compose down) preserves JetStream volume and existing credentials; down -v **deletes all stored data**. Stop/start Quality and restart the daemon without deleting volumes to observe durable recovery. CI's isolated scripts/compose-check.sh generates its own credentials and destroys its test project and volume; refuses to run if .demo-secrets already exists.

For native deployment, use the [example daemon configuration](../deploy/config.example.yaml) as a starting point.

## Helm

Use a supported Kubernetes cluster, Helm and the [official NATS Helm chart](https://github.com/nats-io/k8s). Create Secrets in the chosen namespace: agentiplex-auth (operator, viewer, and `agent-<id>` per entry of the chart value `agents`, which is empty by default; set e.g. `--set 'agents={demo}'`), agentiplex-tls (tls.crt, tls.key), nats-tls (broker TLS keypair) and agentiplex-broker (token or credentials, ca.crt). Provision certificates with DNS SANs matching the broker service and daemon advertised hostname. The test script illustrates creation from disposable development secrets.

```sh
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm repo update nats
helm upgrade --install nats nats/nats --version 2.15.2 -f deploy/helm/nats-values.yaml --wait
helm upgrade --install agentiplex deploy/helm/agentiplex --set 'agents={demo}' --set image.tag=v0.1.0 --wait
kubectl port-forward svc/agentiplex 18443:8080
```

Use an actually published image tag or a locally loaded image. Set publicURL to the real HTTPS origin clients use, and allowedOrigins to the approved agent endpoints. The default is suitable for the documented localhost port-forward and matching certificate only. The chart mounts Secrets and never creates them from values. It does not install NATS or ingress automatically. If a different broker is used, configure its Secret and URL. The official NATS chart provisions its own PVC. A single replica with Recreate avoids normal rolling-update overlap but does not enforce distributed ownership. No claim of HA.

## Release

The Release workflow runs only on vMAJOR.MINOR.PATCH[-prerelease] tags whose commit is an ancestor of main. It verifies Go tests and dependency consistency, packages Linux and macOS AMD64/ARM64 daemon+CLI archives and a versioned Helm package, emits SHA256SUMS, then publishes a tag-versioned multiarch OCI image to GHCR with SBOM/provenance and a GitHub release. PR Checks dry-runs archive and chart packaging and the two-architecture OCI build without pushing anything. Registry permissions and the actual tag publication are separate operator acceptance steps. No tag or release is created by this PR.

## Security limits

This is a first network deployment boundary, not production multi-tenant security. Agents read only their own deliveries, but shared state and notes are writable by every agent and there are no per-key policies. No identity provider, automatic credential rotation, HA, external policy engine or audit certification. Dashboard schema execution remains trusted operator configuration; the AMIS bundled dependency review remains open. Do not expose the old unauthenticated demo to untrusted networks. See [STATE.md](STATE.md), [MCP.md](MCP.md) and [DASHBOARD.md](DASHBOARD.md).

## MVP operational limits

One daemon per namespace only. Configure delivery.workers (default 4; range 1–16, zero uses default) or --workers. No active-active failover. Operator-only messages abandon requires matching downstream task ID and explicit --confirm-remote-stopped; never use merely because an endpoint is unreachable. It stops local monitoring, retains uncertainty and releases the reservation, not remote execution. No automatic replay. Retry a 409 if a poll is in flight. Release tags are created with make release bump=PATCH|MINOR|MAJOR from a clean current main; preview via make release-plan. The existing tag workflow builds/tests/publishes. No tag was pushed by implementing the target.

## Restart and supervision

The daemon keeps its durable record in JetStream, so a crashed or killed daemon only needs to be started again. Every packaging mode does this:

- Compose: `agentiplex` and `nats` use `restart: unless-stopped`; the daemon healthcheck runs `agentiplex status` (GET `/readyz`) against the TLS endpoint. `scripts/compose-check.sh` SIGKILLs the daemon process from the host PID namespace and verifies it restarts healthy with state intact (`docker kill` does not exercise the restart policy).
- Helm: the kubelet restarts a crashed container (default `restartPolicy: Always`); liveness uses `/healthz`, readiness and startup use `/readyz`, and the Deployment uses `Recreate` with one replica.
- systemd: [deploy/systemd/agentiplexd.service](../deploy/systemd/agentiplexd.service) sets `Restart=on-failure`; verify with `systemd-analyze verify deploy/systemd/agentiplexd.service`.

Downtime assumption: one daemon per namespace, no HA. While the daemon is down it accepts nothing and clients must retry; queued work waits in JetStream, and after restart accepted tasks are monitored again and queued deliveries resume.

<!-- retention:begin -->
## Delivery retention and bucket usage

The `DELIVERIES` bucket is capped at 128 MiB and rejects writes when full, so terminal records are removed by an age-based sweep that runs once a minute in the existing reconciliation loop.

- `delivery.retention` (default `168h`, range 5m–3650d): a terminal record (completed, failed, uncertain, input-required, auth-required) is removed once its `updatedAt` is older than this. This single window is also the retry window for idempotency keys and A2A `messageId`s: after the purge the same key starts a new operation. The default (7 days) covers unattended work over a weekend and is far below the bucket cap at typical record sizes. The minimum (5 minutes) exceeds the broker's 2-minute publish de-duplication window.
- Nonterminal records are never removed. A broadcast group (parent plus children) is removed only when its derived state is terminal (completed, failed or partial) and the newest child update is older than `retention`; children of a live parent are never removed individually. 
- Removal is a compare-and-swap purge: a concurrent update wins and the record is retried on the next sweep. Purging frees the stored bytes (verified by test); only a small marker remains until it is cleaned up.
- `GET /readyz` stays public and returns only `{"ready":true}`. The authenticated `GET /api/v1/usage` (viewer role or higher) reports `deliveries`: `bytes`, `maxBytes`, `records`.
<!-- retention:end -->

### Breaking change: required idempotency keys and single retention

- MCP and REST submissions (`ask`, `send_message`, `broadcast`, `dispatch`, `POST /api/v1/messages|broadcasts|dispatches`) now require a nonempty `idempotencyKey`; keyless calls are rejected with 400/invalid arguments. The prompt-derived `ask` identity, the `returnedAt` delivery field, the `delivery.unreturned_retention` setting (remove it from configs; unknown keys are ignored) and the `unreturnedTerminal` usage counter are removed. Clients must create a key once per intended operation and reuse it only to retry. The CLI now generates this key automatically unless `--idempotency-key` is supplied; REST/MCP requirements remain unchanged.
- Records written by earlier versions keep their IDs; `returnedAt` in stored JSON is ignored and they now follow the single `delivery.retention` window (a previous 30-day unreturned record may be purged earlier). Drain or reconcile outstanding work before upgrading if that matters; no migration is provided.
- Keys are not a permanent replay guard: after retention expiry the same key is a new operation. Uncertain outcomes are never replayed automatically and remote exactly-once execution is not claimed.

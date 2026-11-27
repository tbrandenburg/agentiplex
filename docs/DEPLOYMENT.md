# Deployment and secure connectivity

## Supported modes

- Native trusted-local: embedded JetStream, loopback, optional MCP. Existing demo works unchanged.
- Native secured: verified HTTPS and mounted static role credentials; embedded or external JetStream.
- Compose: one secured daemon, official NATS 2.15.3-alpine with TLS/token and persistent named volume, six separate simulator containers. Only the daemon HTTPS port is published, to host loopback.
- Helm: one daemon Deployment (Recreate, non-root, read-only filesystem) and ClusterIP service; external NATS installed independently with its official Helm chart, persistent PVC. No custom controller or Kubernetes-specific core code.

These are single-daemon deployments, not HA. Never run a second daemon in the same JetStream namespace. Network policies/firewalls, resource sizing, backup, broker accounts/ACLs, certificate issuance, secret rotation and operational hardening remain operator responsibilities.

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

The operator file is required in secure mode; viewer is required if the dashboard is enabled; each named agent gets its own file in `security.agent_token_files` (environment: a JSON object in `AGENTIPLEX_SECURITY_AGENT_TOKEN_FILES`). Every token (operator, viewer, each agent) must be distinct, 32-512 URL-safe characters, in a bounded file; agent IDs must be valid logical IDs (not `operator`/`viewer`). Only SHA-256 digests are held in memory; raw tokens are never stored in cards, KV, events, logs or responses. TLS and explicit HTTPS origin are mandatory when authentication is enabled; no forwarded-header trust or plaintext authenticated proxy hop. Use native TLS, a TLS-passthrough gateway or a gateway that re-encrypts/verifies the backend connection. The trusted-network flag remains for isolated unauthenticated demonstrations only; it is not a secure-mode fallback and never to be exposed publicly. The OCI image defaults to loopback inside the container.

### Principal model and access matrix

The bearer token is resolved once, at the HTTP boundary, to a principal (`operator`, `viewer`, or `agent` plus its agent ID). A2A, MCP and REST share it; `sender`/`author` fields and MCP client names are ignored or rejected. Unknown, missing or removed tokens get 401; a valid token without permission gets 403.

| Principal | Access |
|---|---|
| Operator | Everything: registry/collection administration, state deletion, abandon, all deliveries, global delivery list and event feed |
| Agent `<id>` | Discovery/catalog and collection reads; send/broadcast/dispatch/ask via REST, A2A, MCP; read **only its own** deliveries, broadcast children and results (others look "not found"); read and revision-checked write of shared state; append notes (author = its ID). No registry/collection mutation, state deletion, abandon, global delivery list or event feed |
| Viewer (browser Basic user `viewer`, or bearer) | Read-only dashboard incl. explicit delivery detail; no collaboration, raw state or mutations |
| Anonymous | Health/readiness and the public Agent Card only |

Team state and notes are intentionally shared inside the namespace. This is one trust domain, not tenant isolation: a malicious but authenticated agent can overwrite shared state.

Each delivery, broadcast child, event and note records the immutable authenticated caller ID (operator submissions record none). Idempotency keys and A2A `messageId`s are scoped by caller: the same key and text from two agents are two independent deliveries; a retry by the same agent resolves to its existing delivery.

### Provisioning, rotation, revocation

1. Create one random token file per agent (for example `openssl rand -hex 32`), mode 0400/0440, and list it under `security.agent_token_files`. Give the agent only its own file.
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

Tokens are attached only to the approved HTTPS origin (card resolution, send and task polling); redirects are not followed, and the interface must match the card's origin. URLs containing credentials are rejected. The public Agentiplex card advertises bearer auth when secured. MCP hosts may supply a static bearer header if supported; OAuth discovery, individual agent identity, fine-grained authorization, cross-tenant boundaries, mTLS client authentication and enterprise SSO are not implemented.

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

The native NATS client performs TLS verification and credential authentication. Credentials in broker URLs are rejected. Credentialed NATS connections require tls://; unauthenticated nats:// is only suitable for isolated trusted local testing. Provide a dedicated broker account with necessary JetStream provisioning and subject permissions, not a shared administrator credential. No secrets in Agent Cards, persistent state, source control or default logs. Credential paths and cert files are operator-controlled. Agent-produced content itself can contain sensitive data; dashboard detail and collaborators can read it.

## Compose

From the repository root, with Docker Compose v2 and OpenSSL:

```sh
bash scripts/demo-secrets.sh
docker compose up -d --build --wait
# Or: make compose-up (generates demo secrets only if absent)
```

The development script creates unique role and broker credentials plus a seven-day demo CA and separate daemon/broker certificates under ignored .demo-secrets. The directory is 0700; mounted files are read-only and readable by service UIDs. Never reuse these demo certificates in production. Trust the CA explicitly in curl/CLI or your browser (only in a disposable profile). The simulators use unauthenticated HTTP **only** within the demo bridge network; no simulator or broker ports are published. Do not attach untrusted containers.

Register the team using the operator credential:

```sh
export AGENTIPLEX_SERVER=https://localhost:18443
export AGENTIPLEX_CA_FILE=.demo-secrets/ca.crt
export AGENTIPLEX_TOKEN_FILE=.demo-secrets/operator
for agent in pm architect developer tester-1 tester-2 quality; do
  agentiplex register "$agent" "http://$agent:8001"
done
agentiplex groups set engineering pm architect developer tester-1 tester-2 quality
agentiplex pools set testers tester-1 tester-2
agentiplex broadcast engineering "Contract update"
```

Use the CLI built from the checkout (or docker compose exec agentiplex agentiplex with mounted credential paths). Dashboard: https://localhost:18443/ui/. Agent tasks use the separate `agent-demo` credential (agent ID `demo`). `make compose-down` (Compose down) preserves JetStream volume and existing credentials; down -v **deletes all stored data**. Stop/start Quality and restart the daemon without deleting volumes to observe durable recovery. CI's isolated scripts/compose-check.sh generates its own credentials and destroys its test project and volume; refuses to run if .demo-secrets already exists.

## Helm

Use a supported Kubernetes cluster, Helm and the [official NATS Helm chart](https://github.com/nats-io/k8s). Create Secrets in the chosen namespace: agentiplex-auth (operator, viewer, and `agent-<id>` per entry of the chart value `agents`), agentiplex-tls (tls.crt, tls.key), nats-tls (broker TLS keypair) and agentiplex-broker (token or credentials, ca.crt). Provision certificates with DNS SANs matching the broker service and daemon advertised hostname. The test script illustrates creation from disposable development secrets.

```sh
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm repo update nats
helm upgrade --install nats nats/nats --version 2.15.2 -f deploy/nats-values.yaml --wait
helm upgrade --install agentiplex charts/agentiplex --set image.tag=v0.1.0 --wait
kubectl port-forward svc/agentiplex 18443:8080
```

Use an actually published image tag or a locally loaded image. Set publicURL to the real HTTPS origin clients use, and allowedOrigins to the approved agent endpoints. The default is suitable for the documented localhost port-forward and matching certificate only. The chart mounts Secrets and never creates them from values. It does not install NATS or ingress automatically. If a different broker is used, configure its Secret and URL. The official NATS chart provisions its own PVC. A single replica with Recreate avoids normal rolling-update overlap but does not enforce distributed ownership. No claim of HA.

## Release

The Release workflow runs only on vMAJOR.MINOR.PATCH[-prerelease] tags whose commit is an ancestor of main. It verifies Go tests and dependency consistency, packages Linux and macOS AMD64/ARM64 daemon+CLI archives and a versioned Helm package, emits SHA256SUMS, then publishes a tag-versioned multiarch OCI image to GHCR with SBOM/provenance and a GitHub release. PR Checks dry-runs archive and chart packaging and the two-architecture OCI build without pushing anything. Registry permissions and the actual tag publication are separate operator acceptance steps. No tag or release is created by this PR.

## Security limits

This is a first network deployment boundary, not production multi-tenant security. The static role model does not scope a caller to specific deliveries, agents, or state keys. No identity provider, automatic credential rotation, HA, external policy engine or audit certification. Dashboard schema execution remains trusted operator configuration; the AMIS bundled dependency review remains open. Do not expose the old unauthenticated demo to untrusted networks. See [STATE.md](STATE.md), [MCP.md](MCP.md) and [DASHBOARD.md](DASHBOARD.md).

## MVP operational limits

One daemon per namespace only. Configure delivery.workers (default 4; range 1–16, zero uses default) or --workers. No active-active failover. Operator-only messages abandon requires matching downstream task ID and explicit --confirm-remote-stopped; never use merely because an endpoint is unreachable. It stops local monitoring, retains uncertainty and releases the reservation, not remote execution. No automatic replay. Retry a 409 if a poll is in flight. Release tags are created with make release bump=PATCH|MINOR|MAJOR from a clean current main; preview via make release-plan. The existing tag workflow builds/tests/publishes. No tag was pushed by implementing the target.

## Restart and supervision

The daemon keeps its durable record in JetStream, so a crashed or killed daemon only needs to be started again. Every packaging mode does this:

- Compose: `agentiplex` and `nats` use `restart: unless-stopped`; the daemon healthcheck runs `agentiplex status` (GET `/readyz`) against the TLS endpoint. `scripts/compose-check.sh` SIGKILLs the daemon process from the host PID namespace and verifies it restarts healthy with state intact (`docker kill` does not exercise the restart policy).
- Helm: the kubelet restarts a crashed container (default `restartPolicy: Always`); liveness uses `/healthz`, readiness and startup use `/readyz`, and the Deployment uses `Recreate` with one replica.
- systemd: [deploy/agentiplexd.service](../deploy/agentiplexd.service) sets `Restart=on-failure`; verify with `systemd-analyze verify deploy/agentiplexd.service`.

Downtime assumption: one daemon per namespace, no HA. While the daemon is down it accepts nothing and clients must retry; queued work waits in JetStream, and after restart accepted tasks are monitored again and queued deliveries resume.

<!-- retention:begin -->
## Delivery retention and bucket usage

The `DELIVERIES` bucket is capped at 128 MiB and rejects writes when full, so terminal records are removed by an age-based sweep that runs once a minute in the existing reconciliation loop.

- `delivery.retention` (default `168h`): a terminal record (completed, failed, uncertain, input-required, auth-required) is removed once its `updatedAt` is older than this (age is measured from the last update, not from `returnedAt`).
- `delivery.unreturned_retention` (default `720h`, must be at least `retention`): a terminal record that has no returned mark (no `ask` handed its result back) is kept this long instead, so a re-ask after a crash can still collect it. Agentiplex cannot tell never-asked records (`send_message`, `get_delivery`) from uncollected asks, so this longer limit applies to every unreturned terminal record.
- Nonterminal records are never removed. A broadcast group (parent plus children) is removed only when its derived state is terminal (completed, failed or partial) and the newest child update is older than `retention`; children of a live parent are never removed individually. Broadcasts use the normal limit.
- Removal is a compare-and-swap purge: a concurrent update wins and the record is retried on the next sweep. Purging frees the stored bytes (verified by test); only a small marker remains until it is cleaned up.
- `GET /readyz` stays public and returns only `{"ready":true}`. The authenticated `GET /api/v1/usage` (viewer role or higher) reports `deliveries`: `bytes`, `maxBytes`, `records`, and `unreturnedTerminal` (terminal records kept past `retention` by the last sweep). The sweep logs the same count.
<!-- retention:end -->

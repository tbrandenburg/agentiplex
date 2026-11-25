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
  collaborator_token_file: /run/secrets/collaborator
  viewer_token_file: /run/secrets/viewer
protocols:
  mcp:
    enabled: true
```

Both operator and collaborator files are required in secure mode; viewer is required if the dashboard is enabled. Roles must use distinct secrets. TLS and explicit HTTPS origin are mandatory when authentication is enabled; no forwarded-header trust or plaintext authenticated proxy hop. Use native TLS, a TLS-passthrough gateway or a gateway that re-encrypts/verifies the backend connection. The legacy trusted-network flag remains for isolated unauthenticated demonstrations only; never expose it publicly. The OCI image now defaults to loopback inside the container, so simply publishing its port does not expose an unauthenticated listener. Use the secure Compose/Helm deployment or explicitly configure an isolated demo listener.

| Role | Access |
|---|---|
| Operator (bearer) | All API operations including registry/collection administration and state deletion |
| Collaborator (bearer) | Discovery and collection reads, delegation, A2A/MCP, results/events, read and revision-checked write state; no registry/collection mutation or state deletion |
| Viewer (browser Basic user viewer / separate secret, or bearer) | Read-only dashboard, including explicit stored delivery detail; no collaboration, raw state reads or admin mutations |
| Anonymous | Health/readiness and Agentiplex's public Agent Card only |

Use the CLI without placing credentials in arguments:

```sh
agentiplex --server https://agentiplex.example:8080 --ca-file /path/ca.crt --token-file /path/collaborator agents list
```

Environment AGENTIPLEX_TOKEN_FILE and AGENTIPLEX_CA_FILE are equivalent. Browser users open /ui/ and authenticate as viewer. Secure requests use TLS 1.2 or newer; client trust uses system roots plus optional custom CA. No insecure skip-verify option. Static tokens are read on startup; restart after rotation. They authorize roles, not specific agents, and all collaborators share delivery/state and idempotency namespaces. Browser-origin mutations and MCP browser Origins are rejected. Public probes disclose only readiness/aliveness.

Outbound A2A uses the existing origin allowlist. For secured recipients:

```yaml
delivery:
  allowed_origins: ["https://architect.example:8443"]
  ca_file: /run/secrets/agent-ca.crt
  token_files:
    "https://architect.example:8443": /run/secrets/architect-token
```

Tokens are attached only to the approved HTTPS origin (card resolution, send and task polling); redirects are not followed, and the interface must match the card's origin. URLs containing credentials are rejected. The public Agentiplex card advertises bearer auth when secured. MCP hosts may supply a static bearer header if supported; OAuth discovery, individual agent identity, fine-grained authorization, cross-tenant boundaries, mTLS client authentication and enterprise SSO are not implemented.

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

Use the CLI built from the checkout (or docker compose exec agentiplex agentiplex with mounted credential paths). Dashboard: https://localhost:18443/ui/. The operator may use the separate collaborator credential for agent tasks. Compose down preserves JetStream volume; down -v **deletes all stored data**. Stop/start Quality and restart the daemon without deleting volumes to observe durable recovery. CI's isolated scripts/compose-check.sh generates its own credentials and destroys its test project and volume; refuses to run if .demo-secrets already exists.

## Helm

Use a supported Kubernetes cluster, Helm and the [official NATS Helm chart](https://github.com/nats-io/k8s). Create Secrets in the chosen namespace: agentiplex-auth (operator, collaborator, viewer), agentiplex-tls (tls.crt, tls.key), nats-tls (broker TLS keypair) and agentiplex-broker (token or credentials, ca.crt). Provision certificates with DNS SANs matching the broker service and daemon advertised hostname. The test script illustrates creation from disposable development secrets.

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

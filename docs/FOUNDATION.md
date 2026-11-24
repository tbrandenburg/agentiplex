# Foundation PR — implementation and verification

This draft implements the first outbound vertical slice of [PRD](PRD.md) and [ARCH](ARCH.md). It is not the complete P0 release.

## Scope

- Separate native daemon and remote CLI, one active daemon per namespace.
- Embedded or external JetStream, registry and delivery KV, work stream and event log.
- Registration of A2A 1.0 JSON-RPC same-origin endpoints, lookup by skill/tag, structurally validated AI Catalog publication.
- Direct text delivery with durable enqueue receipts, optional caller idempotency key, pre-send offline retries and downstream task tracking.
- Deterministic real A2A SDK server, provider-free tests, image packaging and CI.

Deliberately deferred: inbound A2A server, registry refresh/delete, group filters, non-text payloads, health history, dashboard, persistent application memory, broadcasting, pooling, SSE, OpenCode live acceptance, Helm, multi-replica daemon and production security.

The initial work stream uses a **single serial durable consumer** for direct messages rather than separate per-agent consumers. This establishes crash semantics first; parallel per-agent workers are a follow-up. An unavailable/long-running agent may reduce throughput. Events are a bounded best-effort audit view; delivery KV is authoritative. Idempotency keys are global to this trusted single-daemon namespace, not authenticated principal-scoped. Use caller keys for retry of an enqueue whose response was lost. Once a send might have executed, it becomes uncertain rather than automatically replaying it. Input-required/auth-required results are exposed but not resumed by the bus.

Local-only demo: authentication is not implemented. Never expose publicly. Non-loopback listener requires explicit trusted-network opt-in; non-loopback target origins require operator configuration. Registered card and supported interface must share origin. Only JSON-RPC A2A 1.0 is supported in this slice. A2A cards are parsed using the SDK and checked for required fields; this is not full protocol conformance or signature verification. Catalog validation is structural, not trust verification.

## Run (proposed; verify after CI)

Requires Go 1.26 or newer. Direct and transitive dependency versions and go.sum are locked from the CI-generated module manifest.

```sh
go run ./cmd/agentiplexd --config config.example.yaml
```

Separate shell:

```sh
go run ./examples/simulated-agent --name architect --skill architecture --port 8001
```

Separate shell:

```sh
go run ./cmd/agentiplex register architect http://127.0.0.1:8001
go run ./cmd/agentiplex discover --skill architecture
go run ./cmd/agentiplex catalog get
go run ./cmd/agentiplex send architect "Review requirement 42" --idempotency-key demo-42
go run ./cmd/agentiplex messages status <id-from-send>
go run ./cmd/agentiplex events --follow
```

Stop the simulated agent, submit another message with a different key, restart the simulated agent on the same port before the retry limit. Observe queued then completed. Restart the daemon using the same storage directory to check durable state.

External broker: start NATS with JetStream enabled and persistent storage; pass `--broker-mode external --broker-url nats://127.0.0.1:4222`. Do not run two Agentiplex daemon instances against the same namespace.

Container: build `docker build -t agentiplex:dev .`, run `docker run --rm -p 127.0.0.1:8080:8080 -v agentiplex-data:/data agentiplex:dev`. It is a trusted demo service with no auth; do not publish externally. The container cannot access a host's loopback OpenCode service through its own loopback. Prefer native local processes for that integration.

## Merge gates

- CI build, vet and race/integration tests pass.
- Verify committed go.mod/go.sum remain unchanged after go mod tidy; format Go source with gofmt.
- Confirm direct registration/discovery, independently parsed AI Catalog, A2A send/result, duplicate idempotency key, offline recovery, queued state on restart, and ambiguous failure tests.
- Exercise external NATS and Docker persistence, inspect CI logs and record evidence.
- Review conservative uncertainty behavior and limits. Do not claim production readiness.

Authoring limitation: the available local execution environment uses Go 1.23.2 and cannot reach GitHub or module proxies. No local compilation, dependency resolution, Go tests or container tests were possible. CI is configured to run the checks; the PR remains draft until actual evidence exists.

Recovery design: the durable consumer uses a 10-second acknowledgment lease, renewed every two seconds during an active attempt; graceful shutdown flushes pending broker acknowledgments. Both immediate shutdown after enqueue and shutdown after an observed offline probe have integration tests. These changes are awaiting CI verification.

# Agentiplex — Product Requirements Document

**Status:** Initial proposal / v0.1 demonstrator baseline  
**Companion:** [Architecture](ARCH.md)

## 1. Product vision

Agentiplex is the open communication fabric for autonomous agents: lightweight, open-source middleware for agent discovery, pooling, durable messaging, broadcasting and collaboration.

Independent agents should be able to join a network, advertise capabilities, find peers and exchange work even when participants are temporarily unavailable. Agentiplex supplies shared infrastructure rather than prescribing how agents reason, execute tools or organize workflows.

**Product principle:** bring your own agents; build your own organization.

## 2. Problem and audience

Existing A2A agents can communicate directly once endpoints are known. An engineering team still needs a shared registry, durable delivery, group communication, worker pools, visibility and a way to operate the network independently of any IDE or model provider.

Primary users are developers and platform engineers assembling multi-agent systems from existing agents. Secondary users are operators who need to inspect delivery, diagnose failures and deploy the same service locally or in a container environment.

The initial motivating scenario is a small engineering organization: architect, developer, testers and quality reviewer. Agentiplex does not implement their roles, contracts or governance. Those remain applications built on the communication fabric.

## 3. Product principles and boundaries

- Standards-first: use A2A 1.0 at agent-facing boundaries; publish AI Catalog entries referencing native Agent Cards. Do not invent a replacement wire protocol.
- Glue code first: reuse official SDKs and NATS JetStream for messaging, persistence and consumer coordination.
- Runtime-independent: independently hosted agents remain responsible for their execution and context. No requirement for VS Code, Copilot, OpenCode or a particular model.
- Library-first Go design, with a headless daemon (`agentiplexd`) and separate remote CLI (`agentiplex`).
- Deployment-agnostic: native, Docker/Compose and Kubernetes via configuration, not platform-specific core code.
- Honest reliability: distinguish durable enqueue, delivery, acceptance, execution and completion. At-least-once transport is not exactly-once agent execution.
- Collaboration rather than orchestration: no prescribed workflow graph or central LLM decides who works next.

A2A is the initial protocol. AHP (host/session control), ACP (runtime integration) and MCP (tools/resources) are future, complementary adapters, not equivalent message transports.

## 4. User journeys

1. **Join:** an operator registers an existing A2A endpoint under a stable logical identity; Agentiplex resolves its Agent Card, stores metadata and publishes it in the catalog.
2. **Discover:** a client locates registered agents by ID, advertised skill, tag or configured group.
3. **Communicate:** a CLI or A2A client submits a message to a registered recipient; Agentiplex durably queues it and invokes the recipient's native A2A endpoint.
4. **Collaborate:** a sender broadcasts to all members of a team or dispatches work to one eligible member of a worker pool.
5. **Recover:** a recipient can stop, miss a delivery, restart and receive retained work. Operators see the true delivery outcome and can inspect failed or uncertain work.
6. **Operate:** a user can start the daemon, register agents, inspect catalog and delivery events, and restart the infrastructure without losing committed state.

## 5. Requirements and priorities

P0 is the first usable demo and release gate. P1 follows if the foundation works; it is part of the product direction, not a claim of completion. P2 is explicitly deferred.

| ID | Priority | Requirement / observable acceptance |
|---|---|---|
| REG-01 | P0 | Register a reachable A2A URL, resolve and validate its native Agent Card with the SDK, assign a unique stable local ID; reject invalid or conflicting registrations without replacing valid state. |
| REG-02 | P0 | List, inspect, refresh and remove registrations. Store the endpoint, card snapshot, registration metadata and observed reachability independently; offline agents remain registered. |
| CAT-01 | P0 | Publish a valid AI Catalog at `/.well-known/ai-catalog.json` using the SDK's types/validation, with resolvable card references and standard media types. Validate with an independent consumer. |
| DISC-01 | P0 | Discover by ID, skill ID/name and tag; filter by configured group. Show advertised capabilities rather than inferring them from prompts. |
| MSG-01 | P0 | Send a text or supported A2A message to a logical agent ID. Return a stable delivery ID only after a JetStream publish acknowledgment; support caller-provided idempotency keys. |
| MSG-02 | P0 | Deliver through an official A2A client and retain the returned message/result or task reference. Expose queued, attempting, accepted, completed, failed and uncertain states; do not equate an HTTP success with completion. |
| MSG-03 | P0 | Persist queued messages across recipient and daemon restart. Retry safe transient failures with bounded backoff; surface exhausted or ambiguous deliveries explicitly. |
| A2A-01 | P0 | Expose Agentiplex's own Agent Card and a native A2A endpoint accepting a documented structured direct-send request. An independent A2A client can submit a task without the Agentiplex CLI. |
| A2A-02 | P0 | Support task status lookup for the submitted A2A operation; distinguish completion of the bus operation from the downstream agent outcome. Unsupported streaming, cancel or push capabilities must not be advertised. |
| OPS-01 | P0 | Start/stop `agentiplexd`, use remote `agentiplex` commands for registry, discovery, send, status and events. Configuration supports embedded and external NATS. |
| OPS-02 | P0 | Provide health/readiness endpoints, structured logs and a durable event feed with delivery correlation IDs. No secrets or raw prompts in default logs. |
| DEMO-01 | P0 | Run at least two independent deterministic simulated A2A services using the official SDK; demonstrate direct send, catalog discovery, offline recovery and daemon restart without external model credentials. |
| GROUP-01 | P1 | Broadcast to a configured group. Snapshot membership at submission, generate one child delivery per member and report partial failure without falsely claiming atomic fan-out. |
| POOL-01 | P1 | Dispatch one item to one eligible member of a configured pool via shared durable consumption, not broadcast. Assign distinct work to available members; preserve assignment after downstream acceptance. |
| STATE-01 | P1 | Offer bounded, namespaced persistent key/value context storage with revision checks. This is application-managed state, not automatic LLM conversational memory or runtime checkpointing. |
| UI-01 | P1 | Provide a minimal live dashboard driven by actual registry, delivery and event state. Clearly label simulated agents and observed versus unknown reachability. |
| DEMO-02 | P1 | Register and invoke a real `tbrandenburg/opencode-a2a-lite` endpoint unchanged; show actual response and distinguish it from simulated output. |
| DEP-01 | P1 | Package a non-root OCI image and Docker Compose example using persistent external NATS; provide a basic single-replica Helm deployment when tested. |
| SEC-01 | P2 | Authenticated identities, per-agent authorization, TLS, secret management and tenant boundaries for network-accessible deployments. |
| HA-01 | P2 | Multi-replica daemon coordination, replicated JetStream, safe ownership/reconciliation, failover tests and operational SLOs. |
| EXT-01 | P2 | AHP, ACP and MCP adapters; catalog import/federation and additional discovery/transport integrations where justified. |

P0 includes loopback binding and restricted outbound endpoints (see architecture); SEC-01 is not permission to expose an unauthenticated service.

## 6. Demonstrator

Use a repeatable provider-free demo as the baseline, optionally substituting a real OpenCode Architect after a live smoke test.

| Scene | Action | Evidence |
|---|---|---|
| 1 | Start daemon and independent mock agents; register endpoints | CLI lists agents; catalog validates and contains resolvable card URLs |
| 2 | Discover testers and architecture skills | Query returns matching advertised skills |
| 3 | Submit direct task using CLI and independent A2A client | Stable delivery IDs; actual downstream responses and terminal states |
| 4 (P1) | Broadcast a contract update to a group | One child delivery per group member; aggregate status |
| 5 (P1) | Dispatch several tasks to a tester pool | One assignment per task; observed distribution across available workers |
| 6 | Stop one recipient; submit a message; restart it | Message remains queued/retryable while offline, then actually completes |
| 7 | Stop daemon with queued work; restart using same data directory | Registry and pending work survive; delivery continues |
| 8 (P1) | Send a read-only repository analysis task to OpenCode | Real A2A response and task status; no mocked success |

Suggested agents: Architect (OpenCode or mock), Developer (mock), Tester-1/Tester-2 (mock), Quality (mock). Use a disposable trusted workspace and deterministic mock responses. A failed real-model demo must not invalidate the provider-free transport tests or be presented as a success.

## 7. Quality gates and non-functional requirements

- Run unit and integration tests in CI; use real JetStream and independent A2A servers for integration, not mocked broker acknowledgments.
- Test invalid cards, duplicate registrations, enqueue failures, bounded retries, offline recipient recovery, daemon restart, ambiguous HTTP outcomes, and task failure. Test broadcast/pool cardinality when those features land.
- Record test command, exit code and commit SHA. Document exact demo setup and pinned versions. No performance, security, HA or conformance claims without corresponding evidence.
- Use explicit timeouts, finite queue/storage limits, bounded payloads, clean shutdown and observable error states.
- Keep default operation local and trusted; no public exposure or untrusted arbitrary endpoints in the demo.
- A successful demo is observable interoperability and durable delivery under the tested failure cases, not model quality or safety certification.

## 8. Non-goals

No agent framework, workflow engine, proprietary agent messaging protocol, vector database, automatic session replay, central reasoning agent, custom message broker, mandatory Kubernetes, exactly-once external side effects or production multi-tenant security in the demonstrator.

## 9. Open decisions

- Exact release/dependency versions and tested compatibility matrix.
- Public API stability and standardized discovery registry/ARD integration beyond catalog publication.
- Authentication and trust model for non-local deployments.
- Policies for ambiguous delivery, operator replay, retention and durable inbound A2A task storage.
- Whether to offer external adapters as public Go extension packages after the first implementation.

See [ARCH.md](ARCH.md) for the proposed implementation. No requirements in this document are claimed implemented yet.

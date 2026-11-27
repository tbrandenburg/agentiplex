# Collaboration primitives

## Groups versus pools

A group is an explicitly configured collection of registered logical agent IDs. Broadcast submits one child delivery per member. A pool is an explicitly configured collection of interchangeable workers. Dispatch submits one task to one eligible member, not one copy per member.

Definitions persist in JetStream KV. Each operation snapshots membership on admission; updates affect only subsequent submissions. IDs are unique within the collection kind. Up to 32 groups, 32 pools and 32 distinct registered members per collection in this demo. No deletion or dynamic skill-based membership yet.

## CLI

Register two independently running simulator endpoints as tester-1 and tester-2. Then:

```sh
agentiplex groups set engineering tester-1 tester-2
agentiplex pools set testers tester-1 tester-2
agentiplex groups list
agentiplex pools get testers
agentiplex discover --group engineering
agentiplex broadcast engineering "Contract updated" --idempotency-key release-1
agentiplex dispatch testers "Verify requirement 42" --idempotency-key verify-42
agentiplex messages status DELIVERY_ID
agentiplex events --follow
agentiplex events --follow --after 12 --kind delivery.state
```

Groups/pools set is an upsert. Members must already be registered. The idempotency key is required; use a fresh key for each new operation; repeating the same key and payload returns the same receipt while the record is retained (7 days after it finishes by default). A conflicting payload is rejected. Keys are scoped to the caller and operation.

## HTTP / A2A

- PUT /api/v1/groups/{id} and /api/v1/pools/{id}: body {"members":["tester-1","tester-2"]}.
- GET /api/v1/groups or /pools, and /{id}: inspect definitions.
- POST /api/v1/broadcasts and /api/v1/dispatches: body {"target":"engineering","text":"Contract updated","idempotencyKey":"release-1"} (use pool ID for dispatch).
- GET /api/v1/deliveries/{id}: parent or individual delivery. A broadcast parent includes child IDs, targets, states and details; fetch child IDs for full downstream results.
- GET /api/v1/agents?group=engineering: combine group membership with existing skill/tag filters.
- A2A: SendMessage structured data part with operation broadcast or dispatch, target collection ID and text. GetTask exposes aggregate broadcast status or the dispatch assignment and original result. Use a fresh messageId, reuse it only for identical retries.

## Durable fan-out and pool assignment

A broadcast stores a recipient manifest before creating child deliveries. Child IDs are deterministic. A receipt acknowledges the durable manifest, not successful publication to every recipient or completion; partially admitted children are retried by reconciliation. Fan-out is not atomic. The parent reports queued/accepted while recipients are pending, completed only if all succeed, partial if some succeed and others fail or need intervention, failed if none succeed. Child outcomes, including uncertain, remain visible.

Each dispatch uses one durable message on the pool subject. Each pool has one durable JetStream pull consumer. A single execution loop services the direct and pool consumers, choosing members round-robin and skipping unreachable or busy endpoints. Active accepted/attempting records reserve endpoint capacity across queues and restarts. Round-robin cursor resets at restart; membership and assigned endpoint persist. The execution loop makes HTTP calls serially, but each consumer permits up to 64 outstanding broker messages so delayed or accepted tasks do not prevent other eligible endpoints from receiving work. Multiple asynchronous downstream tasks can be active, with at most one active task per endpoint as tracked by Agentiplex. This is not independent competing workers or a multi-replica dispatcher. A slow synchronous HTTP call or reachability probe can still reduce throughput. Assignment is persisted before attempting the external call. After acceptance or an ambiguous request, no reassignment or automatic replay. Pool membership is explicit; no claim of scheduling by live capacity beyond the persisted active-task check.

The existing direct worker and delivery guarantees remain; at-least-once broker delivery is not exactly-once external execution. Pending work is bounded by broker/KV capacity. Production throughput, persistent fairness, cross-replica coordination and security are deferred.

## Event subscriptions

GET /api/v1/events/stream?after=12&kind=delivery.state uses Server-Sent Events (SSE). The last sequence can also be sent as Last-Event-ID. Events carry a sequence ID and JSON data. Filter by exact kind or omit the filter. Replay the retained JetStream history, then follow live events via Core NATS wakeups and durable reads. A bounded connection lifetime permits reconnect; the CLI resumes a cleanly closed stream at the last printed sequence. On a transport error, rerun with the last sequence. The event stream is bounded, best effort, and may have retention gaps; inspect delivery records for authoritative state. No per-agent durable subscription cursor or event delivery acknowledgment is introduced. This is an admin observation API, not a native A2A streaming implementation.

## Verification

Checks exercises independent A2A targets, snapshot fan-out and restart, partial outcomes, interrupted manifest recovery, one-recipient dispatch, round-robin distribution, offline fallback, pool restart, ambiguous no-reassignment, native A2A/CLI collaboration and SSE replay/live/resume. No new dependencies. Trusted-local only; no authenticated caller identity or authorization.

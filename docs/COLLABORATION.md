# Collaboration primitives

## Groups versus pools

A group is an explicitly configured collection of registered logical agent IDs. Broadcast submits one child delivery per member. A pool is an explicitly configured collection of interchangeable workers. Dispatch submits one task to one eligible member, not one copy per member.

Definitions persist in JetStream KV. Each operation snapshots membership on admission; updates affect only subsequent submissions. IDs are unique within the collection kind. Up to 32 groups, 32 pools and 32 distinct registered members per collection in this demo. No deletion or dynamic skill-based membership yet.

Groups and pools can also be declared statically under `registry.groups`/`registry.pools`; the declared membership is applied at startup when it differs and replaces runtime edits on restart (see deployment guide).

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
agentiplex messages wait DELIVERY_ID
agentiplex send architect "Review this proposal" --idempotency-key proposal-1 --wait
agentiplex events --follow
agentiplex events --follow --after 12 --kind delivery.state
```

Groups/pools set is an upsert. Members must already be registered. The idempotency key is required; use a fresh key for each new operation; repeating the same key and payload returns the same receipt while the record is retained (7 days after it finishes by default). A conflicting payload is rejected. Keys are scoped to the caller and operation.

## Waiting for results

`messages wait ID` waits for a terminal delivery without submitting work. It prints the state, ID and downstream text (or structured result); `--json` returns the full record. For broadcast parents it waits for all admitted children and reports their states and IDs. `send --wait` submits once under the same required idempotency key and prints the receipt ID to stderr before blocking. Normal `send` and `messages status` keep their JSON output. Any terminal state other than completed exits nonzero; completed is transport-level success, not an engineering verdict.

The wait is event-driven on the durable delivery KV record, using the same watcher as MCP `ask`; broadcast admission and each child use their own existing record. No server lifetime cap or per-waiter polling of agents. Ctrl-C, a lost connection or a daemon restart ends the observation, not the work. Run `messages wait ID` again against the restarted daemon on the same storage. A failed submission response may be retried only with the same key and payload while retained; never blindly resubmit uncertain work. Normal authentication and per-caller visibility apply. Raw result text is untrusted.

## Durable follow-through (until-done)

A single delivery is still the default. Use the same `send` command for a
collaborative objective that may need multiple independent agent turns:

```sh
agentiplex send lead "Resolve all findings" --idempotency-key objective-1 --until-done --wait
agentiplex send lead --file objective.md --idempotency-key objective-2 --until-done --max-turns 20 --timeout 2h --nudge-after 5m --wait
```

These are alternative submissions, not concurrent commands for the same daemon.
`--file FILE` (or `--file -` for stdin) is also supported for ordinary
send and state put. Files contain UTF-8 text, not middleware-interpreted
contracts. Ordinary send accepts up to 64 KiB; until-done up to 60 KiB to
leave room for activation context. State values keep their existing 16 KiB limit.

- `--until-done`: persist a logical request with an original ID and
  coordinator. The daemon automatically admits another safe activation after
  a successful turn without explicit resolution; no inactivity interval is
  required. A short 250ms admission floor prevents a tight loop.
- `--wait`: attach an observer to the selected lifecycle. Without it, the
  receipt returns immediately and continuation still happens. Ctrl-C does not
  cancel execution.
- `--max-turns`: default 20, range 1–100, includes the initial activation and
  every continuation or reminder; not subordinate calls or status polls.
- `--timeout`: default 2h, range 1s–24h in whole seconds; durable continuation
  deadline, not the HTTP/wait timeout. On exhaustion, no further activation
  intents are admitted; already admitted work may still run or finish.
- `--nudge-after`: optional watchdog interval in whole seconds (0/off by
  default; maximum 24h). A safe continuation overdue by this interval is
  labelled an inactivity watchdog reminder. It shares the same eligibility
  and budget as normal continuation; it cannot force an extra turn, interrupt
  active work, or bypass uncertainty. Normal progress does not wait for it.

```sh
agentiplex messages list
agentiplex messages status MESSAGE_ID
agentiplex messages nudge MESSAGE_ID
agentiplex messages wait MESSAGE_ID
```

Manual nudge rechecks eligibility immediately, rather than retrying the original
work. Status includes `follow` (options, deadline, activation IDs and proposed
resolution) and child delivery summaries. Fetch child IDs to inspect individual
turns. The original request stays `continuing` until the coordinator explicitly
proposes resolution through MCP and its activation and known delegated work
complete. `completed` means the coordinator declared success and the supported
delivery scope is quiescent; it is not independent proof of objective acceptance.
Blocked objectives, budget exhaustion and unsafe work end in an intervention
state with details. Failed or uncertain execution is not automatically replayed.

**Scope:** initial support is for operator submissions to one initially idle,
dedicated daemon, with at most one open until-done request. Existing delegated
calls do not propagate a request ID. Consequently every delivery admitted during
this request is conservatively considered relevant. Other direct worker calls
use ordinary messaging; competing direct sends to the coordinator (including
aliases) and new broadcast/pool dispatch submissions are rejected while the
request is open. Do not mix unrelated work. Existing uncertain records prevent
new until-done admission. No claim of multi-request correlation or of knowing
work executed outside Agentiplex. Trusted-local mode cannot attest the sender;
authenticated resolution requires the coordinator's own agent credential.

The parent and deterministic activation intents are persisted in the existing
DELIVERIES KV. Delivery state transitions wake one daemon reconciler; a bounded
two-second reconciliation tick repairs missed wakes; earlier durable deadlines,
continuation floors and optional watchdog timestamps shorten the next wake. No new stream, scheduler, task store or application workflow
is needed. Native JetStream message scheduling was considered, but it would add
stream configuration and duplicate admission paths without improving the
immediate event-driven continuation in this dedicated scope. The persisted
timestamps and intents survive restart. Broker publish ACKs and existing
delivery attempt/uncertainty rules still apply to every activation.
Repeat only the identical target/text/lifecycle options under the same retained
key to reattach. A new key is new work. Retention protects known evidence while
a request is open and retains the parent while admitted activations remain active.

REST: `POST /api/v1/messages` accepts an optional
`"follow":{"maxTurns":20,"timeoutSeconds":7200,"nudgeSeconds":0}` object.
Omit it for an ordinary send. `POST /api/v1/deliveries/{id}/nudge` is
operator-only in secure mode. Existing status/wait routes observe the parent.
Native A2A ingress and MCP ask/send retain their original semantics.

Examples: [three read-only agents](../examples/discussion-team/README.md#continue-until-the-objective-is-done)
and [repository repair](../examples/repository-team/README.md#contract-driven-mode-opt-in).

## HTTP / A2A

- PUT /api/v1/groups/{id} and /api/v1/pools/{id}: body {"members":["tester-1","tester-2"]}.
- GET /api/v1/groups or /pools, and /{id}: inspect definitions.
- POST /api/v1/broadcasts and /api/v1/dispatches: body {"target":"engineering","text":"Contract updated","idempotencyKey":"release-1"} (use pool ID for dispatch).
- GET /api/v1/deliveries/{id}: parent or individual delivery. A broadcast parent includes child IDs, targets, states and details; fetch child IDs for full downstream results.
- GET /api/v1/deliveries/{id}/wait: blocking, read-only terminal delivery (same record and authorization as status); reconnect by the original ID.
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

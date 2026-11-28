# Agent lifecycle and persistent application state

## Registration lifecycle

```sh
agentiplex agents refresh architect
agentiplex agents health architect
agentiplex agents remove architect
```

Refresh resolves the original card with the official A2A SDK, updates the stored snapshot only if the same registered endpoint is advertised, and uses the KV revision to prevent stale writes. Failure preserves the prior card and records an unsuccessful observation. A changed endpoint requires removal and new registration, not implicit redirection. Offline agents remain registered and catalogued.

Registration records the first successful observation. An optional sequential background observer runs at registry.health_interval (default 30s; zero disables; actual full sweep may take longer). Health explicitly probes; cached GET health returns the last observation. Reachable means the configured card was resolved, validated and still advertises the registered interface, not that inference or task execution will succeed. Unreachable means that check failed, including incompatible card/interface, not proof the host is down. Legacy registrations without observations show unknown. Timestamp shows observation age; old observations are not live status. Busy is independent: Agentiplex tracks an attempting/accepted delivery for the endpoint; external work is unknown.

Removal is rejected while the agent is a member of any configured collection or referenced by pending direct, broadcast or dispatch work (including unassigned pool snapshots). Edit collections and let work finish first. Collection deletion and empty memberships are not supported yet; a sole member requires a replacement before removal. Terminal and intervention/uncertain historical records remain inspectable, and are not replayed. State is independent of registration and is never deleted implicitly. Removal excludes the agent from future discovery/catalog and new direct submissions. Existing idempotent receipts remain queryable. Re-registering a logical ID is an explicit operator decision.

## Persistent state

```sh
agentiplex state put architect context "Review pending"
agentiplex state get architect context
agentiplex state put architect context "Review finished" --revision REVISION_FROM_GET
agentiplex state put engineering requirement REQ-42
agentiplex state list engineering
agentiplex state watch engineering
agentiplex state delete engineering requirement --revision REVISION_FROM_GET
```

Values are UTF-8 strings (JSON can be stored as text), max 16 KiB. Namespace and key each use the existing lowercase logical ID grammar (1–64 characters, letters/digits/underscore/hyphen, starts alphanumeric). Namespace is an application label; it need not match a registered agent or group. 256 live keys per namespace, 32 MiB aggregate broker bucket limit, history one, no TTL. JetStream rejects writes at capacity instead of evicting another key; delete markers count toward storage. Operators must manage retention/capacity. Native Create uses revision zero, Update and Delete require exact current nonzero revision. Stale writes return conflict (HTTP 409); missing values return 404. A revision is the native bucket sequence, not a per-key counter. Read and retry with a newly observed revision only after merging the intended change; do not blindly overwrite.

List shows metadata only; get and watch explicitly return values. Watch uses native KV snapshot and updates: put/delete operations carry the revision, and ready marks snapshot completion. A watch is not a durable historical event subscription; on disconnect or five-minute HTTP stream expiry reconnect for a fresh snapshot, not exactly-once change replay. Existing administrative event feed includes state.updated/state.deleted metadata without values.

## Shared notes

```sh
agentiplex notes list discussion
agentiplex notes list discussion expert
agentiplex notes list discussion --limit 10 --json
```

Read the latest 100 notes across a namespace or filter to an exact topic. `--limit` accepts 1–100; results are in time order, not grouped by topic. The default renders topic, author, timestamp and text; `--json` prints the array. Authors are authenticated IDs in secure mode; trusted-local notes have no attested author. The same shared-state trust boundary applies. Notes are appended using the existing MCP `note` tool or Go API, not a second store.

## API

- POST /api/v1/agents/{id}/refresh; DELETE /api/v1/agents/{id}
- GET /api/v1/agents/{id}/health (cached); POST same path (probe)
- GET /api/v1/state/{namespace} (metadata); GET /api/v1/state/{namespace}/{key}
- PUT /api/v1/state/{namespace}/{key} with {"value":"text","revision":0} to create, or current revision to update
- DELETE /api/v1/state/{namespace}/{key}?revision=N
- GET /api/v1/notes/{namespace}?topic=expert&limit=10 (omit topic for all topics; default limit 100)
- GET /api/v1/state-watch/{namespace} (SSE; snapshot + updates)

The Go API uses the same semantics. The AMIS dashboard adds reachability timestamp and busy columns, without custom visual components. State values are not included in dashboard snapshots or default logs. No MCP tools, automatic model memory, secrets management, authenticated namespace isolation, workflow engine or distributed ownership in this slice. Trusted-local only; do not store secrets in unauthenticated state.

## Verification

Checks covers registration refresh and failed observation, removal guards, catalog/discovery, concurrent state CAS, limits, namespace isolation, native watch, restart, CLI/HTTP, and inherited demo/browser/container regressions.

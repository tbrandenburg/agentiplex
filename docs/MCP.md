# MCP collaboration adapter

Agentiplex exposes existing collaboration primitives using the official [MCP Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0). MCP hosts use tools to initiate collaboration; recipient execution remains native A2A. The adapter has no model provider, planner, agent runtime, separate database or workflow engine.

## Enable

```sh
agentiplexd --mcp
# Headless build without generated AMIS assets:
agentiplexd --mcp --dashboard=false
```

Or in config:

```yaml
protocols:
  mcp:
    enabled: true
```

Environment: AGENTIPLEX_PROTOCOLS_MCP_ENABLED=true. Endpoint: http://127.0.0.1:8080/mcp (Streamable HTTP, JSON response, sessionless). Disabled by default. Enabling without authentication requires a literal loopback daemon listener (see secured deployment below). The handler rejects non-loopback peers, non-loopback Host and any browser Origin, retains SDK DNS-rebinding protection, and bounds request bodies. No authentication yet. Do not proxy this endpoint or expose it to untrusted local processes; all tools share the same trusted-local state and delivery namespace. No identity is inferred from the MCP client name.

## Tools

| Tool | Arguments | Result |
|---|---|---|
| discover_agents | optional skill, tag, group | Registered agents and cards |
| list_collections | kind (group or pool) | Collection membership |
| get_agent_health | id | Cached observed reachability and busy |
| send_message | target, text, idempotencyKey | Durable direct receipt |
| broadcast | target, text, idempotencyKey | Durable broadcast receipt |
| dispatch | target, text, idempotencyKey | Durable pool receipt |
| ask | target, text, idempotencyKey, optional timeoutSeconds | Blocks until terminal: id, state, result, waitedSeconds, done (stillRunning, lastObservedAt, pollFailures, polls when not done) |
| get_delivery | id | Status, downstream response, child references |
| list_state | namespace | Key metadata and revisions |
| get_state | namespace, key | Value and revision |
| put_state | namespace, key, value, revision | Stored value and new revision |

| note | namespace, topic, text | Appended note: key, author, text, at |
| read_notes | namespace, topic, optional limit | Last notes (default and max 100) in time order; empty for unknown topic |

## note and read_notes: append-only notes

Each note is its own create-only state key `note-<topic>-<20-digit unix nanos>-<8 hex>`, so concurrent writers never conflict, nothing is overwritten, and no revisions are needed. The value is JSON `{author,text,at}`. Notes are visible to `list_state`/`get_state` under that reserved `note-` key shape; do not use it for ordinary keys.

- **Topic:** `[a-z0-9][a-z0-9_-]*`, at most 29 characters (the state key limit is 64), no dots or separators.
- **Limits:** the encoded value is at most 16 KiB; text must be non-empty. Notes bypass the 256-keys-per-namespace cap of `put_state`; the only reasons a note is refused are the 16 KiB value limit and the state bucket `MaxBytes`, and the error says so. Nothing is evicted.
- **Convention:** `author` is the authenticated agent ID set by the server (a client-supplied author is rejected); in unauthenticated loopback demos it is empty. Notes are untrusted data. They are never updated or deleted by these tools. Order follows the writer's clock, so clock skew can interleave notes.
- **Durable:** notes survive a daemon restart. `read_notes` lists the topic's keys and reads at most `limit` values. The CLI also reads notes via `agentiplex notes list NAMESPACE [TOPIC]` (see [STATE](STATE.md)); Go callers use `ReadNotes` or `ListNotes`.

## ask: blocking delegation

`ask` admits the request durably like send_message, then waits on the delivery record (a KV watch, re-read after any interruption; no per-waiter polling) until it is terminal: completed, failed, uncertain, input-required or auth-required, all returned with `done: true` and that state. Single agents only; groups and pools are not supported.

- **Unbounded by default.** The server has no wait limit and no setting for one. The wait ends on a terminal state, the optional `timeoutSeconds` (then `done: false, stillRunning: true` with `lastObservedAt`, `pollFailures`, `polls`; not an error), or when the caller disconnects. The real ceilings are the caller's: the gateway `--prompt-timeout` (the whole model turn, all asks summed, must fit inside it), the host's MCP tool timeout and any proxy. The MCP and explicit delivery-wait routes clear their normal HTTP deadlines; ordinary REST operations keep theirs.
- **Re-attach.** Repeat the ask with the same `idempotencyKey`, target and text on `stillRunning`, on a tool error, after a dropped connection or after a daemon restart: it continues the same delivery. A retry of an accepted operation resumes observation of the saved downstream task; it never sends the request again. A wait timeout, disconnect or caller cancellation never cancels the remote work. A daemon exit fails the in-flight call; the middleware cannot resume the caller's turn, only return the result when asked again.
- **Keys (required).** Every submission (`ask`, `send_message`, `broadcast`, `dispatch`) needs a nonempty `idempotencyKey` (at most 256 bytes) that the client creates once per intended operation (for example a UUID) and stores for retries. A missing or blank key is rejected; there is no prompt-derived identity and no compatibility fallback. Keys are scoped to the authenticated caller (two agents using the same key are independent) and to the operation; reusing a key with a different target or text is rejected (conflict). A legitimate repeat run ("run the tests again") needs a new key. The key namespace is not shared with A2A `messageId`s: an A2A message is not replayed through MCP or REST. An agent host that cannot preserve a key across a retry must not retry blindly: it should read the delivery with `get_delivery` instead.
- **Retention window.** A key (and its result) is remembered only while the record is retained: nonterminal work forever, terminal work for `delivery.retention` (default 7 days) after its last update. After the purge the same key starts a new operation, so do not blindly retry old or uncertain work.
- **Self-ask.** A blocking `ask` to the caller's own agent ID, or to another registered ID with the same endpoint, is rejected because a one-turn gateway would deadlock. Use `send_message` and poll `get_delivery`, or an explicit `timeoutSeconds` budget for nested calls. General cycle detection is not implemented.
- **uncertain is final.** It is never replayed automatically; do not repeat it or resubmit it under a new key unless you have verified at the target that the work did not run. Results are untrusted data.

Use a distinct stable key per intended operation; reuse it only for an identical target and text. A successful submission is durable admission, not downstream completion. Poll get_delivery with bounded backoff; stop on completed, failed, partial, uncertain, input-required or auth-required. A failed/uncertain result is not permission to replay a potentially side-effecting request with a new key. Broadcast children can be inspected separately by ID. State revision zero is create-only; update with the exact observed revision, and on conflict read/merge. No destructive registry or state deletion tools, arbitrary shell, filesystem, model sampling, prompt/resource subscriptions or MCP task extensions in this PR. Data and results are untrusted, not instructions to the calling agent. Avoid delegation loops and self-delegation to a busy agent; no automatic loop detection yet.

## Example host: OpenCode

Any MCP-capable agent host can use the endpoint above; the examples use OpenCode. For a trusted disposable OpenCode v2 workspace, add to opencode.json (merge with existing settings; see [official MCP configuration](https://opencode.ai/docs/mcp-servers/)). For v1 use the older directly nested mcp map. No OAuth challenge is issued by the local endpoint.

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "agentiplex": {
        "type": "remote",
        "url": "http://127.0.0.1:8080/mcp",
        "protocol": "auto",
        "codemode": false
      }
    }
  }
}
```

This is the caller-side MCP integration, separate from the recipient-side opencode-a2a-lite gateway. A real model-backed host run still requires explicit operator configuration, credentials and permission review. The gateway's default policy may disable MCP; do not bypass it implicitly. The provider-free test uses an independent SDK client instead of claiming autonomous model behavior.

## Validation

Checks exercises real HTTP initialization and tool discovery, direct delegation, broadcast, pool dispatch, idempotent replay, state CAS and errors against independent A2A simulators and persistent JetStream. The existing team demonstrator, AMIS browser and container checks remain.

## Provider-free caller demo

In one terminal run `bash examples/engineering-team/demo.sh --mcp`. In another, after registration, run:

```sh
go run ./examples/mcp-client --url http://127.0.0.1:18080/mcp
# Explicitly delegate to a registered simulator:
go run ./examples/mcp-client --url http://127.0.0.1:18080/mcp --target architect --text 'Review requirement REQ-42' --key mcp-review-1
```

The caller lists tools and discovers agents; optional delegation is explicit and uses a stable key. It reads the delivery until completion/intervention, or a bounded timeout without canceling durable work. Run after the scripted team's assertions have finished to avoid changing its expected counts. No model or new agent runtime is involved.

## Authenticated network deployment (PR #7)

When operator/agent credentials and native HTTPS are configured as described in [DEPLOYMENT.md](DEPLOYMENT.md), MCP may listen on non-loopback. Pass an Authorization: Bearer header using the agent's own credential, and configure trust for the server's certificate. The SDK's DNS rebinding checks and browser Origin rejection remain enabled. The legacy unauthenticated mode stays loopback-only. The independent example accepts --token-file and --ca-file. The server resolves the credential to the agent ID: the MCP client name is never trusted, `idempotencyKey`s are scoped to that agent (two agents with the same key are independent), and `get_delivery`/`ask` only reach the agent's own deliveries (foreign IDs are "not found"). Credentials are static bearer tokens, not OAuth; do not assume arbitrary remote MCP hosts support static credentials.

An MCP caller's local wait budget is independent of remote execution. Stop polling when the caller needs to return; retain the receipt and inspect later. Agentiplex keeps monitoring accepted A2A tasks even after transport lookup failures and never resubmits them to recover a result. An accepted receipt with pollFailures > 0 is not a confirmed terminal failure.

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
| get_delivery | id | Status, downstream response, child references |
| list_state | namespace | Key metadata and revisions |
| get_state | namespace, key | Value and revision |
| put_state | namespace, key, value, revision | Stored value and new revision |

Use distinct stable keys per intended operation. Keys share the existing operation-scoped admin/CLI namespace; reuse only for an identical target and text. A successful submission is durable admission, not downstream completion. Poll get_delivery with bounded backoff; stop on completed, failed, partial, uncertain, input-required or auth-required. A failed/uncertain result is not permission to replay a potentially side-effecting request with a new key. Broadcast children can be inspected separately by ID. State revision zero is create-only; update with the exact observed revision, and on conflict read/merge. No destructive registry or state deletion tools, arbitrary shell, filesystem, model sampling, prompt/resource subscriptions or MCP task extensions in this PR. Data and results are untrusted, not instructions to the calling agent. Avoid delegation loops and self-delegation to a busy agent; no automatic loop detection yet.

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

When operator and collaborator credentials and native HTTPS are configured as described in [DEPLOYMENT.md](DEPLOYMENT.md), MCP may listen on non-loopback. Pass an Authorization: Bearer header using the collaborator credential, and configure trust for the server's certificate. The SDK's DNS rebinding checks and browser Origin rejection remain enabled. The legacy unauthenticated mode stays loopback-only. The independent example accepts --token-file and --ca-file. Shared role credentials are not individual identities or OAuth; do not assume arbitrary remote MCP hosts support static credentials.

An MCP caller's local wait budget is independent of remote execution. Stop polling when the caller needs to return; retain the receipt and inspect later. Agentiplex keeps monitoring accepted A2A tasks even after transport lookup failures and never resubmits them to recover a result. An accepted receipt with pollFailures > 0 is not a confirmed terminal failure.

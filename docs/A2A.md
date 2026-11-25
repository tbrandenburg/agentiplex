# Native A2A ingress

Agentiplex exposes an A2A 1.0 JSON-RPC endpoint at `/a2a` and its own Agent Card at `/.well-known/agent-card.json`. The official Go SDK supplies the transport.

## Direct-send skill

The input is one native A2A data part with exactly these fields:

```json
{"operation":"send","target":"architect","text":"Review requirement 42"}
```

Use a fresh user-role A2A message with a unique messageId. A2A 1.0 JSON-RPC is the supported binding. The structured payload is Agentiplex's application skill contract, not an A2A routing extension or transport standard.

The returned A2A task belongs to Agentiplex; its ID also identifies the durable delivery. Downstream task and context IDs are separate correlation fields. On retry, reuse the original messageId and identical target/text. A changed payload with the same messageId is rejected. Message IDs are namespace-global until authenticated identities are introduced.

SendMessage with returnImmediately=true returns after durable enqueue. The default blocking mode waits for a terminal/intervention state, bounded by the configured delivery timeout and request cancellation. If that wait times out, work is not canceled; retry the same messageId with returnImmediately=true to obtain the existing task and poll GetTask. GetTask works across daemon restart and never exposes CLI-only delivery records as A2A tasks.

| Delivery state | Bus task state |
|---|---|
| queued | SUBMITTED |
| attempting / accepted | WORKING |
| completed | COMPLETED |
| failed | FAILED |
| uncertain | FAILED (bus cannot confirm outcome; NOT evidence that the remote task failed) |
| input-required | INPUT_REQUIRED |
| auth-required | AUTH_REQUIRED |

The original downstream message/task JSON is returned as a data artifact named downstream-response. Agentiplex-specific metadata under https://github.com/tbrandenburg/agentiplex exposes deliveryState, target and downstream task/context references. History contains the reconstructed original structured input (historyLength=0 omits it). No cross-agent session continuation, cancellation, list, push notifications, streaming, multi-tenant routing or intervention relay in this PR. Unsupported operations return native A2A errors and are not advertised.

## Provider-free demo

Start the daemon and independent simulator from [FOUNDATION.md](FOUNDATION.md), register architect using the CLI. The following example uses ordinary HTTP JSON-RPC (no Agentiplex SDK required):

```sh
curl -fsS http://127.0.0.1:8080/.well-known/agent-card.json
curl -fsS http://127.0.0.1:8080/a2a -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' -d '{
  "jsonrpc":"2.0","id":1,"method":"SendMessage",
  "params":{"message":{"messageId":"demo-native-1","role":"ROLE_USER","parts":[{"data":{"operation":"send","target":"architect","text":"Review requirement 42"}}]},"configuration":{"returnImmediately":true}}
}'
```

Read the returned task ID and query it with GetTask:

```sh
curl -fsS http://127.0.0.1:8080/a2a -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' -d '{
  "jsonrpc":"2.0","id":2,"method":"GetTask","params":{"id":"REPLACE_WITH_TASK_ID"}
}'
```

The official independent A2A Go client is exercised in the integration tests. Use a different messageId to submit new work.

## Deployment and trust

`server.public_url` / `--public-url` / `AGENTIPLEX_SERVER_PUBLIC_URL` sets the advertised origin; loopback listeners default to the actual bound address. Non-loopback listeners require an explicit advertised origin and the existing trusted-network opt-in. Docker defaults to an internal loopback listener; use the secure deployment or explicitly configure an isolated demo listener and matching advertised origin. No Host header inference.

The service is **trusted-local only**, including task lookups. No authentication, permission isolation or production network exposure. Do not put sensitive work into an unauthenticated network-accessible instance.

## Merge gates

- Official SDK client resolves the Agent Card and submits a structured request.
- Duplicate messageId does not duplicate delivery, including across a daemon restart.
- Blocking and immediate return modes; offline queue survives restart and completes.
- Unknown tasks, invalid payloads and unsupported operations return errors.
- Existing outbound/CLI tests remain green; Checks builds, runs race tests, vets and smoke-tests the container.

## Collaboration skills (PR #3)

The same structured data contract accepts operation=broadcast (target is a group ID) and operation=dispatch (target is a pool ID). Broadcast tasks return a broadcast-recipients artifact with child IDs and statuses; a partial result maps to A2A FAILED with deliveryState=partial. Dispatch metadata includes assignedAgent. See [COLLABORATION.md](COLLABORATION.md). Streaming A2A task events remain unsupported; the separate admin event SSE API is not an A2A transport.

## Authenticated deployment (PR #7)

With secure server configuration, the public Agent Card advertises bearer authentication and A2A requests require the operator or collaborator token over verified HTTPS. Outbound A2A bearer credentials are configured by explicitly allowed HTTPS origin and mounted file; they are not accepted from registered cards or stored in JetStream. See [DEPLOYMENT.md](DEPLOYMENT.md).

## Long-running accepted tasks

SendMessage requests return immediately when supported. The per-request delivery.timeout is a transport/blocking ingress wait budget, not a remote execution deadline. delivery.max_attempts limits pre-submission reachability. Once a downstream task ID is known, Agentiplex observes it via GetTask indefinitely, at delivery.task_poll_interval (zero inherits retry_delay), with exponential backoff up to one minute for consecutive failed lookups. A failed/mismatched lookup retains accepted state, last valid result, task ID, assignment and busy reservation; detail, pollFailures and lastObservedAt distinguish lost observability from a confirmed terminal outcome. Successful lookup resets failures. State survives restart. No automatic replay, task cancellation or reassignment. A permanently missing remote task may require future explicit reconciliation; it is not safe to infer execution failure from lost contact. Old terminal uncertain records remain terminal.

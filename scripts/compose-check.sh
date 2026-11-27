#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
# Run only against the isolated, disposable test project.
export COMPOSE_PROJECT_NAME=agentiplex-check
export COMPOSE_FILE=examples/engineering-team/compose.yaml
test ! -e .demo-secrets || { echo "Refusing to modify existing .demo-secrets"; exit 1; }
bash scripts/demo-secrets.sh
cleanup(){
  status=$?
  trap - EXIT
  if ((status!=0)); then docker compose logs --tail=35 >&2 || true; fi
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf .demo-secrets
  exit "$status"
}
trap cleanup EXIT
docker compose up -d --build --wait --wait-timeout 180
cli(){ docker compose exec -T agentiplex agentiplex --server https://localhost:8080 --ca-file /run/secrets/ca.crt --token-file /run/secrets/operator "$@"; }
collab(){ docker compose exec -T agentiplex agentiplex --server https://localhost:8080 --ca-file /run/secrets/ca.crt --token-file /run/secrets/agent-pm "$@"; }
# The team is declared in examples/engineering-team/compose-registry.yaml and applied at startup.
cli agents list | jq -e 'length==6'
cli groups list | jq -e 'length==1'
cli pools list | jq -e 'length==1'
collab state put engineering requirement REQ-42 >/dev/null
if collab agents remove architect >/dev/null 2>&1; then echo "agent performed admin action"; exit 1; fi
if curl --cacert .demo-secrets/ca.crt -fsS https://localhost:18443/api/v1/agents >/dev/null 2>&1; then echo "unauthenticated registry accessible"; exit 1; fi
curl --cacert .demo-secrets/ca.crt -fsS https://localhost:18443/.well-known/agent-card.json | jq -e '.securitySchemes.bearer.httpAuthSecurityScheme.scheme=="Bearer"'
wait_state(){
  local id=$1 want=$2
  for i in $(seq 1 120); do
    d=$(cli messages status "$id")
    if jq -e --arg want "$want" '.state==$want' <<<"$d" >/dev/null; then printf '%s\n' "$d"; return; fi
    sleep .5
  done
  echo "delivery did not reach $want" >&2; return 1
}
id=$(collab broadcast engineering "Compose smoke" --idempotency-key compose-broadcast | jq -er .id)
wait_state "$id" completed | jq -e '(.children|length)==6 and all(.children[];.state=="completed")'
docker compose stop quality
id=$(collab send quality "Offline recovery" --idempotency-key compose-offline | jq -er .id)
for i in $(seq 1 40); do
  d=$(cli messages status "$id")
  if jq -e '.state=="queued" and .attempts>0' <<<"$d" >/dev/null; then break; fi
  sleep .5
done
jq -e '.state=="queued" and .attempts>0' <<<"$d"
docker compose up -d --force-recreate --no-deps --wait agentiplex
# Recreate the external broker too, retaining the named volume.
docker compose up -d --force-recreate --no-deps --wait nats
for i in $(seq 1 60); do if cli status >/dev/null 2>&1; then break; fi; sleep 1; done
cli state get engineering requirement | jq -e '.value=="REQ-42"'
cli messages status "$id" | jq -e '.state=="queued"'
docker compose start quality
wait_state "$id" completed | jq -e '.result|tostring|contains("SIMULATED quality")'
go run ./examples/mcp-client --url https://localhost:18443/mcp --ca-file .demo-secrets/ca.crt --token-file .demo-secrets/agent-pm --target architect --text "Authenticated Compose MCP" --key compose-mcp >/dev/null
# Crash recovery: SIGKILL the daemon process from the host PID namespace (docker kill and
# an in-container kill of PID 1 are not real crashes and do not trigger the restart policy).
cid=$(docker compose ps -q agentiplex)
docker run --rm --pid=host --user 0 --entrypoint kill nats:2.15.3-alpine -9 "$(docker inspect -f '{{.State.Pid}}' "$cid")"
for i in $(seq 1 60); do
  if [ "$(docker inspect -f '{{.RestartCount}} {{.State.Health.Status}}' "$cid")" = "1 healthy" ]; then break; fi
  sleep 1
done
test "$(docker inspect -f '{{.RestartCount}} {{.State.Health.Status}}' "$cid")" = "1 healthy"
for i in $(seq 1 30); do if cli state get engineering requirement >/dev/null 2>&1; then break; fi; sleep 1; done
cli state get engineering requirement | jq -e '.value=="REQ-42"'
echo "PASS: authenticated TLS Compose, six-recipient broadcast, external NATS and daemon recreation, persistent state and offline delivery."

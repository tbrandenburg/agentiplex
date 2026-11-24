#!/usr/bin/env bash
# Demo driver, not an orchestration engine. Only owned children are stopped.
set -Eeuo pipefail
cd "$(dirname "$0")/../.."
check=false
browser=false
opencode=""
base_port=18080
while (($#)); do
  case "$1" in
    --check) check=true; shift ;;
    --browser-check) browser=true; shift ;;
    --opencode-url) opencode="$2"; shift 2 ;;
    --port) base_port="$2"; shift 2 ;;
    -h|--help) echo "Usage: bash examples/engineering-team/demo.sh [--check] [--browser-check] [--port 18080] [--opencode-url http://127.0.0.1:41241]"; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ "$base_port" =~ ^[0-9]{1,5}$ ]] && ((10#$base_port >= 1024 && 10#$base_port <= 65529)) || { echo "Invalid base port" >&2; exit 2; }
base_port=$((10#$base_port))
if [[ -n "$opencode" && ! "$opencode" =~ ^http://127\.0\.0\.1:[0-9]{1,5}/?$ ]]; then
  echo "OpenCode URL must be a trusted IPv4 loopback HTTP origin" >&2; exit 2
fi
for tool in go curl jq; do command -v "$tool" >/dev/null || { echo "Missing $tool" >&2; exit 1; }; done
if $browser; then command -v node >/dev/null || { echo "Missing Node.js 22+" >&2; exit 1; }; fi

work=$(mktemp -d)
pids=()
cleanup() {
  local status=$?
  trap - EXIT
  for pid in "${pids[@]}"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi
  done
  for pid in "${pids[@]}"; do
    if [[ -n "$pid" ]]; then wait "$pid" 2>/dev/null || true; fi
  done
  if ((status != 0)); then
    echo "Demo failed; daemon log:" >&2
    tail -n 60 "$work/daemon.log" 2>/dev/null >&2 || true
    for log in "$work"/agent-*.log; do [[ -f "$log" ]] && { echo "$log" >&2; tail -n 15 "$log" >&2; }; done
  fi
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

server="http://127.0.0.1:$base_port"
port_pm=$((base_port+1))
port_arch=$((base_port+2))
port_dev=$((base_port+3))
port_test1=$((base_port+4))
port_test2=$((base_port+5))
port_quality=$((base_port+6))
go generate ./internal/dashboard
printf 'Building Agentiplex and independent A2A simulator...\n'
go build -mod=readonly -o "$work/agentiplexd" ./cmd/agentiplexd
go build -mod=readonly -o "$work/agentiplex" ./cmd/agentiplex
go build -mod=readonly -o "$work/simulated-agent" ./examples/simulated-agent

cli() { "$work/agentiplex" --server "$server" "$@"; }
http() { curl --noproxy '*' --connect-timeout 2 --max-time 8 -fsS "$@"; }
assert_free() {
  if curl --noproxy '*' --connect-timeout 1 --max-time 1 -sS "http://127.0.0.1:$1/" >/dev/null 2>&1; then
    echo "Port $1 already serves HTTP; choose another --port" >&2; exit 1
  fi
}
await_http() {
  local url=$1 pid=$2 deadline=$((SECONDS+25))
  while ((SECONDS < deadline)); do
    if ! kill -0 "$pid" 2>/dev/null; then echo "Process exited before readiness: $url" >&2; return 1; fi
    if http "$url" >/dev/null 2>&1 && kill -0 "$pid" 2>/dev/null; then return 0; fi
    sleep 0.15
  done
  echo "Readiness timeout: $url" >&2; return 1
}
start_daemon() {
  assert_free "$base_port"
  AGENTIPLEX_DELIVERY_RETRY_DELAY=1s AGENTIPLEX_DELIVERY_MAX_ATTEMPTS=40 \
    "$work/agentiplexd" --listen "127.0.0.1:$base_port" --public-url "$server" --broker-mode embedded --namespace demo --storage "$work/state" --dashboard >"$work/daemon.log" 2>&1 &
  daemon_pid=$!
  daemon_index=${#pids[@]}
  pids+=("$daemon_pid")
  await_http "$server/readyz" "$daemon_pid"
}
start_agent() {
  local id=$1 skill=$2 port=$3
  assert_free "$port"
  "$work/simulated-agent" --name "$id" --skill "$skill" --port "$port" >"$work/agent-$id.log" 2>&1 &
  last_pid=$!
  last_index=${#pids[@]}
  pids+=("$last_pid")
  await_http "http://127.0.0.1:$port/.well-known/agent-card.json" "$last_pid"
}
stop_child() {
  local index=$1 pid=${pids[$1]}
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  pids[$index]=""
}
register() { cli register "$1" "$2" >/dev/null; }
wait_state() {
  local id=$1 desired=$2 timeout=$3 deadline result state
  deadline=$((SECONDS+timeout))
  while ((SECONDS < deadline)); do
    result=$(cli messages status "$id")
    state=$(jq -er .state <<<"$result")
    if [[ "$state" == "$desired" ]]; then printf '%s\n' "$result"; return 0; fi
    case "$state" in completed|failed|uncertain|partial|input-required|auth-required)
      echo "Unexpected terminal state $state for $id" >&2; jq -c '{id,state,detail}' <<<"$result" >&2; return 1 ;;
    esac
    sleep .2
  done
  echo "Delivery $id did not reach $desired" >&2; return 1
}
receipt_id() { jq -er .id; }
native_send() {
  local id=$1 target=$2 prompt=$3
  jq -nc --arg id "$id" --arg target "$target" --arg text "$prompt" \
    '{jsonrpc:"2.0",id:1,method:"SendMessage",params:{message:{messageId:$id,role:"ROLE_USER",parts:[{data:{operation:"send",target:$target,text:$text}}]},configuration:{returnImmediately:true}}}' |
    http "$server/a2a" -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' --data-binary @-
}
native_get() {
  jq -nc --arg id "$1" '{jsonrpc:"2.0",id:2,method:"GetTask",params:{id:$id}}' |
    http "$server/a2a" -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' --data-binary @-
}
show() { jq -c '{id,kind,target,state,assignedAgent,children:[.children[]? | {target,state}]}' <<<"$1"; }

start_daemon
start_agent pm planning "$port_pm"; register pm "http://127.0.0.1:$port_pm"
if [[ -n "$opencode" ]]; then
  echo "Using operator-started OpenCode gateway at $opencode (not simulated)."
  register architect "$opencode"
else
  start_agent architect architecture "$port_arch"; register architect "http://127.0.0.1:$port_arch"
fi
start_agent developer development "$port_dev"; register developer "http://127.0.0.1:$port_dev"
start_agent tester-1 testing "$port_test1"; register tester-1 "http://127.0.0.1:$port_test1"
start_agent tester-2 testing "$port_test2"; register tester-2 "http://127.0.0.1:$port_test2"
start_agent quality quality "$port_quality"; quality_index=$last_index; register quality "http://127.0.0.1:$port_quality"

echo "Dashboard: $server/ui/"
# Only the explicit read-only architecture request invokes the optional model.
members=(pm developer tester-1 tester-2 quality)
if [[ -z "$opencode" ]]; then members+=(architect); fi
cli groups set engineering "${members[@]}" >/dev/null
cli pools set testers tester-1 tester-2 >/dev/null

echo "1. Discovery: six registered agents, two testing skills and standard catalog"
cli agents list | jq -e 'length == 6' >/dev/null
cli discover --skill testing | jq -e 'length == 2' >/dev/null
cli catalog get | jq -e '.entries | length == 6' >/dev/null
cli discover --skill testing | jq -c '[.[].id]'
requirement="REQ-42: on motor communication loss, stop within 100 ms and prohibit automatic restart."
echo "2. PM planning (fixture), then native A2A architecture review"
id=$(cli send pm "Plan review of $requirement" --idempotency-key demo-plan | receipt_id)
plan=$(wait_state "$id" completed 60); show "$plan"
arch_id=$(native_send demo-architecture architect "Read-only: review this requirement and propose verification considerations. Do not edit files or run commands. $requirement" | jq -er .result.task.id)
architecture=$(wait_state "$arch_id" completed 240)
native_get "$arch_id" | jq -e '.result.status.state == "TASK_STATE_COMPLETED" and (.result.artifacts|length > 0)' >/dev/null
show "$architecture"
if [[ -z "$opencode" ]]; then
  jq -e '.result | tostring | contains("SIMULATED architect")' <<<"$architecture" >/dev/null
else
  echo "Actual gateway result (not a fixture):"
  jq -c '.result' <<<"$architecture"
fi
echo "3. Developer receives architecture handoff through the real bus"
review=$(jq -r '[.result | .. | objects | .text? | select(type=="string")] | join(" ") | .[0:800]' <<<"$architecture")
id=$(cli send developer "$requirement Architecture response: $review" --idempotency-key demo-dev | receipt_id)
dev=$(wait_state "$id" completed 60); show "$dev"
echo "4. Broadcast contract update to every configured member"
id=$(cli broadcast engineering "Contract update: $requirement" --idempotency-key demo-broadcast | receipt_id)
broadcast=$(wait_state "$id" completed 60)
jq -e --argjson size "${#members[@]}" '(.children|length)==$size and all(.children[];.state=="completed")' <<<"$broadcast" >/dev/null
show "$broadcast"
echo "5. Dispatch two verification requests; one recipient per task"
id=$(cli dispatch testers "Plan timing measurement for $requirement" --idempotency-key demo-test-1 | receipt_id)
one=$(wait_state "$id" completed 60)
id=$(cli dispatch testers "Plan restart inhibition check for $requirement" --idempotency-key demo-test-2 | receipt_id)
two=$(wait_state "$id" completed 60)
jq -e --argjson other "$two" '(.assignedAgent=="tester-1" or .assignedAgent=="tester-2") and ($other.assignedAgent=="tester-1" or $other.assignedAgent=="tester-2") and .assignedAgent!=$other.assignedAgent' <<<"$one" >/dev/null
show "$one"; show "$two"
echo "6. Stop Quality, durably queue work, then restart the daemon"
stop_child "$quality_index"
id=$(cli send quality "Review verification evidence requirements for $requirement" --idempotency-key demo-offline | receipt_id)
deadline=$((SECONDS+15))
while :; do
  pending=$(cli messages status "$id")
  if jq -e '.state=="queued" and .attempts>0' <<<"$pending" >/dev/null; then break; fi
  if ((SECONDS>=deadline)); then echo "Offline retry not observed" >&2; exit 1; fi
  sleep .2
done
show "$pending"
stop_child "$daemon_index"
start_daemon
cli agents list | jq -e 'length==6' >/dev/null
cli messages status "$id" | jq -e '.state=="queued"' >/dev/null
echo "7. Restart Quality; observe original delivery complete"
start_agent quality quality "$port_quality"
done_delivery=$(wait_state "$id" completed 60); show "$done_delivery"
echo "8. Verify live console data and durable event replay"
html=$(http "$server/ui/")
grep -q 'Agent collaboration console' <<<"$html"
http "$server/api/v1/deliveries?limit=50" | jq -e 'length==7 and all(.[];.state=="completed") and all(.[];has("text")|not)' >/dev/null
http "$server/api/v1/events" | jq -e 'length>0' >/dev/null
if $browser; then node examples/engineering-team/browser-check.mjs "$server"; fi
echo "PASS: all seven operations completed; offline recovery and restart verified."
echo "Simulated outputs are fixtures, NOT executed tests, repository changes or safety evidence."
echo "Dashboard: $server/ui/"
if $check; then exit 0; fi
echo "Demo team remains active for inspection. Ctrl+C stops the owned processes."
while :; do
  if ! kill -0 "$daemon_pid" 2>/dev/null; then echo "Daemon exited" >&2; exit 1; fi
  sleep 1
done

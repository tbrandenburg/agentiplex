#!/usr/bin/env bash
# Trusted-local example. A single operator/runner; do not run concurrently.
set -euo pipefail
EX=$(cd "$(dirname "$0")" && pwd)
CLI=${AGENTIPLEX_BIN:-$EX/../../bin/agentiplex}
NS=repository
mode=${1:-run}
fail() { echo "Contract paused: $*" >&2; exit 1; }
get() { "$CLI" state get "$NS" "$1"; }
put() { "$CLI" state put "$NS" "$1" "$2" --revision "$3" >/dev/null; }
template=$(jq -c . "$EX/contract.json")
objective=$(cat "$EX/objective.contract.md")

if [[ $mode == init ]]; then
  # Explicit operator approval, fresh storage only. Never overwrite an objective.
  put objective "$objective" 0
  approved=$(get objective)
  objective_revision=$(jq -er .revision <<<"$approved")
  put contract "$template" 0
  put contract-run "$(jq -cn --argjson started "$(date +%s)" --argjson approved "$objective_revision" '{round:1,started:$started,approved:$approved}')" 0
  echo "Contract approved. Run: bash $EX/run-contract.sh run"
  exit 0
fi
[[ $mode == run ]] || fail "usage: $0 init|run"

approved=$(get objective)
[[ $(jq -r .value <<<"$approved") == "$objective" ]] || fail "original objective differs"
objective_revision=$(jq -r .revision <<<"$approved")
shape() { jq -c '[.criteria[] | {id,description,owner}]' <<<"$1"; }
expected=$(shape "$template")
read_contract() {
  local record
  record=$(get contract)
  contract=$(jq -er .value <<<"$record")
  [[ $(shape "$contract") == "$expected" ]] || fail "approved criteria changed"
  jq -e '(.blocker|type=="string") and all(.criteria[]; (.status == "pending" or .status == "rework" or .status == "verified") and (.delivery|type=="string") and (.evidence|type=="string") and (if .status=="verified" then (.delivery|length)>0 and (.evidence|length)>0 else true end))' <<<"$contract" >/dev/null || fail "invalid checklist"
}
show() {
  jq -r '.criteria[] | "[\(if .status=="verified" then "x" else " " end)] \(.id) (\(.owner)): \(.status) — \(.evidence)"' <<<"$contract"
}
check_evidence() {
  local id owner receipt
  jq -e '[.criteria[] | select(.status=="verified") | .delivery] | length == (unique|length)' <<<"$contract" >/dev/null || fail "duplicate criterion delivery"
  while IFS=$'\t' read -r id owner; do
    receipt=$("$CLI" messages status "$id")
    jq -e --arg owner "$owner" '.target==$owner and .state=="completed" and .result!=null' <<<"$receipt" >/dev/null || fail "missing completed owner delivery $id"
  done < <(jq -r '.criteria[] | select(.status=="verified") | [.delivery,.owner] | @tsv' <<<"$contract")
}

while true; do
  cursor=$(get contract-run)
  round=$(jq -er '.value | fromjson | .round' <<<"$cursor")
  started=$(jq -er '.value | fromjson | .started' <<<"$cursor")
  revision=$(jq -er .revision <<<"$cursor")
  # Default terminal receipt retention is seven days; never replay old intent.
  (( $(date +%s) - started >= 0 && $(date +%s) - started < 86400 )) || fail "run too old for automatic recovery"
  [[ $(jq -er '.value | fromjson | .approved' <<<"$cursor") == "$objective_revision" ]] || fail "approved objective revision changed"
  read_contract
  if [[ $round == 0 ]]; then
    jq -e '(.blocker=="") and all(.criteria[]; .status=="verified")' <<<"$contract" >/dev/null || fail "closed checklist changed"
    check_evidence
    show
    echo "Checklist fulfilled; independently verify the actual patch before acceptance."
    exit 0
  fi
  [[ $(jq -r .blocker <<<"$contract") == "" ]] || fail "$(jq -r .blocker <<<"$contract")"
  (( round >= 1 && round <= 8 )) || fail "round budget exhausted"
  # Intent (round) already persisted. The same key AND text reattach after a crash.
  receipt=$("$CLI" send pm "Contract round $round: read the approved objective and living checklist; progress the contract within your role." --idempotency-key "contract-round-$round")
  id=$(jq -er .id <<<"$receipt")
  result=$(timeout 20m "$CLI" messages wait "$id" --json) || fail "round $round unresolved; inspect delivery $id (no automatic replay under a new key)"
  jq -e --arg id "$id" '.id==$id and .state=="completed"' <<<"$result" >/dev/null || fail "round $round did not complete"
  current=$(get objective)
  [[ $(jq -r .value <<<"$current") == "$objective" && $(jq -r .revision <<<"$current") == "$objective_revision" ]] || fail "objective changed during run"
  read_contract
  check_evidence
  show
  [[ $(jq -r .blocker <<<"$contract") == "" ]] || fail "$(jq -r .blocker <<<"$contract")"
  if (( round == 1 )); then
    jq -e '(.criteria[0].status=="verified") and (.criteria[1].status=="pending") and (.criteria[2].status=="rework" and (.criteria[2].evidence|length)>0) and (.criteria[3].status=="pending")' <<<"$contract" >/dev/null || fail "first round must preserve the failed verification checkpoint"
  fi
  if jq -e 'all(.criteria[]; .status=="verified")' <<<"$contract" >/dev/null; then
    next=0
  else
    next=$((round + 1))
  fi
  # Only advance after observing a terminal PM delivery and validating the checklist.
  put contract-run "$(jq -cn --argjson round "$next" --argjson started "$started" --argjson approved "$objective_revision" '{round:$round,started:$started,approved:$approved}')" "$revision"
done

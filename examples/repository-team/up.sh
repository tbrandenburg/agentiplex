#!/usr/bin/env bash
# Convenience only: runs the README native commands in the background. No other logic.
# Usage: ACCEPT_FULL_ACCESS=yes up.sh <disposable-git-workspace>
set -euo pipefail
EX=$(cd "$(dirname "$0")" && pwd); ROOT=$EX/../..
[[ ${ACCEPT_FULL_ACCESS:-} == yes ]] || { echo "developer and tester run with FULL ACCESS as your OS user (shell, files, network, Git). Use a disposable workspace/host, then set ACCEPT_FULL_ACCESS=yes." >&2; exit 2; }
W=$(cd "${1:?usage: up.sh <disposable-git-workspace>}" && pwd)
RUN=$(mktemp -d); P=$RUN/policies            # mode 0700; the gateway rejects group-writable policy paths
[[ -e $W/opencode.json ]] || cp "$EX/opencode.json" "$W/opencode.json"   # never overwrite an operator-owned config
echo /opencode.json >> "$W/.git/info/exclude"
cp -r "$EX/policies" "$P" && chmod 700 "$P" && chmod 600 "$P"/*
make -C "$ROOT" build >/dev/null
setsid "$ROOT/bin/agentiplexd" --config "$EX/team.yaml" --storage "$RUN/jetstream" >"$RUN/daemon.log" 2>&1 &
echo $! >> "$RUN/pids"
curl -fs -o /dev/null --retry 60 --retry-delay 1 --retry-connrefused http://127.0.0.1:19080/readyz || { echo "daemon failed; see $RUN/daemon.log" >&2; exit 1; }
gw() {  # name port access policy description [extra gateway flags]
  setsid opencode-a2a-lite --name "$1" --description "$5" --workspace "$W" --system-prompt-file "$EX/roles/$1.md" --port "$2" --access "$3" --policy-file "$P/$4.json" --prompt-timeout 45m "${@:6}" >"$RUN/$1.log" 2>&1 &
  echo $! >> "$RUN/pids"
  curl -fs -o /dev/null --retry 120 --retry-delay 2 --retry-connrefused "http://127.0.0.1:$2/.well-known/agent-card.json" \
    || { echo "gateway $1 did not start; see $RUN/$1.log; stop with bash examples/down.sh $RUN" >&2; exit 1; }
  "$ROOT/bin/agentiplex" --server http://127.0.0.1:19080 register "$1" "http://127.0.0.1:$2" >/dev/null
}
gw pm 19081 read coordinator "Engineering coordinator" --no-env
gw architect 19082 read reader "Architecture reviewer" --no-env
gw developer 19083 danger-full-access writer "Repository implementation and repair" --no-env
gw tester 19084 danger-full-access writer "Independent executable verification" --no-env
gw quality 19085 read reader "Independent acceptance review" --no-env
printf 'Team up (dashboard http://127.0.0.1:19080/ui/).\nRUN=%q\n' "$RUN"
printf '%s\n' 'From the repository root, publish and submit the same objective:' '  OBJECTIVE="$(cat examples/repository-team/objective.md)"' '  bin/agentiplex --server http://127.0.0.1:19080 state put repository objective "$OBJECTIVE" --revision 0 &&' '  bin/agentiplex --server http://127.0.0.1:19080 send pm "$OBJECTIVE" --idempotency-key repository-objective --wait' '  bin/agentiplex --server http://127.0.0.1:19080 notes list repository'
printf 'Stop with: bash examples/down.sh %q\n' "$RUN"

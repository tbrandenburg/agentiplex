#!/usr/bin/env bash
# Convenience only: runs the README commands (section 2) in the background. No other logic.
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
gw() {  # name port access policy description [extra gateway flags]
  A2A_NAME=$1 A2A_DESCRIPTION="$5" A2A_TAGS=$1,real-opencode \
    setsid opencode-a2a-lite --workspace "$W" --system-prompt-file "$EX/roles/$1.md" --port "$2" --access "$3" --policy-file "$P/$4.json" --prompt-timeout 45m "${@:6}" >"$RUN/$1.log" 2>&1 &
  echo $! >> "$RUN/pids"
}
gw pm 19081 read coordinator "Engineering coordination and objective ownership" --no-env
gw architect 19082 read reader "Architecture analysis and technical recommendations" --no-env
gw developer 19083 danger-full-access writer "Repository implementation and repair"
gw tester 19084 danger-full-access writer "Independent executable verification"
gw quality 19085 read reader "Independent change and acceptance review" --no-env
for port in 19081 19082 19083 19084 19085; do   # curl's own retry: the gateway preflight takes a minute or more
  curl -fs -o /dev/null --retry 120 --retry-delay 2 --retry-connrefused "http://127.0.0.1:$port/.well-known/agent-card.json" \
    || { echo "gateway on $port did not start; see $RUN/*.log (the preflight is flaky: run bash examples/down.sh $RUN from the repository root and retry)" >&2; exit 1; }
done
setsid "$ROOT/bin/agentiplexd" --config "$EX/team.yaml" --storage "$RUN/jetstream" >"$RUN/daemon.log" 2>&1 &
echo $! >> "$RUN/pids"
curl -fs -o /dev/null --retry 60 --retry-delay 1 --retry-connrefused http://127.0.0.1:19080/readyz || { echo "daemon failed; see $RUN/daemon.log" >&2; exit 1; }
printf 'Team up (dashboard http://127.0.0.1:19080/ui/).\nRUN=%q\n' "$RUN"
printf '%s\n' 'From the repository root, submit the objective:' '  bin/agentiplex --server http://127.0.0.1:19080 send pm "$(cat examples/repository-team/objective.md)" --idempotency-key repository-objective'
printf 'Stop with: bash examples/down.sh %q\n' "$RUN"

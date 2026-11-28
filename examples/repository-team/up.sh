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
gw() {  # name port access policy [extra gateway flags]
  A2A_NAME=$1 A2A_DESCRIPTION="$(cat "$EX/roles/$1.md")" A2A_TAGS=$1,real-opencode \
    setsid opencode-a2a-lite --workspace "$W" --port "$2" --access "$3" --policy-file "$P/$4.json" --prompt-timeout 45m "${@:5}" >"$RUN/$1.log" 2>&1 &
  echo $! >> "$RUN/pids"
}
gw pm 19081 read coordinator --no-env
gw architect 19082 read reader --no-env
gw developer 19083 danger-full-access writer
gw tester 19084 danger-full-access writer
gw quality 19085 read reader --no-env
for port in 19081 19082 19083 19084 19085; do   # curl's own retry: the gateway preflight takes a minute or more
  curl -fs -o /dev/null --retry 120 --retry-delay 2 --retry-connrefused "http://127.0.0.1:$port/.well-known/agent-card.json" \
    || { echo "gateway on $port did not start; see $RUN/*.log (the preflight is flaky: run down.sh $RUN and retry)" >&2; exit 1; }
done
setsid "$ROOT/bin/agentiplexd" --config "$EX/team.yaml" --storage "$RUN/jetstream" >"$RUN/daemon.log" 2>&1 &
echo $! >> "$RUN/pids"
curl -fs -o /dev/null --retry 60 --retry-delay 1 --retry-connrefused http://127.0.0.1:19080/readyz || { echo "daemon failed; see $RUN/daemon.log" >&2; exit 1; }
echo "Team up (run dir $RUN, dashboard http://127.0.0.1:19080/ui/). Next: make repository-team-send; stop with: make repository-team-down RUN=$RUN"

#!/usr/bin/env bash
# Convenience only: runs the README native commands in the background. No other logic.
# Usage: up.sh
set -euo pipefail
EX=$(cd "$(dirname "$0")" && pwd); ROOT=$EX/../..
RUN=$(mktemp -d)                             # mode 0700; the gateway rejects group-writable policy paths
cp -r "$EX/policies" "$RUN/policies" && chmod 700 "$RUN/policies" && chmod 600 "$RUN"/policies/*
for r in pm architect; do mkdir "$RUN/$r"; cp "$EX/profiles/$r/opencode.json" "$RUN/$r/"; cp -r "$EX/fixture" "$RUN/$r/"; done
make -C "$ROOT" build >/dev/null
go build -C "$ROOT" -o bin/ ./examples/simulated-agent
spawn() { local name=$1; shift; setsid "$@" >"$RUN/$name.log" 2>&1 & echo $! >> "$RUN/pids"; }
spawn developer "$ROOT/bin/simulated-agent" --name developer --skill development --port 19083
spawn tester-1  "$ROOT/bin/simulated-agent" --name tester-1   --skill testing     --port 19084
spawn tester-2  "$ROOT/bin/simulated-agent" --name tester-2   --skill testing     --port 19085
spawn quality   "$ROOT/bin/simulated-agent" --name quality    --skill quality     --port 19086
spawn pm opencode-a2a-lite --name pm --description "Read-only planning coordinator" --workspace "$RUN/pm" --port 19081 --access read --no-env --policy-file "$RUN/policies/coordinator.json" --prompt-timeout 10m
spawn architect opencode-a2a-lite --name architect --description "Read-only architecture reviewer" --workspace "$RUN/architect" --port 19082 --access read --no-env --policy-file "$RUN/policies/worker.json" --prompt-timeout 10m
for port in 19081 19082 19083 19084 19085 19086; do   # curl's own retry: the gateway preflight takes a minute or more
  curl -fs -o /dev/null --retry 120 --retry-delay 2 --retry-connrefused "http://127.0.0.1:$port/.well-known/agent-card.json" \
    || { echo "agent on $port did not start; see $RUN/*.log (the gateway preflight is flaky: run bash examples/down.sh $RUN from the repository root and retry)" >&2; exit 1; }
done
spawn daemon "$ROOT/bin/agentiplexd" --config "$EX/team.yaml" --storage "$RUN/jetstream"
curl -fs -o /dev/null --retry 60 --retry-delay 1 --retry-connrefused http://127.0.0.1:19080/readyz || { echo "daemon failed; see $RUN/daemon.log" >&2; exit 1; }
printf 'Team up (dashboard http://127.0.0.1:19080/ui/).\nRUN=%q\n' "$RUN"
printf '%s\n' 'From the repository root, submit the objective:' '  bin/agentiplex --server http://127.0.0.1:19080 send pm "$(cat examples/autonomous-team/objective.md)" --idempotency-key review --wait' '  bin/agentiplex --server http://127.0.0.1:19080 state get review pm' '  bin/agentiplex --server http://127.0.0.1:19080 state get review architect' '  bin/agentiplex --server http://127.0.0.1:19080 notes list review'
printf 'Stop with: bash examples/down.sh %q\n' "$RUN"

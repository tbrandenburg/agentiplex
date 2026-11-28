#!/usr/bin/env bash
# Stops exactly the process groups recorded by up.sh. Usage: down.sh <run-dir>
set -u
RUN=${1:?usage: down.sh <run-dir>}
while read -r pid; do kill -- "-$pid" 2>/dev/null || true; done < "$RUN/pids"
echo "Stopped. Delete $RUN when done (logs, JetStream data)."

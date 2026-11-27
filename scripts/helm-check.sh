#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
test ! -e .demo-secrets || { echo "Refusing to modify existing .demo-secrets"; exit 1; }
bash scripts/demo-secrets.sh
cluster=agentiplex-check
pf=""
cleanup(){
  status=$?
  trap - EXIT
  if ((status!=0)); then kubectl get pods,pvc -A >&2 || true; kubectl logs deploy/agentiplex --tail=40 >&2 || true; fi
  if [[ -n "$pf" ]]; then kill "$pf" 2>/dev/null || true; fi
  kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
  rm -rf .demo-secrets
  exit "$status"
}
trap cleanup EXIT
kind create cluster --name "$cluster" --wait 120s
kind load docker-image agentiplex:ci --name "$cluster"
kubectl create secret generic agentiplex-auth --from-file=.demo-secrets/operator --from-file=.demo-secrets/agent-demo --from-file=.demo-secrets/viewer
kubectl create secret tls agentiplex-tls --cert=.demo-secrets/server.crt --key=.demo-secrets/server.key
kubectl create secret tls nats-tls --cert=.demo-secrets/nats.crt --key=.demo-secrets/nats.key
kubectl create secret generic agentiplex-broker --from-file=token=.demo-secrets/nats-token --from-file=.demo-secrets/ca.crt
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm repo update nats
helm upgrade --install nats nats/nats --version 2.15.2 -f deploy/nats-values.yaml --wait --timeout 5m
helm lint charts/agentiplex --set 'agents={demo}'
helm upgrade --install agentiplex charts/agentiplex --set 'agents={demo}' --set image.repository=agentiplex --set image.tag=ci --wait --timeout 5m
port_forward(){
  kubectl port-forward svc/agentiplex 18443:8080 >/tmp/agentiplex-port-forward.log 2>&1 &
  pf=$!
  for i in $(seq 1 30); do if curl --cacert .demo-secrets/ca.crt -fsS https://localhost:18443/readyz >/dev/null 2>&1; then return; fi; sleep 1; done
  return 1
}
port_forward
go build -mod=readonly -o /tmp/agentiplex-helm-cli ./cmd/agentiplex
cli(){ /tmp/agentiplex-helm-cli --server https://localhost:18443 --ca-file .demo-secrets/ca.crt --token-file .demo-secrets/agent-demo "$@"; }
cli state put engineering key durable | jq -e '.revision>0'
kill "$pf" 2>/dev/null || true; wait "$pf" 2>/dev/null || true; pf=""
kubectl rollout restart deployment/agentiplex
kubectl rollout status deployment/agentiplex --timeout=180s
port_forward
cli state get engineering key | jq -e '.value=="durable"'
curl --cacert .demo-secrets/ca.crt -fsS https://localhost:18443/.well-known/agent-card.json | jq -e '.supportedInterfaces[0].url=="https://localhost:18443/a2a"'
echo "PASS: official NATS Helm chart, TLS/authenticated single-replica Agentiplex and persistent state across rollout."

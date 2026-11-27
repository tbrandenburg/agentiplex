#!/usr/bin/env bash
# Development-only credentials and certificates. Never overwrite existing secrets.
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
if [[ -d .demo-secrets ]]; then
  echo ".demo-secrets already exists; retain it or explicitly remove it before rotating" >&2
  exit 1
fi
mkdir -m 700 .demo-secrets
cd .demo-secrets
for name in operator agent-demo viewer nats-token agent-pm agent-architect agent-developer agent-tester-1 agent-tester-2 agent-quality; do openssl rand -hex 32 > "$name"; done
openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 7 -subj /CN=Agentiplex-demo-CA -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign -keyout ca.key -out ca.crt 2>/dev/null
for name in server nats; do
  openssl req -newkey rsa:2048 -nodes -subj "/CN=$name" -keyout "$name.key" -out "$name.csr" 2>/dev/null
  san="DNS:localhost,IP:127.0.0.1,DNS:agentiplex"
  if [[ "$name" == nats ]]; then san="DNS:nats"; fi
  printf 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=%s\n' "$san" > "$name.ext"
  openssl x509 -req -in "$name.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -out "$name.crt" -days 7 -sha256 -extfile "$name.ext" 2>/dev/null
done
cat > nats.conf <<EOF
port: 4222
http: 127.0.0.1:8222
jetstream { store_dir: "/data" }
authorization { token: "$(cat nats-token)" }
tls { cert_file: "/run/secrets/nats.crt", key_file: "/run/secrets/nats.key" }
EOF
# Bind-mounted files must be readable by the non-root service user. The
# containing directory remains 0700; never publish or copy it into images.
chmod 0444 operator viewer nats-token agent-* nats.conf server.crt server.key ca.crt nats.crt nats.key
rm -f ca.key ca.srl server.csr server.ext nats.csr nats.ext
echo "Development secrets generated; trust .demo-secrets/ca.crt explicitly. Certificates expire in 7 days."

# Engineering team (Docker Compose)

Static, TLS-secured Compose fixture: Agentiplex, official NATS (persistent `jetstream` volume) and six simulated agents on a private network. The team itself is declared in [compose-registry.yaml](compose-registry.yaml). Only `https://localhost:18443` is published, on host loopback. See [Deployment](../../docs/DEPLOYMENT.md#compose).

```sh
bash scripts/demo-secrets.sh   # once; writes git- and Docker-ignored .demo-secrets/
docker compose -f examples/engineering-team/compose.yaml up -d --build --wait
docker compose -f examples/engineering-team/compose.yaml down   # keeps volume and credentials
```

`make compose-up` / `make compose-down` run the same commands (compose-up creates missing demo secrets). The project name is `agentiplex-demo`; secret paths in `compose.yaml` are relative to that file, the build context is the repository root.

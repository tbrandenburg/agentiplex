# Examples: running real OpenCode agents

Applies to `repository-team` and `autonomous-team`. These start real, partly
full-access OpenCode agents with their **native commands**; each example has one `team.yaml` that is passed unchanged to
`agentiplexd --config team.yaml`. There is no launcher: follow the example README. The core middleware does not depend on any of this.
Ports are fixed loopback ports (daemon 19080, agents 19081 and up); both examples use 19080, so run one at a time and check `ss -ltn` first.
Trusted-local means unauthenticated loopback HTTP; for secure operation use an operator-owned copy of `team.yaml`.

Quick start: `make repository-team-up|send|down` and `make autonomous-team-up|send|down` (see each example's README). The scripts only
run the documented commands in the background; prompts are plain files (`roles/`, `objective.md`, `profiles/`).

## Setup
- Go 1.26+, Git, Make, Node/npm, and Bun 1.4.4 (the gateway needs `bun` on PATH). Python 3.10+ is only needed for the
  optional verification scripts in [tests/examples](../tests/examples).
- `opencode` (2.3.2 or newer) and `bun` must be on PATH, plus the gateway `opencode-a2a-lite` (0.9.0 or newer):
  ```sh
  npm install --global @tbrandenburg/opencode-a2a-lite
  ```
  (`npx @tbrandenburg/opencode-a2a-lite@^0.9.0 ...` also works, but stopping the npx wrapper can orphan the real gateway.)
- Gateway policy files (`--policy-file`) must be outside the workspace, owned by you and neither they nor any ancestor
  directory may be group/world-writable. If your checkout or umask makes them group-writable, use private copies as the READMEs do.
  "OpenCode 2.3.2 or newer" is accepted, but 2.4.1 is no more reliable than 2.3.2 (see below).

## Secure mode (verified HTTPS + per-agent tokens)

> **Status: NOT a complete working demonstration on stock gateways.** `opencode-a2a-lite` 0.9.0 builds the OpenCode
> child environment from scratch: `NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE` are rejected by `--pass-env` and ignored in the
> gateway's own environment, so OpenCode cannot trust a private CA and fails closed (`unable to verify the first certificate`,
> no Agentiplex tools). Real PM + worker authentication (`caller=pm` observed) was reproduced **only with a locally patched
> copy** of the gateway that forwards an operator-set absolute `NODE_EXTRA_CA_CERTS` from the gateway's own environment; that
> change is **not yet released**. Without it you need a CA already trusted by the system store of the host (root required; untested).
> Daemon-side behavior (below) was verified without the patch and without a model. Tested: OpenCode 2.4.1, gateway 0.9.0
> (+ local patch), Bun 1.4.4, `agentiplexd` from this repository.

Trusted-local HTTP (the default of these examples) stays separate: secure mode never falls back to HTTP, there is no
skip-verify, and an untrusted certificate or a wrong/missing token is rejected.

**Operator prerequisites (all files outside the repository, mode 0600/0700, never committed).** One private CA, a server
certificate for the daemon (SAN `localhost`/`127.0.0.1` or your host name, `serverAuth`), one distinct 32-512 character token
file each for `operator`, `viewer` (dashboard) and every agent. [scripts/demo-secrets.sh](../scripts/demo-secrets.sh) shows the
openssl/`openssl rand -hex 32` steps used by the repo's tests; field semantics are in
[DEPLOYMENT](../docs/DEPLOYMENT.md#static-credentials-and-tls).

**Edit an operator-owned copy of `team.yaml`** (the committed file stays unchanged; the roster is not duplicated):
```yaml
server:   # add to the existing block
  public_url: "https://localhost:19080"
  tls_cert_file: /secure/server.crt
  tls_key_file: /secure/server.key
registry:
  agents:
    pm: {url: "http://127.0.0.1:19081", token_file: /secure/pm}   # add token_file to EVERY agent
security:
  operator_token_file: /secure/operator
  viewer_token_file: /secure/viewer     # required while the dashboard is enabled
```
Agent endpoints may stay loopback HTTP; only agent-to-daemon traffic is TLS.

**Shared `opencode.json`**: change the MCP entry to `"url": "https://localhost:19080/mcp"` and add
`"oauth": false` and `"headers": {"Authorization": "Bearer {env:AGENTIPLEX_TOKEN}"}`. No token is in the file. Give **each
gateway process its own token from its file** and forward only that name, e.g. for `pm`:
```sh
AGENTIPLEX_TOKEN=$(cat /secure/pm) NODE_EXTRA_CA_CERTS=/secure/ca.crt A2A_NAME=pm ... opencode-a2a-lite ... --pass-env AGENTIPLEX_TOKEN
```
(`NODE_EXTRA_CA_CERTS` only helps with the patched gateway described above; skip it with a system-trusted CA.) Never pass the
operator token to an agent, never put a token in arguments or prompts, and keep policy files outside the workspace (see Setup).

**Operator CLI** (separate credential): `bin/agentiplex --server https://localhost:19080 --ca-file /secure/ca.crt --token-file /secure/operator agents list`
(`AGENTIPLEX_SERVER`/`AGENTIPLEX_CA_FILE`/`AGENTIPLEX_TOKEN_FILE` are equivalent).

**Evidence.** `bin/agentiplex ... messages status <id>` (single `GET /api/v1/deliveries/{id}`) shows `caller`; the list endpoint
omits it. Operator submissions have no caller; a delegation by the PM shows `"caller":"pm"`. Only the target and the caller can read a
delivery (others get 404). A delivery record shows a delegation reached the daemon, **not which MCP tool** was used; the author of a
`note` is likewise set from the token.

**Verified without a model** (real daemon built from the repository, repository-team `team.yaml` edited as above, simulated agents):
`agents list` with the operator token and CA succeeds; no CA, another CA, `http://` (CLI: "credential requires HTTPS"), a missing token
(401) and a wrong token (401) are all rejected; a `send` with the `pm` token records `caller=pm`; `developer`/`architect` tokens get 404 for it.
Automated boundaries: `TestPerAgentIdentityIsolation`, `TestSecureHTTPRolesA2AAndMCP` in [tests/integration](../tests/integration).

**Limitations.** Per-agent authentication is not OS isolation: gateways share one OS user and workspace, and an agent with shell access
can read its own token from its environment. Tokens are read at startup; restart after rotation. Free-model runs are
weak evidence of collaboration; review actual results.

## Private repositories
- Pass `--pass-env <PROVIDER>_API_KEY` to the gateway and select an approved `model` in `opencode.json`. Only named
  variables are forwarded; `gh` login and OpenCode auth are not.
- Never point free models at private code: free-model providers may retain prompts.
- Clone the repository yourself into the disposable workspace (the gateways have no GitHub credentials).

## Collaboration model
- Agents collaborate through the MCP tools of the daemon. The coordinator (PM) discovers the team and delegates with the tools it is
  granted (typically the blocking `ask`); mechanics such as idempotency keys and re-attaching are in the tool contract, not the prompt. Findings are shared with `note`/`read_notes`.
- Each team is the `registry` block of its `team.yaml` (agents, groups, pools) plus one native runtime config per role and
  static gateway policy files. A new member is one more registry entry and one more native runtime.
- **Restart expectation:** if the daemon is killed (even SIGKILL) mid-run and restarted on the same storage and port, the run
  continues: the PM's failed `ask` is repeated and re-attaches to the same delivery, with no duplicate downstream
  execution. This relies on the restart policy of [DEPLOYMENT](../docs/DEPLOYMENT.md) (#31) or an operator restart.
  A model turn that was in flight in an agent is not resumed.
- Automated verification (not the normal usage path) lives in [tests/examples](../tests/examples): `make examples-check`,
  `make repository-provision-check`, `make live-check` (inference with a free model).

## Known issue: flaky startup preflight
The gateway checks the private policy with `opencode --standalone debug agents`. OpenCode
intermittently omits the policy, and the gateway retries only 3 times. Measured probe pass
rates: 5/12 on 2.3.2 with a reused data dir, 1/12 with a fresh one, 3/12 on 2.4.1. Roughly 30% of
launches pass on 2.3.2.

The error "capability policy was dropped, overridden or weakened by configuration" is therefore
often not a configuration problem. Retry the launch before changing configuration.

## Operating tips
- Gateway startup takes a minute or more (preflight). Run each process in its own terminal or detached; do not poll.
- Agents are asked not to commit or push. Review the workspace diff, then open the PR yourself.
- JetStream needs free disk (about 560 MB reserved): point `broker.storage` in `team.yaml` at a roomier or in-memory filesystem if needed.
- Stop only the processes you started, by PID; delete the run directory afterwards.

Guides: [repository-team](repository-team/README.md), [autonomous-team](autonomous-team/README.md).

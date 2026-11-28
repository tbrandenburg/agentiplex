# Examples: running real OpenCode agents

New here? Start with [three-agent discussion](discussion-team/README.md), a read-only topic discussion with no launcher script. The sections below describe the larger engineering examples.

Applies to `repository-team` and `autonomous-team`. These start real, partly
full-access OpenCode agents with their **native commands**; each example has one `team.yaml` that is passed unchanged to
`agentiplexd --config team.yaml`. The prompted repository-team starts MCP first and registers live gateways with native CLI commands; autonomous-team uses declarative registry provisioning. No orchestration layer is needed: follow the example README. The core middleware does not depend on any of this.
Ports are fixed loopback ports (daemon 19080, agents 19081 and up); both examples use 19080, so run one at a time and check `ss -ltn` first.
Trusted-local means unauthenticated loopback HTTP; for secure operation use an operator-owned copy of `team.yaml`.

Quick start from the repository root: use the example's `up.sh` ([repository-team](repository-team/README.md#quick-start) or
[autonomous-team](autonomous-team/README.md#quick-start)), submit its objective using the native `bin/agentiplex` CLI, and stop
with `bash examples/down.sh <run-dir>` (the directory printed as `RUN=...`). The scripts only run the documented native commands
in the background; prompts are plain files (`roles/`, `objective.md`, `profiles/`). The repository-team launcher still requires
explicit `ACCEPT_FULL_ACCESS=yes` and a disposable Git workspace. The former six Makefile lifecycle targets are removed.

## Setup
- Go 1.26+, Git, Make, Node/npm, and Bun 1.4.4 (the gateway needs `bun` on PATH). The acceptance tests use the same Go toolchain; live verification requires explicit opt-in.
- Use `opencode` 2.4.1 and gateway 0.9.2, the upstream verified native-role pair; both and `bun` must be on PATH:
  ```sh
  npm install --global @opencode/cli@2.4.1 @tbrandenburg/opencode-a2a-lite@0.9.2
  ```
  (`npx @tbrandenburg/opencode-a2a-lite@0.9.2 ...` also works, but stopping the npx wrapper can orphan the real gateway.)
- Gateway policy files (`--policy-file`) must be outside the workspace, owned by you and neither they nor any ancestor
  directory may be group/world-writable. If your checkout or umask makes them group-writable, use private copies as the READMEs do.
  See the upstream [native role evidence](https://github.com/tbrandenburg/opencode-a2a-lite/blob/v0.9.2/docs/native-roles.md) for verified versions.

## Secure mode (verified HTTPS + per-agent tokens)

> **Status: secure real-team acceptance remains open (#41).** Earlier gateway 0.9.0 could not forward private-CA trust; gateway 0.9.1 added operator-controlled `NODE_EXTRA_CA_CERTS` forwarding, also present in 0.9.2. The earlier real PM + worker authentication was reproduced with a locally patched gateway only; the released gateway still needs a secure end-to-end run. Daemon-side authentication and TLS were verified independently.

Trusted-local HTTP (the default of these examples) stays separate: secure mode never falls back to HTTP, there is no
skip-verify, and an untrusted certificate or a wrong/missing token is rejected.

**Operator prerequisites (all files outside the repository, mode 0600/0700, never committed).** One private CA, a server
certificate for the daemon (SAN `localhost`/`127.0.0.1` or your host name, `serverAuth`), one distinct 32-512 character token
file each for `operator`, `viewer` (dashboard) and every agent. [scripts/demo-secrets.sh](../scripts/demo-secrets.sh) shows the
openssl/`openssl rand -hex 32` steps used by the repo's tests; field semantics are in
[DEPLOYMENT](../docs/DEPLOYMENT.md#static-credentials-and-tls).

**Edit an operator-owned copy of `team.yaml`** (the committed file stays unchanged). For autonomous-team, use the static registry credential fields shown below. For the prompted repository-team, keep its empty declarative roster and put all five agent IDs and token-file paths in `security.agent_token_files` instead; start the HTTPS daemon first, start the prompted gateways, and register the endpoints with the authenticated operator CLI. Credential identity and endpoint registration are independent. Do not configure both credential fields for the same ID. The released-gateway authenticated real-team run remains unverified (#41).
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

For repository-team use this alternative credential mapping rather than `registry.agents`:
```yaml
security:
  operator_token_file: /secure/operator
  viewer_token_file: /secure/viewer
  agent_token_files:
    pm: /secure/pm
    architect: /secure/architect
    developer: /secure/developer
    tester: /secure/tester
    quality: /secure/quality
```

**Shared `opencode.json`**: change the MCP entry to `"url": "https://localhost:19080/mcp"` and add
`"oauth": false` and `"headers": {"Authorization": "Bearer {env:AGENTIPLEX_TOKEN}"}`. No token is in the file. Give **each
gateway process its own token from its file** and forward only that name, e.g. for `pm`:
```sh
AGENTIPLEX_TOKEN=$(cat /secure/pm) NODE_EXTRA_CA_CERTS=/secure/ca.crt A2A_NAME=pm ... opencode-a2a-lite ... --pass-env AGENTIPLEX_TOKEN
```
(Use an absolute trusted CA path; skip it with a system-trusted CA.) Never pass the
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
- Team membership comes from native CLI registrations (repository-team) or the `registry` block of `team.yaml` (autonomous-team), plus native runtime configuration and static gateway policies. Repository-team uses standalone role files passed as actual system prompts; autonomous-team uses native prepared profiles. A new member is one more registry entry and one more native runtime.
- **Restart expectation:** if the daemon is killed (even SIGKILL) mid-run and restarted on the same storage and port, the run
  continues: the PM's failed `ask` is repeated and re-attaches to the same delivery, with no duplicate downstream
  execution. This relies on the restart policy of [DEPLOYMENT](../docs/DEPLOYMENT.md) (#31) or an operator restart.
  A model turn that was in flight in an agent is not resumed.
- Automated verification (not the normal usage path) lives in [tests/examples](../tests/examples): `make examples-check`,
  `make repository-provision-check`, `make live-check` (inference with a free model).

## Known issue: flaky startup preflight
The gateway checks the private policy with `opencode --standalone debug agents`. OpenCode
intermittently omits the policy, and the gateway retries within a bounded startup budget (improved in 0.9.1 and 0.9.2). Historical 0.9.0 probe pass
rates: 5/12 on 2.3.2 with a reused data dir, 1/12 with a fresh one, 3/12 on 2.4.1. Roughly 30% of
launches pass on 2.3.2.

The error "capability policy was dropped, overridden or weakened by configuration" is therefore
often not a configuration problem. Retry the launch before changing configuration.

## Operating tips
- Gateway startup takes a minute or more (preflight). Run each process in its own terminal or detached; do not poll.
- The repository-team defaults to an uncommitted patch; an explicit objective may authorize local integration in a disposable repository. Remote publication remains forbidden. Review the actual diff/history, then open the GitHub PR yourself. Autonomous-team stays read-only.
- JetStream needs free disk (about 560 MB reserved): point `broker.storage` in `team.yaml` at a roomier or in-memory filesystem if needed.
- Stop only the processes you started, by PID; delete the run directory afterwards.

Guides: [repository-team](repository-team/README.md), [autonomous-team](autonomous-team/README.md).

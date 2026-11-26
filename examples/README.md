# Examples: running real OpenCode agents

Applies to `repository-team` and `autonomous-team`. These start real, partly
full-access OpenCode agents. The core middleware does not depend on any of this.

## Setup
- Go 1.26+, Python 3.10+, Git, Make, Node/npm, and Bun 1.4.4 (the gateway needs `bun` on PATH).
- Install the pinned tools (the gateway is the scoped package, not `opencode-a2a-lite`):
  ```sh
  npm install --global @opencode/cli@2.3.2 @tbrandenburg/opencode-a2a-lite@0.9.0
  ```
  Or use `npm install --prefix <dir>` and prepend `<dir>/node_modules/.bin` to PATH.
  "OpenCode 2.3.2 or newer" is accepted, but 2.4.1 is no more reliable than 2.3.2 (see below).

## Private repositories
- Pass `--pass-env <PROVIDER>_API_KEY` and `MODEL=provider/model`. Only `*_API_KEY` variables
  are forwarded; `gh` login and OpenCode auth are not.
- Never use `--public-workspace` for private code (it attests the code may go to free models).
- Clone first and pass the local path as `REPO=/abs/path`, because the launcher has no GitHub credentials.

## Known issue: flaky startup preflight
The gateway checks the private policy with `opencode --standalone debug agents`. OpenCode
intermittently omits the policy, and the gateway retries only 3 times. Measured probe pass
rates: 5/12 on 2.3.2 with a reused data dir, 1/12 with a fresh one, 3/12 on 2.4.1. Roughly 30% of
launches pass on 2.3.2.

The error "capability policy was dropped, overridden or weakened by configuration" is therefore
often not a configuration problem. Retry the launch before changing configuration.

## Operating tips
- Startup takes several minutes (`go build ./examples/...`, then the preflight). Use a long
  timeout, run in the background and do not poll.
- Agents are asked not to commit or push. Review the scratch workspace diff, then open the PR yourself.
- Evidence is kept in `/tmp/agentiplex-repository-*`. Delete it after the processes have stopped.
- Stop only the processes you started, by PID.

Guides: [repository-team](repository-team/README.md), [autonomous-team](autonomous-team/README.md).

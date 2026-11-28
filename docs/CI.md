# CI workflows

The Actions sidebar uses the following categories. Workflow filenames and existing job IDs remain stable so links, badges and branch protection are not accidentally broken.

| Workflow | Category | Coverage | Trigger / local equivalent |
| --- | --- | --- | --- |
| [Checks](../.github/workflows/ci.yaml) | Required, provider-free | Build, formatting, unit/race tests, integration, example configuration and durable continuation regressions | PR / main; `make check`, `make examples-check`, `make contract-check` |
| [Examples / Live / Three-agent quickstart](../.github/workflows/discussion-quickstart.yaml) | Live examples | README setup, ordinary discussion, native until-done and actor-owned read-only contracts | Relevant PR / main / manual |
| [Examples / Live / Hybrid autonomous team](../.github/workflows/live-opencode.yaml) | Live examples | Shared provisioning and autonomous hybrid collaboration | Relevant PR / main / manual; `make live-check` |
| [Examples / Live / Repository engineering team](../.github/workflows/repository-team-live.yaml) | Live examples | Five real agents on synthetic repositories; docs, defect, integration and contract scenarios | Relevant PR / manual |
| [Release](../.github/workflows/release.yaml) | Publication | Tagged, checked native binaries, Helm packages, OCI image and GitHub release | Version tags only |

Use `Examples / Live / <scenario>` for model-dependent example workflows. Keep deterministic regressions in Checks, not behind model availability. Do not rename required status contexts (including Checks / test) without coordinating repository protection rules.

## Contract verification

`make contract-check` exercises native until-done lifecycle tests with the real embedded broker and deterministic A2A agents, independent of model availability.

The three-agent quickstart also runs the documented until-done command with the same three agents, checks a recorded incomplete checkpoint and distinct revised review evidence, then checks explicit resolution. No inactivity nudge or orchestration script is required.

The repository engineering team live matrix includes `contract`. It executes the README's actual approval and native send commands with five native-role agents, observes the first failed-verification checkpoint, guards the approved checklist and objective in the test observer, requires distinct owner evidence and multiple completed PM activations without inactivity nudging, then independently verifies the actual patch. The executable workers have no MCP exceptions; the read-only coordinator delegates with blocking ask only. A second invocation must not schedule more work after closure. It uses synthetic inputs and the same credential-free runtime as the other live examples; it is not deterministic and provider availability can affect the outcome.

For commands and operational limits, see [contract-driven mode](../examples/repository-team/README.md#contract-driven-mode-opt-in).

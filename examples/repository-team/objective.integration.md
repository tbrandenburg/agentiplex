# Repair and integrate a failing check

The local repository is disposable, has no remote, and starts on main. Fix the defective Clamp(value, lower, upper int) int in calc.go for lower <= upper, preserving its signature. The existing command go test -count=1 ./... must pass; do not modify that check or unrelated files.

Acceptance criteria:
- [ ] The developer commits the fix on a feature branch and reports the candidate commit. Keep the candidate branch for inspection.
- [ ] The `tester` agent independently runs the original check against that exact candidate commit and records the passing result in topic `tester`.
- [ ] The separate `quality` agent independently reviews the actual candidate diff against the original base and records its acceptance or blockers in topic `quality`. An architect's assessment, the developer's self-review or the tester's passing result cannot substitute for this quality gate.
- [ ] Before authorizing integration, the PM must obtain both completed deliveries (tester and quality) and their evidence for the same candidate commit. If the candidate changes or either gate fails, request corrections and repeat both gates.
- [ ] The reviewed candidate is integrated into the original main with a two-parent (non-fast-forward) merge commit.
- [ ] The tester verifies the integrated revision with the same check; main passes it and the working tree is clean.
- [ ] Record test results, review findings and integration evidence with the team, including actual tester and quality delivery IDs. Before returning, assess every checklist item and explicitly report any unmet criterion; do not treat a completed delivery or a successful merge as contract fulfillment.

This objective authorizes local Git branch creation and switching, staging, commits and merging into main only in this disposable repository. No remote, push, credentials, history rewriting or unrelated changes.

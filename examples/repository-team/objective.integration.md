# Repair and integrate a failing check

The local repository is disposable, has no remote, and starts on main. Fix the defective Clamp(value, lower, upper int) int in calc.go for lower <= upper, preserving its signature. The existing command go test -count=1 ./... must pass; do not modify that check or unrelated files.

Acceptance criteria:
- The fix is committed on a feature branch, independently tested and reviewed before integration. Keep the candidate branch for inspection.
- The reviewed candidate is integrated into the original main with a two-parent (non-fast-forward) merge commit.
- The tester verifies the integrated revision with the same check; main passes it and the working tree is clean.
- Record test results, review findings and integration evidence with the team.

This objective authorizes local Git branch creation and switching, staging, commits and merging into main only in this disposable repository. No remote, push, credentials, history rewriting or unrelated changes.

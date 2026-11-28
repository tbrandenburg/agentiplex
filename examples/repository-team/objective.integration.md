# Repair and integrate a failing check

The local repository is disposable, has no remote, and starts on main. Fix the defective clamp(value, lower, upper) in calc.py for lower <= upper, preserving its signature. The existing command python3 -B test_calc.py must pass; do not modify that check or unrelated files.

Acceptance criteria:
- The fix is committed on a feature branch, independently tested and reviewed before integration. Keep the candidate branch for inspection.
- The reviewed candidate is integrated into the original main with a two-parent (non-fast-forward) merge commit.
- The tester verifies the integrated revision with the same check; main passes it and the working tree is clean.
- Record test results, review findings and integration evidence with the team.

This objective authorizes local Git branch creation and switching, staging, commits and merging into main only in this disposable repository. No remote, push, credentials, history rewriting or unrelated changes.

# Repair the calculator under a living contract

Fix Clamp(value, lower, upper int) int in calc.go for lower <= upper so it returns the value bounded by the inclusive limits. Preserve its signature. Leave the patch uncommitted; no branch or history changes. Do not edit any other files or weaken the tests. The original go test -count=1 ./... must pass.

The approved checklist is stored in repository/contract. Its fixed criteria are:

| ID | Owner | Description |
| --- | --- | --- |
| baseline | tester | Reproduce the existing Clamp regression before repair |
| implementation | developer | Repair Clamp without changing unrelated files or tests |
| tests | tester | Independently verify the repaired implementation with the original tests |
| quality | quality | Independently accept the actual diff and scope |

The ID, owner and description are immutable for this approved request. First independently reproduce the failing test before repair. The developer repairs, the tester independently checks the repaired workspace, and quality reviews the actual diff. Evidence and delivery IDs are required for every criterion. A failed verification requires rework, not closure.

The operator authorizes only this local patch in a disposable repository. No publication or credentials.

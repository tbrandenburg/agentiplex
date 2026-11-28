# Repair the calculator through actor-owned artifacts

Fix Clamp(value, lower, upper int) int in calc.go for lower <= upper so it returns the value bounded by the inclusive limits. Preserve its signature. Leave the patch uncommitted; no branch or history changes. Do not edit any other files or weaken the tests. The original go test -count=1 ./... must pass.

The approved living contract is repository/actors-contract. The four immutable artifact IDs, owners and descriptions are:

| ID | Owner | Description |
| --- | --- | --- |
| baseline | tester | Reproduce the existing Clamp regression before repair |
| implementation | developer | Repair Clamp without changing unrelated files or tests |
| tests | tester | Independently verify the repaired implementation with the original tests |
| quality | quality | Independently accept the actual diff and scope |

The initial responsible actor is pm. Each artifact owner owns its evidence and status; current responsibility is the exclusive duty to advance the contract, not authority to approve another actor's artifact. The baseline must precede repair. Verification and quality review must refer to the current implementation; they may occur in either useful order once a candidate exists. Rejected work requires correction and fresh evidence. The PM closes the approved outcome only when the evidence is complete. Unrelated roles and unnecessary work are not required.

Use ordinary direct messages for notifications after persisting changes to the contract. The living contract, not a workflow script or an initial agent, determines who can make progress next. The operator independently verifies the actual patch. Only this local patch in a disposable repository is authorized. No publication or credentials.

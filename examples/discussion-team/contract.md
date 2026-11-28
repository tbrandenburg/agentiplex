# Persistence architecture decision

Produce a concise decision for a synthetic read-heavy service that needs
read-your-writes consistency. No external research or repository changes.

## Acceptance checklist
- Compare event sourcing and a transactional relational database.
- Explain the consistency trade-offs.
- Explain failure recovery.
- Obtain independent critical review and address every blocking finding.
- Return the recommendation with the actual expert and critic delivery IDs.

## Demonstration checkpoint
The first activation intentionally produces an incomplete draft: ask the expert
for a recommendation and alternatives without failure recovery, then ask the
critic to assess it against the entire acceptance checklist. The critic must
identify the missing recovery analysis. Record the observed incomplete result,
then return from this activation without resolving the request. This is a
synthetic demonstration of an intermediate checkpoint, not a workflow rule
imposed by the middleware.

Use shared state namespace discussion-contract for this example. Create key
baseline (revision zero) as a JSON document with draftDelivery, reviewDelivery,
and finding (the critic's actual missing-recovery finding). Do not change this
checkpoint after recording it. On subsequent activations read it, obtain a
revised expert analysis and independent critic review; if the critic identifies
remaining blockers, continue correcting them within the execution budget.
After acceptance, create key final with draftDelivery, reviewDelivery,
recommendation (include failure recovery) and accepted (true only after the
actual critic accepts). Record supporting findings and use distinct stable
keys for new delegations. Resolve the request only with the verified result.

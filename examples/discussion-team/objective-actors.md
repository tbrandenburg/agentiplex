# Actor-owned decision contract

Recommend a persistence architecture for a synthetic read-heavy service requiring
read-your-writes consistency. Compare event sourcing and a transactional relational
database; explain consistency trade-offs and failure recovery. The expert owns the
draft, the critic owns independent review, and the lead owns acceptance of the
approved objective. Work from the unchecked acceptance criteria in the living Markdown contract.
Only the current responsible actor may advance the living
contract at discussion-actors/contract. Responsibility may move directly between
actors; the lead need not relay their work.

The five immutable acceptance criteria (including their artifact owners) are:

- Compare event sourcing and a transactional relational database (expert)
- Explain consistency trade-offs (expert)
- Explain failure recovery (expert)
- Independent review of current draft (critic)
- Final acceptance (lead)

For this demonstration the initial expert draft intentionally omits recovery.
The critic must identify this specific missing criterion and return responsibility
to the expert, who corrects it before another independent review. The lead closes
only when the current draft has an accepted review and all five boxes are checked.
A checked box requires corresponding evidence; review rejection is not acceptance.
No external research, file edits or unrelated work. Use ordinary asynchronous
messages; the shared contract is the authority for progress and responsibility.

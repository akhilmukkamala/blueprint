---
status: template     # this file is the blank form; copies use proposed | accepted | superseded
---

# ADR-0000: <decision title>

- date: <YYYY-MM-DD>
- status: proposed | accepted | superseded by ADR-NNNN

## Decision

_One paragraph, stated as a decision made — "We use X", not "we could use X"._

## Why

_The forces: the constraint that forced a choice, the alternatives considered,
and the reason each losing alternative lost. This section is the part future
readers need._

## Consequences

_What becomes easier, what becomes harder, what we accept living with._

---

To record a decision: copy this file to `ADR-NNNN-<slug>.md` (next unused
number), fill it in, keep it to one page, and give it frontmatter of its own:

```
---
reviewed: <YYYY-MM-DD>
owner: <name>
status: proposed
---
```

Set `status: accepted` once the decision stands — accepted ADRs are history,
exempt from the freshness lint. Never edit an accepted ADR to change the
decision; write a new ADR that supersedes it.

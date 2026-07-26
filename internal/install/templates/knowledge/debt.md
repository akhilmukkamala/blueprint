---
reviewed:            # YYYY-MM-DD — set when a human curates this file (knowledge lint enforces freshness, max-age 90 days)
owner:               # who answers questions about this tracker
status: draft
---

# Tech debt

One entry per debt item. Every entry carries a ratchet baseline: a measured
number that may only go down, with a pointer to where the number is enforced
(a verifier threshold file, or the adoption baselines in
`.blueprint/baselines.json`).

## <debt item title>

- what: _the shortcut and where it lives (files/modules)_
- why it stands: _the constraint that made it the right call when taken_
- ratchet baseline: _metric = value, enforced at <threshold-file or check>_
- exit: _the condition under which this entry is deleted_

A decision behind long-lived debt deserves a one-page ADR: copy
[decisions/ADR-0000-template.md](decisions/ADR-0000-template.md).

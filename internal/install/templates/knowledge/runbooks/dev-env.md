---
reviewed:            # YYYY-MM-DD — set when a human curates this file (knowledge lint enforces freshness, max-age 180 days)
owner:               # who answers questions about dev setup
status: draft
---

# Dev environment — run it first

The first fenced block below is executable documentation: `blueprint doctor
--run-dev-env` executes it verbatim, so it must stay a copy-pasteable sequence
that takes a fresh clone to a working dev environment.

```sh
# replace with the real sequence, one command per line
make setup
make test
```

When setup drifts, fix this block in the same change that caused the drift — a
runbook that lies is worse than no runbook.

Running agents air-gapped? Work through the [no-egress checklist](no-egress.md).

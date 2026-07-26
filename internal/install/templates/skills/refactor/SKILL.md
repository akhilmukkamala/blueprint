---
name: refactor
description: Blueprint refactor playbook — characterization tests pin current behavior, then restructure with the suite unchanged.
---

# Refactor

1. Route it: `blueprint new "refactor: <what>" --type refactor`. The contract declares `behavior = "preserved"` — no observable behavior change.
2. Before touching code, pin current behavior with characterization tests (golden/approval tests of today's actual outputs, bugs included). If coverage already characterizes the area, note which tests are the contract.
3. `blueprint approve <id>` — approved.lock hashes those tests; editing them mid-refactor fails TAMPER.
4. Refactor in small steps, keeping the suite green the whole way (the `behavior-preserved` verifier runs your repo test command unchanged).
5. `blueprint verify <id>`; optionally enable the `diff-cover` verifier in .blueprint/verifiers.toml to require touched lines be covered. `blueprint close <id>` when green.

If a test must change, that is a behavior change — route it as its own change instead of bending the refactor.

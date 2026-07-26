---
name: chore
description: Blueprint chore playbook — registry-matched exempt changes with linters and the suite still green.
---

# Chore

1. Route it: `blueprint new "chore: <task>" --type chore` (docs/style/build/ci types land here too). Registry-matched chores are exempt: no spec, no contract, one worklog line.
2. Check the match: `blueprint registry list` shows the pre-approved classes (docs-only, formatting-only, dep-patch-bump by default). If the router did not exempt, the diff exceeded a class ceiling — keep the chore pure or accept the light tier.
3. Exempt is not unverified: declared checks and linters still run (`tests-pass` covers chores; add your linter via the `lint-clean` example in .blueprint/verifiers.toml).
4. Recurring chore shape? Add a [[class]] to .blueprint/registry.toml — expanding the registry is itself a reviewed, human-gated change.

Never mix a behavior change into a chore to ride the exempt lane; the glob allowlists exist to make that impossible.

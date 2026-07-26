---
name: feature
description: Blueprint feature playbook — spec the requirements, trace tests to REQ IDs, verify before merge.
---

# Feature

1. Route it: `blueprint new "<intent>" --type feat`. Light tier gets a change.md contract; full tier adds design + tasks (add `--worktree` for an isolated agent worktree).
2. Write the delta spec: EARS-style requirements with stable `REQ-<area>-NNN` IDs in the change contract; `blueprint lint spec` bans vague words and placeholder tasks.
3. `blueprint approve <id>` to lock the contract, then implement task by task, writing tests that carry trace annotations back to their REQ IDs (`blueprint lint trace` checks coverage).
4. Keep the suite green — the `tests-pass` verifier runs your repo test command from [test] in config.toml or auto-detection.
5. `blueprint verify <id>` — lint, tamper, verifiers, and (full tier) the fresh-context model checker. `blueprint close <id>` when green.

One change = one contract file; if the feature grows, split it rather than letting change.md sprawl.

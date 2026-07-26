---
name: bug-fix
description: Blueprint bug-fix playbook — red regression test first, then fix, then verify red→green plus the full suite.
---

# Bug fix

1. Route it: `blueprint new "fix: <symptom>" --type fix` (usually light tier; a light change.md is scaffolded).
2. Write the failing regression test FIRST and run it — confirm it is red. The red test is the spec of the bug; no fix lands without one.
3. Reference the test from the change contract (trace annotation), then `blueprint approve <id>` so approved.lock pins it — weakening it later fails TAMPER.
4. Fix the code until the regression test and the whole suite are green (the `tests-pass` verifier runs your repo test command).
5. `blueprint verify <id>` — spec/trace lint, tamper check, then verifiers. Iterate until green; `blueprint close <id>` when done.

Never delete or skip the regression test to get green — verify counts spec-linked tests and hunts skip markers.

# ADOPT.md — bringing Blueprint into an existing repo

Five stages. Each stage ends at a machine exit check
(`blueprint doctor --adopt-stage <n>`), each has a worst-case duration cap, and
**you can stop after any stage and keep everything you built** (see
[Stop-anywhere guarantee](#stop-anywhere-guarantee)). The FDE ships at stage 3.
Stage 4 is week-1+ work.

Prerequisites: the binary installed ([INSTALL.md](INSTALL.md)) and a git repo.
For your first end-to-end change after adoption, follow
[QUICKSTART.md](QUICKSTART.md).

**Fast path (the AC-1 measurement protocol):** stages 0–3 back-to-back with
stage-1 curation **timeboxed to 15 minutes** (a quick-pass prune; full curation
continues after first value), targeted at **≤60 minutes total** from
`blueprint adopt` to a verifier-green merged change.

| Stage | Goal | Worst-case cap | Exit check |
|---|---|---|---|
| 0 | Import + baselines | minutes | `blueprint doctor --adopt-stage 0` |
| 1 | Orient (curated index) | 15 min fast path; ≤1 h full | `blueprint doctor --adopt-stage 1` |
| 2 | Mechanical floor in CI | one CI file; hardening over day 1 | `blueprint doctor --adopt-stage 2` |
| 3 | First routed change | one real bug/chore | `blueprint doctor --adopt-stage 3` |
| 4 | Tribal capture | week 1+ | `blueprint doctor --adopt-stage 4` |

## Stage 0 — `blueprint adopt` (minutes)

Goal: install the floor, import what already exists, capture baselines.

```console
$ blueprint adopt
adopted (stage 0): floor installed, 2 file(s) imported into .blueprint/steering/
baselines (trailing 90d): 214 commits, revert rate 1.9%, fix-commit rate 11.2%
next: curate AGENTS.md to <=120 lines (stage 1), then wire `blueprint verify` into CI (stage 2)
```

What it did: imported existing `CLAUDE.md` / `.cursor/rules` into
`.blueprint/steering/` drafts, probed the forge, and wrote
`.blueprint/baselines.json` — the framework-off task-timing protocol plus
git-derived trailing-90-day rework/revert rates. Every later metric divides by
these same-repo numbers. Commit the result (`git add -A && git commit`).

Exit check — manifest + baselines exist:

```console
$ blueprint doctor --adopt-stage 0
check              pass  remediation
...
adopt-0-manifest   ok    .blueprint/manifest.json exists
adopt-0-baselines  ok    .blueprint/baselines.json exists
```

## Stage 1 — Orient (fast path 15 min; full pass ≤1 h)

Goal: a human-curated index an agent can load in one read.

1. `blueprint adopt` drafted `AGENTS.md` from the repo probe + git-churn
   hot-file mining. **Now a human curates it to ≤120 lines.** Generation is a
   draft; the curation is the named deliverable. On the fast path, timebox
   this to 15 minutes: delete what you can't vouch for, keep pointers over
   prose. Full curation continues after first value.
2. Record the one command that brings up a dev environment in
   `.blueprint/knowledge/runbooks/dev-env.md` (fenced code block required):

   ````markdown
   # Dev environment

   ```sh
   make dev
   ```
   ````
3. Sanity-read the ranked repo map agents will retrieve from: `blueprint map`.

Exit check — index lint green + dev-env runbook present (add `--run-dev-env`
to also execute the runbook command):

```console
$ blueprint doctor --adopt-stage 1
check                    pass  remediation
...
adopt-1-agents-lint      ok    AGENTS.md: 87/120 lines, all pointers resolve
adopt-1-dev-env-runbook  ok    .blueprint/knowledge/runbooks/dev-env.md exists
```

## Stage 2 — Mechanical floor (fast path: one CI file; hardening over day 1)

Goal: `blueprint verify` runs in CI as a required status check with **whatever
tests exist today**. Do not fix the backlog first — see
[Ratchet doctrine](#ratchet-doctrine). A minimal GitHub Actions job:

```yaml
# .github/workflows/blueprint.yml
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: blueprint verify $CHANGE_ID   # binary from your release cache / offline tarball
```

Mark the job required in branch protection (doctor's forge probe tells you
whether enforcement is real or advisory). Record lint/type debt counts as
ratchet baselines in `.blueprint/knowledge/debt.md`: new changes may not
increase them; nobody is asked to drain them.

Exit check — the verify command is executable as a CI predicate:

```console
$ blueprint doctor --adopt-stage 2
check                   pass  remediation
...
adopt-2-verify-command  ok    `blueprint` resolves on PATH; `blueprint verify` is executable as a predicate/CI check
```

The stage is done when a PR shows the verify status posted.

## Stage 3 — First routed change (the FDE exit point)

Goal: one real bug or chore through the full loop — `blueprint new`, EARS
delta, red test, approve, verify green, merge, `blueprint close`. This is the
AC-1 stop-clock. [QUICKSTART.md](QUICKSTART.md) walks it end-to-end with
transcripts.

Exit check — a change closed with a green verdict:

```console
$ blueprint doctor --adopt-stage 3
check                    pass  remediation
...
adopt-3-verified-change  ok    archived change 2026-07-21-fix-empty-cart-total closed with a green verdict
```

**Ship here.** The FDE leaves a repo where specs verify, CI enforces, and one
worked change proves the loop. Stage 4 belongs to week 1+.

## Stage 4 — Tribal capture (week 1+)

Goal: move what lives in heads into files that lint.

### Interview protocol

Run **2–3 structured 30-minute interviews** with the longest-tenured
engineers. One interviewer, one note-taker (or record). Ask, verbatim:

1. **Architecture boundaries** — "Draw the 3–6 boxes. Which arrows are
   forbidden? What would you never let module X import?"
2. **Deploy fears** — "What do you check by hand before every deploy? What
   broke production last time, and what would have caught it?"
3. **Domain glossary nouns** — "Which words in this codebase mean something
   non-obvious? Which two terms do new hires always confuse?"
4. **Tribal gotchas** — "What does everyone learn the hard way in month one?
   What's the thing you warn people about in code review?"

### Outputs (each answer lands in exactly one file)

- Boundaries → `.blueprint/knowledge/architecture.md` (+ `invariants/*.yml`
  ast-grep rules for forbidden imports)
- Deploy fears → `.blueprint/knowledge/runbooks/deploy.md`; each "what broke"
  becomes a characterization test pinning a top-churn module (an executable
  spec of what *is*)
- Nouns → `.blueprint/knowledge/glossary.md`
- Gotchas → one-page ADR backfills in `.blueprint/knowledge/decisions/`

Exit check — knowledge lint green with ≥1 glossary + ≥1 runbook + ≥1 ADR:

```console
$ blueprint doctor --adopt-stage 4
check              pass  remediation
...
adopt-4-glossary   ok    .blueprint/knowledge/glossary.md exists
adopt-4-runbook    ok    .blueprint/knowledge/runbooks present (e.g. dev-env.md)
adopt-4-adr        ok    .blueprint/knowledge/decisions contains at least one ADR
```

## Ratchet doctrine

**Never fix-everything-first.** Adoption records today's debt as the baseline
and forbids regression; it never blocks on cleanup. A repo with 4,000 lint
errors adopts in an hour: 4,000 becomes the ratchet, change 4,001 fails, and
the number only moves down when someone chooses to move it. Every stage above
obeys this — stage 2 wires CI around the tests that exist, stage 4 pins
current behavior with characterization tests instead of rewriting it.

## Coexistence with methodology plugins

If superpowers or a similar planning/methodology plugin is installed, keep one
source of truth: **`change.md` is the spec its plans consume.** Point the
plugin's planning step at `.blueprint/changes/<id>/change.md`; its plans dirs
are scratch output and **must not become a second source of truth** — never
copy requirements into them, and never let a plan file outlive its change.
`blueprint verify` only attests what `change.md` declares.

## Stop-anywhere guarantee

Every stage leaves the repo strictly better and nothing half-wired:

- After 0 you keep baselines + imported steering; after 1 a curated index +
  dev-env runbook; after 2 a CI verify gate; after 3 a proven loop; after 4
  durable knowledge.
- Everything is plain committed files — no daemon, no service, no network
  (`blueprint uninstall` removes tool files; `[user]` specs and knowledge stay).
- Re-running any stage's command is idempotent, and
  `blueprint doctor --adopt-stage <n>` re-verifies any stage at any time.

Greenfield instead? `blueprint init && blueprint new "first feature"` — the
first feature runs full tier once and seeds the living spec + verifier set
everything later inherits.

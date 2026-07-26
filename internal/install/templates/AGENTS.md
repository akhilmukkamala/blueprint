# Project index

<!-- This file is the canonical index: a table of contents, not an
     encyclopedia. Hard cap 120 lines (lint-enforced). Everything above the
     managed block at the bottom is yours — tooling never edits it. -->

## What this is

_One paragraph: what this system does and for whom. Replace me._

## Map

- `src/` — _main code; replace with your real layout_
- `tests/` — _how tests are organized and run_

## Commands

- build: `make build` _(replace with the real command)_
- test: `make test` _(replace with the real command)_
- dev env: _see `.blueprint/knowledge/runbooks/dev-env.md` once recorded_

## Conventions

- _House style, error-handling rules, naming — short bullets only._
- _Link out to detail; do not inline it here._

## Pointers

- Living specs: `.blueprint/specs/<area>/spec.md`
- Active changes: `.blueprint/changes/<id>/change.md`
- Steering rules: `.blueprint/steering/`
- Knowledge (ADRs, runbooks, glossary): `.blueprint/knowledge/`

<!-- blueprint:managed -->
Blueprint manages this region; put your own guidance above it.
- Route every change: `blueprint new "<intent>"` decides exempt / light / full ceremony.
- Specs are EARS requirements with stable `REQ-<area>-NNN` IDs; `blueprint lint` checks them.
- One change = one file: `.blueprint/changes/<id>/change.md` (contract + delta + tasks).
- Verify before merge: `blueprint verify` (approved.lock is the tamper-evidence stamp).
- Agent branches live under `agent/**`; `main` is never written by loops.
<!-- /blueprint:managed -->

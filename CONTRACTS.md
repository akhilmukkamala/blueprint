# CONTRACTS.md — rules for building Blueprint (binding for every contributor, human or agent)

The commission's DESIGN.md is the specification (it lives in the commission
workspace, not in this repo). This file is the engineering contract that keeps
parallel work mergeable.

## Package map & ownership
- `internal/core` — shared types ONLY, stdlib-only, imports no sibling package.
  Extending it is allowed (add types), changing existing exported types needs a
  note in your handoff summary.
- `internal/<feature>` — one feature package per DESIGN section; a feature package
  may import `core` and other feature packages listed in its brief, nothing else.
- `internal/cli/<feature>.go` — each feature registers its cobra command(s) in its
  OWN file via `cli.Register(...)` in `init()`. Never edit registry.go, main.go,
  or another feature's cli file.
- `tools/` — repo-quality tooling (license check etc.), not shipped.

## Hard rules
1. **No new module dependencies.** go.mod is frozen; the allowed set is: cobra,
   BurntSushi/toml, yaml.v3, tree-sitter bindings (go-tree-sitter + grammar
   bindings). If you believe you need another dep, STOP and report it in your
   handoff — do not `go get`.
2. **Every command honors `--json`** (structured output to stdout, human text
   otherwise) and returns non-zero on failure. Error messages are remediation
   instructions (linters-that-teach).
3. **Tests accompany every package** (`go test ./...` green before handoff);
   table-driven; fixtures under `internal/<feature>/testdata/`.
4. **No network calls** anywhere except the explicitly opt-in commands listed
   in INSTALL.md's network disclosure (AC-12). Everything else must work
   air-gapped.
5. **Determinism**: linters/router/verify are pure functions of the repo state;
   no wall-clock dependence except explicit timestamps written to journals.
6. **Windows-clean**: no symlinks, filepath.Join everywhere, no POSIX-only shell
   assumptions in required paths.
7. **Journals are append-only ndjson** of `core.JournalEvent`; never rewrite.
8. Comment style: explain constraints and invariants, not narration.

## Merge protocol
Feature work happens on `agent/<feature>` branches in worktrees; the integrator
merges to `main` and resolves go.sum/wiring. Handoff = branch name + summary of
public API + any core type changes + test evidence (`go test ./...` output).

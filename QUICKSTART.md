# QUICKSTART.md — an FDE's first session

The first working session in a customer repo, end to end: install, adopt,
orient, one verified change, parallel worktrees, and MCP wiring. Every
transcript below is real output (repo paths shortened). The staged adoption
playbook around this session is [ADOPT.md](ADOPT.md).

## 1. Install (one command)

macOS / Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/akhilmukkamala/blueprint/main/dist/install.sh | sh
```

Windows (PowerShell): `powershell -ExecutionPolicy Bypass -File install.ps1`
— or `brew install blueprint` / `scoop install blueprint` /
`winget install AkhilMukkamala.Blueprint`.

Air-gapped: transfer `blueprint-offline-<version>-<os>-<arch>.tar.gz`, verify
its sha256 against `checksums.txt`, extract onto PATH. Templates are embedded;
nothing is fetched. Details, upgrades, rollback: [INSTALL.md](INSTALL.md).

Check: `blueprint version` prints the version and platform.

## 2. Adopt the repo

```console
$ cd ~/code/acme && blueprint adopt
adopted (stage 0): floor installed, 2 file(s) imported into .blueprint/steering/
baselines (trailing 90d): 214 commits, revert rate 1.9%, fix-commit rate 11.2%
next: curate AGENTS.md to <=120 lines (stage 1), then wire `blueprint verify` into CI (stage 2)
```

Commit the floor, then confirm: `blueprint doctor --adopt-stage 0`.
**You ship here:** `.blueprint/` + baselines the customer's metrics will
divide by forever.

## 3. Orient

Curate the drafted `AGENTS.md` to ≤120 lines (15-minute timebox on the fast
path — see [ADOPT.md](ADOPT.md) stage 1), record the dev-env command in
`.blueprint/knowledge/runbooks/dev-env.md`, then load the ranked repo map:

```console
$ blueprint map
pkg/greet.go: Greet
AGENTS.md
README.md
go.mod
pkg/greet_test.go: TestGreetEmpty
CLAUDE.md
6 files · ~24 tokens · cache /Users/fde/code/acme/.blueprint/map.json
```

**You ship here:** a curated index (`blueprint doctor --adopt-stage 1` green).

## 4. First change, end to end

### 4a. Route it

```console
$ blueprint new "fix greeting for empty name"
tier: light  (type=feat)
  [default → light] start light and escalate on evidence
  [registry] no standard-change class matches
  [blast-radius] radius 11 < threshold 20
  [loc] changed LOC 0 < escalate_loc 400
  [reversibility] no one-way (schema/data/API/money) paths touched
  [sensitive] no sensitive paths touched
projected ceremony (light): change.md (1–3 EARS deltas + loop contract) + regression test red→green — est. ~25k tokens
change: /Users/fde/code/acme/.blueprint/changes/2026-07-21-fix-greeting-for-empty-name/change.md (draft)
playbook: .blueprint/skills/feature/SKILL.md (materialized on first use of this scenario)
```

### 4b. Write the spec delta

Append the EARS delta with its `verify:` block to the drafted `change.md`
(the frontmatter above it — loop caps, breaker, boundaries — is already
scaffolded):

```markdown
## Delta

### ADDED REQ-greet-001 (event-driven)
WHEN Greet is called with an empty name, the system SHALL return "hello stranger".

verify:
- test: TestGreetEmpty
```

### 4c. Write the red test

The `// verifies:` annotation is what the traceability linter matches against
the REQ ID:

```go
// pkg/greet_test.go
// verifies: REQ-greet-001
func TestGreetEmpty(t *testing.T) {
	if got := Greet(""); got != "hello stranger" {
		t.Fatalf(`Greet("") = %q, want "hello stranger"`, got)
	}
}
```

Confirm it is red: `go test ./pkg/ -run TestGreetEmpty` → `FAIL`.

### 4d. Approve (the human gate)

```console
$ blueprint approve 2026-07-21-fix-greeting-for-empty-name
change 2026-07-21-fix-greeting-for-empty-name approved: 4 file(s) locked, 1 spec-linked test(s), composite cbbd98b70a90...
```

Spec and tests are now hash-locked (`approved.lock`); silent edits to either
fail the tamper check. Commit the spec + red test now — verify diffs against
HEAD, and an uncommitted pile can escalate the tier mid-flight.

### 4e. Verify — red, on purpose

```console
$ blueprint verify 2026-07-21-fix-greeting-for-empty-name
[PASS] spec-lint
[PASS] trace-lint
[PASS] tamper
[PASS] backfill-guard
[FAIL] tests-pass
       verifier "tests-pass" exited 1; read its report at .blueprint/changes/2026-07-21-fix-greeting-for-empty-name/verdict/tests-pass.report.txt and fix the findings, or adjust .blueprint/config.toml if the check is misconfigured
[PASS] model-checker
       skipped: tier is "light"; the model checker runs on full-tier changes only (deterministic checks are the gate here)
fingerprint: 965351f117894ba3c2f89f0daa63167b76149d06504e8dac0a9ea2a44a600670
blueprint: verify 2026-07-21-fix-greeting-for-empty-name: failing checks remain; fix the findings above and re-run `blueprint verify 2026-07-21-fix-greeting-for-empty-name`
```

This command is the loop predicate in `change.md` — the maker agent (Claude
Code, Cursor, Codex) iterates until it exits 0, within the scaffolded caps.

### 4f. Implement, verify green

Make `Greet("")` return `"hello stranger"` (or let the maker do it), then:

```console
$ blueprint verify 2026-07-21-fix-greeting-for-empty-name
[PASS] spec-lint
[PASS] trace-lint
[PASS] tamper
[PASS] backfill-guard
[PASS] tests-pass
       test command "go test ./..." resolved from go.mod (auto-detected)
[PASS] model-checker
       skipped: tier is "light"; the model checker runs on full-tier changes only (deterministic checks are the gate here)
fingerprint: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

Exit code 0. Completion was never self-declared.

### 4g. Close

```console
$ blueprint close 2026-07-21-fix-greeting-for-empty-name
closed 2026-07-21-fix-greeting-for-empty-name: delta merged into living specs; folder archived at .blueprint/archive/2026-07-21-fix-greeting-for-empty-name
```

**You ship here:** the AC-1 finish line — a verifier-green merged change,
REQ-greet-001 living in `.blueprint/specs/`, the whole journey journaled.
`blueprint doctor --adopt-stage 3` is green; `blueprint metrics` now reports
`time_to_first_verified_change`. Interrupted mid-loop? `blueprint status`
shows caps/breaker and `blueprint resume <id>` prints the cold-start context
pack.

## 5. Parallel changes with worktrees

```console
$ blueprint new --worktree "add farewell message"
tier: light  (type=feat)
  ...
change: /Users/fde/code/acme/.blueprint/changes/2026-07-21-add-farewell-message/change.md (draft)
worktree: /Users/fde/code/acme-wt/2026-07-21-add-farewell-message (branch agent/2026-07-21-add-farewell-message)
```

One maker per worktree, each on its own `agent/**` branch, each gated by its
own `blueprint verify <id>` — parallelism without shared-tree races.

## 6. Wire MCP for Claude Code / Cursor / Codex

Declare the server once in `.blueprint/config.toml`:

```toml
[mcp.servers.blueprint]
command = "blueprint"
args = ["mcp", "serve"]
```

Then generate every tool surface:

```console
$ blueprint sync
wrote .mcp.json
wrote .cursor/mcp.json
wrote .codex/config.toml
...
sync: 18 written, 0 unchanged, 1 backed up
```

`.mcp.json` (Claude Code), `.cursor/mcp.json` (Cursor), and `.codex/config.toml`
(Codex) now point at `blueprint mcp serve` — a zero-egress stdio server
exposing `repo_map`, `search`, `symbols`, `blast_radius`, and `spec_lookup`.
The merge preserves entries you added by hand. `sync` also compiles
`.blueprint/safety.toml` deny rules into each tool's native config; verify
drift anytime with `blueprint sync --check`.

`sync` covers six targets: Claude Code, Cursor, Codex CLI, Windsurf, GitHub
Copilot, and Gemini CLI. Windsurf gets rules (mirrored to `.devin/rules/`,
split at its 12k-char cap) plus workflows; Copilot gets `applyTo`-scoped
instructions and prompt files; Gemini gets TOML commands and a
`.gemini/settings.json` merge wiring `context.fileName` to AGENTS.md. Where a
tool has no confirmed project-level MCP surface (Windsurf, Copilot), sync
writes a note file naming your servers instead of guessing a config path.

## 7. Scaling retrieval (Tier 2/3)

Tier 0 (grep) and Tier 1 (`blueprint map` + `blueprint mcp serve`) are always
on and cover most repos. Enable higher tiers when the repo passes ~1k files,
is a monorepo, or runs in an enclave. In `.blueprint/config.toml`:

```toml
[retrieval]
tier2_packing = "repomix"    # context packing, launched via npx
tier2_lsp = "serena"         # LSP symbols, launched via uvx

[retrieval.graph]            # Tier 3: external MCP stdio graph backend
command = "your-graph-server --stdio"
```

`blueprint sync` then adds the Tier-2 MCP servers to every generated tool
config, and `blueprint mcp serve` proxies `blast_radius` and
`search{mode:"graph"}` to the graph backend (spawned per call, 30s timeout;
any failure names the error and falls back to Tier 1). Verify the external
binaries resolve: `blueprint doctor` (check `retrieval-tiers`).

## Where to next

- Staged adoption with exit checks, interviews, ratchet doctrine:
  [ADOPT.md](ADOPT.md)
- Offline install, upgrade, rollback, uninstall: [INSTALL.md](INSTALL.md)
- Health checks anytime: `blueprint doctor` · loop state: `blueprint status`
  · journal: `blueprint log <id>` · every command takes `--json`

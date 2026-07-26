# Blueprint

Spec-driven development for any coding agent: specs that verify, loops that
stop, knowledge that lasts. One static binary, IDE-agnostic canonical artifacts
in `.blueprint/`, generated adapters for six tools: Claude Code, Cursor,
Codex CLI, Windsurf, GitHub Copilot, and Gemini CLI.

Status: v0.1.0 — first release.

## Docs

- [INSTALL.md](INSTALL.md) — online, offline/air-gapped install; upgrade,
  rollback, uninstall; versioning policy; network disclosure
- [QUICKSTART.md](QUICKSTART.md) — an FDE's first session end to end: install,
  adopt, one verified change, parallel worktrees, MCP wiring
- [ADOPT.md](ADOPT.md) — staged adoption for an existing repo, with machine
  exit checks and the stop-anywhere guarantee
- [CONTRACTS.md](CONTRACTS.md) — engineering rules for contributors, human or
  agent
- [docs/THIRD_PARTY.md](docs/THIRD_PARTY.md) — third-party license notices

## Command surface

- Adopt & maintain: `init` · `adopt` · `upgrade` · `uninstall` ·
  `doctor [--adopt-stage <n>]`
- Change loop: `new` · `approve` · `verify` · `close` · `status` · `resume` ·
  `log`
- Retrieval & adapters: `map` · `sync` · `mcp serve`
- Quality & knowledge: `lint` · `registry list` · `garden` · `dream` ·
  `metrics` · `bench retrieval` · `autonomy`

Every command honors `--json` and returns non-zero on failure. Blueprint works
fully air-gapped and ships no telemetry; the only network calls are the
documented opt-in commands in INSTALL.md's network disclosure.

## Build from source

Build: `go build ./cmd/blueprint` · Test: `go test ./...` ·
License check: `go run ./tools/licensecheck -verify`

License: MIT ([LICENSE](LICENSE)).

---
reviewed:            # YYYY-MM-DD — set when a human verifies each line against installed CLI versions (max-age 180 days)
owner:               # who owns the air-gap posture
status: draft
---

# No-egress checklist (per agent CLI)

Work through this before running agents under the `enclave` or `local`
endpoint profiles (`[endpoint]` in `.blueprint/config.toml`). The `blueprint`
binary makes no network calls; the agent CLIs around it can, so each line
below must be verified per machine and re-verified on CLI upgrades. Dev setup
itself is in [dev-env](dev-env.md).

## Claude Code

- [ ] `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` exported in the agent's
      environment (disables non-essential outbound traffic).
- [ ] WebFetch and WebSearch denied in `.claude/settings.json` permissions.
      Preflight: ask the agent to fetch a URL and confirm the tool call is
      refused before trusting the posture.
- [ ] Marketplace/plugin auto-install disabled. Plugins and marketplace
      entries can fetch remote code at startup, which bypasses tool-level
      deny rules — audit `.claude/settings.json` and any installed plugins.

## Codex CLI

- [ ] documented-unknown: no vendor-documented no-egress switch was verified
      when this template was written. Establish it empirically: disconnect
      the network, run a representative task, record which operations fail,
      then replace this line with the verified configuration.

## Cursor

- [ ] documented-unknown: same protocol as Codex CLI — verify empirically
      offline, then replace this line with the verified configuration and
      the CLI/app version it was tested against.

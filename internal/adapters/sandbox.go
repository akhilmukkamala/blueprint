// Sandbox surfaces (DESIGN §10 safety row + §13): the safety compile's
// non-Claude outputs. Codex CLI gets a [sandbox_workspace_write] guidance
// block merged into .codex/config.toml PLUS a sandbox-policy.md documentation
// artifact; Cursor gets a short non-parity note. Neither host can mechanically
// enforce per-glob write denies, and the generated artifacts SAY so — the
// contract here is documented non-parity, never fake enforcement. The
// verify-side tamper hash layer is the universal backstop on every host.
package adapters

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// CodexSandboxPolicyPath is the generated Codex documentation artifact.
const CodexSandboxPolicyPath = ".codex/sandbox-policy.md"

// CursorSafetyNotePath is the generated Cursor non-parity note.
const CursorSafetyNotePath = ".cursor/blueprint-safety.md"

func denyGlobList(denyGlobs []string) string {
	var b strings.Builder
	for _, g := range denyGlobs {
		fmt.Fprintf(&b, "- `%s`\n", g)
	}
	return b.String()
}

// RenderCodexSandboxPolicy renders .codex/sandbox-policy.md from the
// safety.toml deny globs. It is documentation, not enforcement: Codex CLI's
// workspace-write sandbox scopes writes by directory root and has no
// per-glob write-deny surface, and this artifact states that non-parity
// explicitly instead of pretending otherwise.
func RenderCodexSandboxPolicy(denyGlobs []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s %s from .blueprint/safety.toml [deny].write; edit safety.toml, then run `blueprint sync` -->\n", provenanceMarker, GeneratorVersion)
	b.WriteString("# Codex CLI sandbox policy (blueprint safety compile)\n\n")
	b.WriteString(".blueprint/safety.toml denies maker writes to:\n\n")
	b.WriteString(denyGlobList(denyGlobs))
	b.WriteString(`
## Non-parity, stated plainly

Codex CLI's workspace-write sandbox ([sandbox_workspace_write] in
.codex/config.toml) scopes writes by directory root and has NO mechanical
equivalent of per-glob write-deny rules. The globs above are documented
intent on this host, not host-enforced. Do not treat this file as
enforcement.

What blueprint CAN express is merged into .codex/config.toml as a
[sandbox_workspace_write] guidance block (network_access = false unless you
already set it); the deny globs themselves cannot be compiled to Codex.

## Backstop

The verify-side tamper layer is the enforcement of record on every host:
` + "`blueprint verify`" + ` hashes the protected files and fails the change with a
TAMPER verdict on any drift, whether or not the host sandbox could have
blocked the write.
`)
	return []byte(b.String())
}

// RenderCursorSafetyNote renders the .cursor/ non-parity note. Cursor has no
// permission or sandbox surface at all, so the whole deny list is documented
// intent there — one short generated doc says so.
func RenderCursorSafetyNote(denyGlobs []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s %s from .blueprint/safety.toml [deny].write; edit safety.toml, then run `blueprint sync` -->\n", provenanceMarker, GeneratorVersion)
	b.WriteString("# Cursor safety non-parity note (blueprint safety compile)\n\n")
	b.WriteString("Cursor has no permission, deny-rule, or sandbox surface blueprint can\n")
	b.WriteString("compile .blueprint/safety.toml [deny].write into. On Cursor these deny\n")
	b.WriteString("globs are documented intent only — nothing on this host enforces them:\n\n")
	b.WriteString(denyGlobList(denyGlobs))
	b.WriteString("\nBackstop: the verify-side tamper layer (`blueprint verify`) hashes the\n")
	b.WriteString("protected files and fails the change on any drift, on every host.\n")
	return []byte(b.String())
}

// MergeCodexConfig merges both blueprint-owned surfaces of .codex/config.toml
// in one decode/encode pass: the [mcp_servers.<name>] tables from
// .blueprint/config.toml and the [sandbox_workspace_write] guidance block from
// safety.toml. Foreign keys and foreign server entries survive untouched
// (values, not comments — the file is re-encoded and the pre-sync original is
// kept as a .bak). Guidance keys the user already set are never overridden,
// which also keeps regeneration idempotent.
func MergeCodexConfig(existing []byte, servers map[string]MCPServer, denyGlobs []string) ([]byte, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := toml.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf("existing codex config.toml is not valid TOML (%v) — fix or delete it, then rerun `blueprint sync`", err)
		}
	}
	var headers []string
	if len(servers) > 0 {
		var entries map[string]any
		if cur, ok := doc["mcp_servers"].(map[string]any); ok {
			entries = cur
		} else {
			entries = map[string]any{}
		}
		for name, s := range servers {
			entry := map[string]any{"command": s.Command}
			if len(s.Args) > 0 {
				entry["args"] = s.Args
			}
			if len(s.Env) > 0 {
				entry["env"] = s.Env
			}
			entries[name] = entry
		}
		doc["mcp_servers"] = entries
		headers = append(headers, fmt.Sprintf("# [mcp_servers] entries %s %s %s from .blueprint/config.toml [mcp.servers]; other keys are yours — edit the source, then run `blueprint sync`",
			strings.Join(sortedNames(servers), ", "), provenanceMarker, GeneratorVersion))
	}
	if len(denyGlobs) > 0 {
		var sw map[string]any
		if cur, ok := doc["sandbox_workspace_write"].(map[string]any); ok {
			sw = cur
		} else {
			sw = map[string]any{}
		}
		// Guidance, not override: only set what the user has not set.
		if _, ok := sw["network_access"]; !ok {
			sw["network_access"] = false
		}
		doc["sandbox_workspace_write"] = sw
		headers = append(headers, fmt.Sprintf("# [sandbox_workspace_write] guidance %s %s from .blueprint/safety.toml — codex cannot enforce per-glob write denies (see %s); `blueprint verify` tamper hashes are the backstop",
			provenanceMarker, GeneratorVersion, CodexSandboxPolicyPath))
	}

	var buf bytes.Buffer
	for _, h := range headers {
		buf.WriteString(h + "\n")
	}
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("adapters: cannot encode codex config.toml: %w", err)
	}
	out := buf.Bytes()
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	return out, nil
}

// Gemini CLI surfaces (research D7 §6): .gemini/settings.json carries BOTH
// the instruction wiring (context.fileName must name AGENTS.md — Gemini reads
// GEMINI.md by default, so AGENTS.md support is one settings key away) and the
// mcpServers table; custom commands are TOML files with a prompt field. The
// settings merge follows the MergeClaudeSettings pattern: parse, own only our
// keys, preserve every foreign key, provenance in a "//blueprint" key because
// JSON has no comment syntax.
package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
)

// geminiContextFile is the canonical instruction file every adapter points at.
const geminiContextFile = "AGENTS.md"

// MergeGeminiSettings merges the AGENTS.md context wiring and the declared MCP
// servers into an existing .gemini/settings.json (existing may be nil for a
// fresh file). Foreign top-level keys, foreign context keys, and foreign
// mcpServers entries survive untouched. context.fileName is normalized to a
// list with AGENTS.md appended — never replacing names the user already set.
func MergeGeminiSettings(existing []byte, servers map[string]MCPServer) ([]byte, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf(".gemini/settings.json is not valid JSON (%v) — fix or delete it, then rerun `blueprint sync`", err)
		}
	}

	ctx, _ := doc["context"].(map[string]any)
	if ctx == nil {
		ctx = map[string]any{}
	}
	var names []any
	switch fn := ctx["fileName"].(type) {
	case string:
		if fn != "" {
			names = []any{fn}
		}
	case []any:
		names = fn
	}
	found := false
	for _, n := range names {
		if s, ok := n.(string); ok && s == geminiContextFile {
			found = true
			break
		}
	}
	if !found {
		names = append(names, geminiContextFile)
	}
	ctx["fileName"] = names
	doc["context"] = ctx

	if len(servers) > 0 {
		entries, _ := doc["mcpServers"].(map[string]any)
		if entries == nil {
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
		doc["mcpServers"] = entries
	}

	doc["//blueprint"] = fmt.Sprintf("context.fileName (%s) and any [mcp.servers] entries here are %s %s via `blueprint sync`; other keys are yours — edit .blueprint/config.toml, not this file",
		geminiContextFile, provenanceMarker, GeneratorVersion)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// renderCommandTOML renders one command stub as a Gemini custom command:
// a TOML file whose prompt field holds the same text the markdown stubs
// carry. The provenance header is a # comment (TOML supports comments).
func renderCommandTOML(t Target, s commandStub) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %s %s for %s; edit nothing here — regenerate with `blueprint sync`\n", provenanceMarker, GeneratorVersion, t.Name)
	doc := struct {
		Description string `toml:"description"`
		Prompt      string `toml:"prompt"`
	}{
		Description: s.title,
		Prompt:      fmt.Sprintf("Run:\n\n```sh\n%s\n```\n\n%s", s.command, s.usage),
	}
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(doc); err != nil {
		// Two string fields cannot fail TOML encoding; keep the signature
		// allocation-free for callers by panicking on the impossible.
		panic(fmt.Sprintf("adapters: encode gemini command %s: %v", s.name, err))
	}
	out := buf.Bytes()
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	return out
}

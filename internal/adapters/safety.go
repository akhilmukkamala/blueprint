package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Safety compile (DESIGN §13, minimal MVP surface): the [deny].write globs in
// .blueprint/safety.toml become Claude Code permission deny rules merged into
// .claude/settings.json — capability enforcement where the host supports it.
// Cursor has no equivalent surface (documented non-parity) and the Codex
// sandbox profile ships with the packaging phase; the verify-side tamper hash
// layer remains the universal backstop on every host.

// LoadDenyGlobs reads the maker write-denylist from safety.toml.
func LoadDenyGlobs(repoRoot string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".blueprint", "safety.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc struct {
		Deny struct {
			Write []string `toml:"write"`
		} `toml:"deny"`
	}
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return nil, fmt.Errorf(".blueprint/safety.toml: %v — fix the TOML syntax; `blueprint sync` compiles [deny].write into agent permission rules", err)
	}
	return doc.Deny.Write, nil
}

// MergeClaudeSettings merges deny rules into an existing .claude/settings.json,
// preserving every foreign key and any user-added permission entries. Only
// blueprint-derived rules (tracked by exact string match against the desired
// set) are owned; nothing else is removed.
func MergeClaudeSettings(existing []byte, denyGlobs []string) ([]byte, error) {
	doc := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf(".claude/settings.json is not valid JSON: %v — fix it by hand; `blueprint sync` merges, never overwrites", err)
		}
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	existingDeny, _ := perms["deny"].([]any)
	seen := map[string]bool{}
	for _, d := range existingDeny {
		if s, ok := d.(string); ok {
			seen[s] = true
		}
	}
	for _, g := range denyGlobs {
		for _, rule := range []string{"Write(" + g + ")", "Edit(" + g + ")"} {
			if !seen[rule] {
				existingDeny = append(existingDeny, rule)
				seen[rule] = true
			}
		}
	}
	perms["deny"] = existingDeny
	doc["permissions"] = perms
	if _, ok := doc["//blueprint"]; !ok {
		doc["//blueprint"] = "permission deny rules below are " + provenanceMarker + " " + GeneratorVersion + " from .blueprint/safety.toml [deny].write via `blueprint sync`; edit safety.toml, not this list"
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

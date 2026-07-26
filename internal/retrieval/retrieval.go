// Package retrieval is the Tier 2/3 scaling configuration (DESIGN §9): the
// [retrieval] table of .blueprint/config.toml names OPTIONAL external
// backends — Tier 2 packing/LSP tools wired into generated MCP configs by
// `blueprint sync`, and a Tier 3 graph backend spawned per call by
// `blueprint mcp serve`. Everything here is exec'd or config-projected,
// never linked: the binary stays network-incapable and dependency-frozen.
// Empty values mean the tier is off and Tier 0/1 carry the load (G14).
package retrieval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// GraphConfig is the [retrieval.graph] table: an external MCP-speaking
// process (stdio, newline-delimited JSON-RPC) that answers blast_radius and
// search{mode:"graph"}. The command line is split on whitespace — no shell.
type GraphConfig struct {
	Command string `toml:"command" json:"command"`
}

// Config is the [retrieval] table. Enabled tiers are derived from non-empty
// values; there is no separate on/off switch to drift out of sync.
type Config struct {
	Tier2Packing string      `toml:"tier2_packing" json:"tier2_packing"` // e.g. "repomix"
	Tier2LSP     string      `toml:"tier2_lsp" json:"tier2_lsp"`         // e.g. "serena"
	Graph        GraphConfig `toml:"graph" json:"graph"`
}

// Tier2Enabled reports whether any Tier-2 tool is configured.
func (c Config) Tier2Enabled() bool { return c.Tier2Packing != "" || c.Tier2LSP != "" }

// GraphEnabled reports whether the Tier-3 graph backend is configured.
func (c Config) GraphEnabled() bool { return strings.TrimSpace(c.Graph.Command) != "" }

type configFile struct {
	Retrieval Config `toml:"retrieval"`
}

// Load reads the [retrieval] table from .blueprint/config.toml. A missing
// file or absent table is the zero Config (Tier 0/1 only) — retrieval tiers
// are opt-in, never a prerequisite.
func Load(repoRoot string) (Config, error) {
	p := filepath.Join(repoRoot, ".blueprint", "config.toml")
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("retrieval: cannot read %s: %w", p, err)
	}
	var f configFile
	if err := toml.Unmarshal(raw, &f); err != nil {
		return Config{}, fmt.Errorf("retrieval: %s is not valid TOML (%v) — fix the syntax error before retrieval tiers can be read", p, err)
	}
	f.Retrieval.Tier2Packing = strings.TrimSpace(f.Retrieval.Tier2Packing)
	f.Retrieval.Tier2LSP = strings.TrimSpace(f.Retrieval.Tier2LSP)
	f.Retrieval.Graph.Command = strings.TrimSpace(f.Retrieval.Graph.Command)
	return f.Retrieval, nil
}

// SplitCommand turns the configured graph command line into argv. Plain
// whitespace splitting, no shell semantics — quoting or substitution in the
// value is a misconfiguration, and Windows has no /bin/sh to defer to.
func SplitCommand(command string) []string {
	return strings.Fields(command)
}

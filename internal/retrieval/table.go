// The versioned Tier-2 tool table: the ONE place upstream launch command
// lines live. Both tools are MIT-licensed and license-verified as EXTERNAL
// integrations — we generate config that launches them, we never vendor,
// import, or link them (BUILD_BRIEF 3A).
package retrieval

import (
	"fmt"
	"strings"
)

// TableVersion is bumped whenever an upstream command line below changes,
// so generated-config diffs trace to a table revision.
const TableVersion = "2026-07"

// Tool is one Tier-2 entry: the MCP launch line `blueprint sync` projects
// into tool configs, plus what doctor needs to validate the environment.
type Tool struct {
	Name    string   // config value and generated MCP server name
	Kind    string   // "packing" | "lsp"
	Command string   // launcher binary (must be on PATH — doctor checks)
	Args    []string // launch arguments
	Install string   // remediation when the launcher is missing
}

// tier2Table holds both supported Tier-2 command lines. CAUTION: these are
// upstream CLIs and they churn — the exact flags/refs here are a snapshot,
// TableVersion tracks revisions, and the doctor `retrieval-tiers` check
// validates the launchers are actually present before anyone relies on them.
var tier2Table = map[string]Tool{
	"repomix": {
		Name:    "repomix",
		Kind:    "packing",
		Command: "npx",
		Args:    []string{"-y", "repomix", "--mcp"},
		Install: "install Node.js (which provides npx) — https://nodejs.org — then rerun `blueprint doctor`",
	},
	"serena": {
		Name:    "serena",
		Kind:    "lsp",
		Command: "uvx",
		Args:    []string{"--from", "git+https://github.com/oraios/serena", "serena", "start-mcp-server"},
		Install: "install uv (which provides uvx) — https://docs.astral.sh/uv — then rerun `blueprint doctor`",
	},
}

// LookupTool resolves a configured tier2_* value against the table.
func LookupTool(name string) (Tool, bool) {
	t, ok := tier2Table[name]
	return t, ok
}

// KnownToolNames lists the accepted tier2_* values for remediation text.
func KnownToolNames() []string { return []string{"repomix", "serena"} }

// Tier2Tools returns the tools enabled by cfg, keyed by server name. An
// unknown value is an error naming the accepted set — a typo that silently
// wired nothing would be a Tier-2 that everyone believes is on.
func Tier2Tools(cfg Config) (map[string]Tool, error) {
	out := map[string]Tool{}
	for key, val := range map[string]string{"tier2_packing": cfg.Tier2Packing, "tier2_lsp": cfg.Tier2LSP} {
		if val == "" {
			continue
		}
		t, ok := LookupTool(val)
		if !ok {
			return nil, fmt.Errorf("retrieval: [retrieval] %s = %q is not a known tool — supported values: %s (config: .blueprint/config.toml)",
				key, val, strings.Join(KnownToolNames(), ", "))
		}
		out[t.Name] = t
	}
	return out, nil
}

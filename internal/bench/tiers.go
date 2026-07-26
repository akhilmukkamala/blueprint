// Dynamic tier grid (DESIGN §9): the A/B plan always contains the tier0 grep
// baseline and the tier1 in-binary repo map; the opt-in tiers (Repomix
// packing, Serena LSP, graph backend) join the grid only when the operator has
// actually configured them in .blueprint/config.toml — benchmarking a tier
// that cannot run would fabricate pending cells forever.
//
// INTEGRATOR NOTE (benchcorpus feature): the [retrieval] TOML keys below are a
// deliberately minimal local reader because the tiers feature's
// internal/retrieval package is being built on a parallel branch. After both
// branches merge, replace readRetrievalConfig with the internal/retrieval
// loader and delete the local retrievalFileTOML types — the key names
// ([retrieval.packing], [retrieval.lsp], [retrieval.graph], each with an
// optional `enabled` bool) are the shared contract.
package bench

import (
	"blueprint/internal/retrieval"
)

const (
	// Tier2Packing is Repomix packing wired via MCP (opt-in, external tool —
	// exec'd/configured, never linked).
	Tier2Packing Tier = "tier2-packing"
	// Tier2LSP is Serena LSP symbols wired via MCP (opt-in, external tool).
	Tier2LSP Tier = "tier2-lsp"
	// Tier3Graph is the condition-gated swappable graph backend (post-MVP).
	Tier3Graph Tier = "tier3-graph"
)

// DefaultTiers is the always-on grid: optimized grep baseline vs repo map.
var DefaultTiers = []Tier{Tier0Grep, Tier1Map}

// ActiveTiers derives the benchmark grid from the canonical [retrieval]
// config (internal/retrieval): tier0+tier1 always; tier2-packing when
// tier2_packing is set, tier2-lsp when tier2_lsp is set, tier3-graph when
// [retrieval.graph].command is set. Config read failures degrade to the
// default grid — the bench must never fail because an optional tier is
// misconfigured (doctor reports that).
func ActiveTiers(repoRoot string) []Tier {
	tiers := append([]Tier{}, DefaultTiers...)
	cfg, err := retrieval.Load(repoRoot)
	if err != nil {
		return tiers
	}
	if cfg.Tier2Packing != "" {
		tiers = append(tiers, Tier2Packing)
	}
	if cfg.Tier2LSP != "" {
		tiers = append(tiers, Tier2LSP)
	}
	if cfg.GraphEnabled() {
		tiers = append(tiers, Tier3Graph)
	}
	return tiers
}

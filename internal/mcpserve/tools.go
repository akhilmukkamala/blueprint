package mcpserve

import (
	"fmt"
	"sort"
	"strings"

	"blueprint/internal/repomap"
)

// handler runs one tool. A returned error becomes an isError tool result
// (bad arguments); degraded-but-answerable situations must return a payload
// with degraded/reason fields instead (G14: honest degradation, never a
// protocol error).
type handler func(s *Server, args map[string]any) (any, error)

var toolHandlers = map[string]handler{
	"repo_map":     handleRepoMap,
	"search":       handleSearch,
	"symbols":      handleSymbols,
	"blast_radius": handleBlastRadius,
	"spec_lookup":  handleSpecLookup,
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// toolList is the stable five-tool surface (DESIGN §9). Order is fixed.
func toolList() []map[string]any {
	return []map[string]any{
		{
			"name":        "repo_map",
			"description": "Ranked repo map (tree-sitter symbols + import-graph PageRank + git churn) within a token budget.",
			"inputSchema": schema(map[string]any{
				"budget": map[string]any{"type": "number", "description": fmt.Sprintf("token budget (default %d)", repomap.DefaultBudget)},
			}),
		},
		{
			"name":        "search",
			"description": "Repo text search. mode=lexical (default) scans in-process; mode=graph proxies the configured [retrieval.graph] backend; mode=semantic degrades honestly unless a Tier-2 backend is wired.",
			"inputSchema": schema(map[string]any{
				"query": map[string]any{"type": "string", "description": "substring to find (case-insensitive)"},
				"mode":  map[string]any{"type": "string", "enum": []string{"lexical", "semantic", "graph"}, "description": "search mode (default lexical)"},
			}, "query"),
		},
		{
			"name":        "symbols",
			"description": "Symbols in a file (pass a repo-relative path) or files declaring a symbol (pass a name substring).",
			"inputSchema": schema(map[string]any{
				"name_or_file": map[string]any{"type": "string", "description": "repo-relative file path or symbol-name substring"},
			}, "name_or_file"),
		},
		{
			"name":        "blast_radius",
			"description": "Transitive dependents of the given files over the import graph — the router's Tier-1 blast-radius input.",
			"inputSchema": schema(map[string]any{
				"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "repo-relative paths"},
			}, "files"),
		},
		{
			"name":        "spec_lookup",
			"description": "SDD primitive: REQ ID -> living-spec text, trace-annotated test files, and latest verifier verdict.",
			"inputSchema": schema(map[string]any{
				"req_id": map[string]any{"type": "string", "description": "requirement ID, e.g. REQ-auth-001"},
			}, "req_id"),
		},
	}
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

// loadMap is the shared degraded-aware map loader: (map, degradedPayload).
func loadMap(s *Server) (*repomap.Map, any) {
	m, err := repomap.LoadOrBuild(s.RepoRoot, false)
	if err != nil {
		return nil, map[string]any{
			"degraded":    true,
			"reason":      fmt.Sprintf("repo map unavailable: %v", err),
			"remediation": "run `blueprint map --refresh` in the repo root, then retry",
		}
	}
	return m, nil
}

func handleRepoMap(s *Server, args map[string]any) (any, error) {
	budget := repomap.DefaultBudget
	if b, ok := args["budget"].(float64); ok && b > 0 {
		budget = int(b)
	}
	m, degraded := loadMap(s)
	if degraded != nil {
		return degraded, nil
	}
	rendered := m.Render(budget)
	return map[string]any{
		"generated": m.Generated,
		"files":     len(m.Files),
		"budget":    budget,
		"tokens":    repomap.TokenEstimate(rendered),
		"map":       rendered,
	}, nil
}

func handleSymbols(s *Server, args map[string]any) (any, error) {
	q := stringArg(args, "name_or_file")
	if q == "" {
		return nil, fmt.Errorf("symbols needs name_or_file — pass a repo-relative path (e.g. internal/route/router.go) or a symbol-name substring (e.g. Decide)")
	}
	m, degraded := loadMap(s)
	if degraded != nil {
		return degraded, nil
	}
	if f := m.Lookup(strings.ReplaceAll(q, "\\", "/")); f != nil {
		return map[string]any{"file": f.Path, "lang": f.Lang, "symbols": f.Symbols, "imports": f.Imports}, nil
	}
	type hit struct {
		File string `json:"file"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		Line int    `json:"line"`
	}
	var hits []hit
	lq := strings.ToLower(q)
	for _, f := range m.Files {
		for _, sym := range f.Symbols {
			if strings.Contains(strings.ToLower(sym.Name), lq) {
				hits = append(hits, hit{File: f.Path, Name: sym.Name, Kind: sym.Kind, Line: sym.Line})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].Line < hits[j].Line
	})
	const maxHits = 200
	truncated := false
	if len(hits) > maxHits {
		hits, truncated = hits[:maxHits], true
	}
	if len(hits) == 0 {
		return map[string]any{
			"query": q, "matches": []hit{},
			"note": "no file or symbol matched; symbols cover Go/JS/TS/Python — for other languages use the search tool",
		}, nil
	}
	return map[string]any{"query": q, "matches": hits, "truncated": truncated}, nil
}

func handleBlastRadius(s *Server, args map[string]any) (any, error) {
	raw, ok := args["files"].([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("blast_radius needs files: a non-empty array of repo-relative paths")
	}
	var paths []string
	for _, v := range raw {
		if p, ok := v.(string); ok && strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("blast_radius files array contained no path strings")
	}

	// Tier-3 first when configured: forward this exact call to the graph
	// backend; any failure names the error and falls back to Tier 1.
	var graphReason string
	cfg, cfgReason := graphConfig(s)
	if cfgReason != "" {
		graphReason = cfgReason
	} else if cfg.GraphEnabled() {
		payload, err := graphCall(cfg.Graph.Command, "blast_radius", args)
		if err == nil {
			return map[string]any{"tier": "graph", "backend": cfg.Graph.Command, "inputs": paths, "result": payload}, nil
		}
		graphReason = err.Error()
	}

	m, degraded := loadMap(s)
	if degraded != nil {
		return degraded, nil
	}
	deps := m.Dependents(paths)
	out := map[string]any{"inputs": paths, "radius": len(deps), "files": deps, "tier": "tier1-import-graph"}
	if graphReason != "" {
		out["degraded"] = true
		out["reason"] = fmt.Sprintf("Tier-3 graph backend unavailable: %s — answered from the Tier-1 import graph instead", graphReason)
		out["remediation"] = tierConfigRemediation
	}
	return out, nil
}

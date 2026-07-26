package mcpserve

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"blueprint/internal/repomap"
)

// searchMatch is one lexical hit: ripgrep-shaped (path, 1-based line, text).
type searchMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

const (
	maxMatches   = 100
	maxLineChars = 300
)

func handleSearch(s *Server, args map[string]any) (any, error) {
	query := stringArg(args, "query")
	if query == "" {
		return nil, fmt.Errorf("search needs query: a non-empty string to look for")
	}
	mode := stringArg(args, "mode")
	if mode == "" {
		mode = "lexical"
	}
	switch mode {
	case "lexical":
		matches, truncated, err := lexicalScan(s.RepoRoot, query)
		if err != nil {
			return map[string]any{
				"degraded": true, "mode": "lexical", "query": query,
				"reason":      fmt.Sprintf("scan failed: %v", err),
				"remediation": "check the repo root is readable",
			}, nil
		}
		return map[string]any{"mode": "lexical", "query": query, "matches": matches, "truncated": truncated}, nil
	case "semantic":
		// Honest degradation (G14): Tier-2 backends attach to the agent as
		// their own MCP servers, so this server always answers lexically —
		// and says exactly which config keys turn Tier 2/3 on.
		matches, truncated, _ := lexicalScan(s.RepoRoot, query)
		return map[string]any{
			"degraded":       true,
			"mode_requested": "semantic",
			"mode_used":      "lexical",
			"reason":         "Tier-2 semantic backend (repomix/serena) is not wired into this server; falling back to lexical scan",
			"remediation":    tierConfigRemediation,
			"query":          query,
			"matches":        matches,
			"truncated":      truncated,
		}, nil
	case "graph":
		var reason string
		cfg, cfgReason := graphConfig(s)
		switch {
		case cfgReason != "":
			reason = cfgReason
		case cfg.GraphEnabled():
			payload, err := graphCall(cfg.Graph.Command, "search", args)
			if err == nil {
				return map[string]any{"mode": "graph", "backend": cfg.Graph.Command, "query": query, "result": payload}, nil
			}
			reason = err.Error()
		default:
			reason = "no Tier-3 graph backend is configured ([retrieval.graph] command is empty)"
		}
		matches, truncated, _ := lexicalScan(s.RepoRoot, query)
		return map[string]any{
			"degraded":       true,
			"mode_requested": "graph",
			"mode_used":      "lexical",
			"reason":         fmt.Sprintf("Tier-3 graph backend unavailable: %s — falling back to Tier-1 lexical scan", reason),
			"remediation":    tierConfigRemediation,
			"query":          query,
			"matches":        matches,
			"truncated":      truncated,
		}, nil
	default:
		return nil, fmt.Errorf("unknown search mode %q — use lexical, semantic, or graph", mode)
	}
}

// lexicalScan is the in-process ripgrep-style pass: case-insensitive
// substring over every mappable file, skipping the same directories the map
// skips. No subprocess, no index — Tier 0 always works.
func lexicalScan(repoRoot, query string) ([]searchMatch, bool, error) {
	files, err := repomap.ScanFiles(repoRoot)
	if err != nil {
		return nil, false, err
	}
	lq := strings.ToLower(query)
	matches := []searchMatch{}
	for _, rel := range files {
		f, err := os.Open(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			text := sc.Text()
			if !strings.Contains(strings.ToLower(text), lq) {
				continue
			}
			if len(text) > maxLineChars {
				text = text[:maxLineChars] + "…"
			}
			matches = append(matches, searchMatch{File: rel, Line: lineNo, Text: strings.TrimSpace(text)})
			if len(matches) > maxMatches {
				f.Close()
				return matches[:maxMatches], true, nil
			}
		}
		f.Close()
	}
	return matches, false, nil
}

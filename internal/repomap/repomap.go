// Package repomap is Tier-1 retrieval (DESIGN §9): a tree-sitter repo map with
// PageRank-over-imports ranking mixed with git churn, rendered inside a token
// budget, cached to .blueprint/map.json (tool-tier, mtime-invalidated). It
// also supplies the router's real blast-radius input: transitive dependents
// over the import graph. Languages with grammars (Go, JavaScript, TypeScript,
// Python) get symbols + import edges; every other file degrades gracefully to
// a file-level entry ranked by churn alone.
package repomap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Symbol is one top-level declaration extracted by tree-sitter.
type Symbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // func, method, type, class, interface, enum, const
	Line int    `json:"line"` // 1-based
}

// File is one repo file in the map. Imports hold repo-relative slash paths
// only — external modules carry no edge weight and are dropped at build time.
type File struct {
	Path    string   `json:"path"` // repo-relative, slash-separated
	Lang    string   `json:"lang,omitempty"`
	Size    int64    `json:"size"`
	MTime   int64    `json:"mtime"` // unix nanos; cache invalidation key
	Symbols []Symbol `json:"symbols,omitempty"`
	Imports []string `json:"imports,omitempty"`
	Churn   int      `json:"churn"`
	Rank    float64  `json:"rank"`
}

// Map is the built repo map. Files are sorted by Path for determinism; use
// Ranked for display order.
type Map struct {
	Version   int       `json:"version"`
	Generated time.Time `json:"generated"`
	Root      string    `json:"-"`
	Files     []*File   `json:"files"`
}

const mapVersion = 1

// CachePath is where the map persists: .blueprint/map.json (tool-tier).
func CachePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "map.json")
}

// Build walks the repo, parses grammar-covered files, resolves import edges,
// measures churn, and ranks. It never fails on a single unreadable or
// unparsable file — those degrade to file-level entries.
func Build(repoRoot string) (*Map, error) {
	files, err := walkRepo(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("repo walk failed at %s: %v — check the directory exists and is readable", repoRoot, err)
	}
	parseAll(repoRoot, files)
	resolveImports(repoRoot, files)
	applyChurn(repoRoot, files)
	rank(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	m := &Map{Version: mapVersion, Generated: time.Now().UTC(), Root: repoRoot, Files: files}
	return m, nil
}

// LoadOrBuild returns the cached map when every source file's (path, mtime,
// size) still matches; otherwise rebuilds and rewrites the cache. refresh
// forces a rebuild. Cache write failures are non-fatal: the map is returned
// with the error so read-only checkouts still work.
func LoadOrBuild(repoRoot string, refresh bool) (*Map, error) {
	if !refresh {
		if m, ok := loadFresh(repoRoot); ok {
			return m, nil
		}
	}
	m, err := Build(repoRoot)
	if err != nil {
		return nil, err
	}
	if err := m.save(); err != nil {
		return m, fmt.Errorf("map built but cache write failed: %v — commit will still work; fix permissions on %s to enable caching", err, CachePath(repoRoot))
	}
	return m, nil
}

// loadFresh loads the cache and validates it against the current tree.
func loadFresh(repoRoot string) (*Map, bool) {
	b, err := os.ReadFile(CachePath(repoRoot))
	if err != nil {
		return nil, false
	}
	var m Map
	if json.Unmarshal(b, &m) != nil || m.Version != mapVersion {
		return nil, false
	}
	m.Root = repoRoot
	current, err := walkRepo(repoRoot)
	if err != nil || len(current) != len(m.Files) {
		return nil, false
	}
	cached := make(map[string]*File, len(m.Files))
	for _, f := range m.Files {
		cached[f.Path] = f
	}
	for _, f := range current {
		c, ok := cached[f.Path]
		if !ok || c.MTime != f.MTime || c.Size != f.Size {
			return nil, false
		}
	}
	return &m, true
}

func (m *Map) save() error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := CachePath(m.Root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Ranked returns files ordered by rank (desc), path (asc) as tiebreak.
func (m *Map) Ranked() []*File {
	out := make([]*File, len(m.Files))
	copy(out, m.Files)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank > out[j].Rank
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Lookup finds a file by repo-relative slash path.
func (m *Map) Lookup(path string) *File {
	for _, f := range m.Files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

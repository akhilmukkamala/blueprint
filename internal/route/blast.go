package route

import "path/filepath"

// BlastRadius estimates how far a set of touched paths reaches. The repomap
// feature (DESIGN §9 Tier-1 import graph) plugs in a real implementation
// later; the router only depends on this interface.
type BlastRadius interface {
	Radius(paths []string) (int, error)
}

// PathCount is the fallback heuristic when Tier-1 retrieval is disabled
// (DESIGN §4): radius = number of distinct touched files. Deterministic, no
// repo access.
type PathCount struct{}

// Radius counts distinct slash-normalized paths.
func (PathCount) Radius(paths []string) (int, error) {
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		seen[filepath.ToSlash(p)] = struct{}{}
	}
	return len(seen), nil
}

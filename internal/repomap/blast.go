package repomap

import (
	"path/filepath"
	"sort"

	"blueprint/internal/route"
)

// Map satisfies route.BlastRadius (DESIGN §4/§9): the router falls back to
// route.PathCount when Tier-1 is off; the integrator wires this in.
var _ route.BlastRadius = (*Map)(nil)

// Radius counts the touched files plus every transitive dependent over the
// import graph (reverse edges). Paths unknown to the map still count
// themselves — a superset of the PathCount fallback, never below it.
func (m *Map) Radius(paths []string) (int, error) {
	return len(m.Dependents(paths)), nil
}

// Dependents returns the sorted set counted by Radius: seeds plus all
// transitive importers.
func (m *Map) Dependents(paths []string) []string {
	importers := map[string][]string{}
	for _, f := range m.Files {
		for _, imp := range f.Imports {
			importers[imp] = append(importers[imp], f.Path)
		}
	}
	seen := map[string]bool{}
	var queue []string
	for _, p := range paths {
		p = filepath.ToSlash(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		queue = append(queue, p)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range importers[cur] {
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

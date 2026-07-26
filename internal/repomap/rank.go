package repomap

import "sort"

// PageRank constants: standard damping, fixed iteration count for
// determinism (CONTRACTS rule 5 — no convergence-dependent wall clock).
const (
	damping      = 0.85
	prIterations = 30
	churnWeight  = 0.4 // final rank = 0.6*normalized PR + 0.4*normalized churn
)

// rank computes PageRank over the import graph (edge importer -> imported:
// being imported confers importance) and mixes in normalized churn.
func rank(files []*File) {
	n := len(files)
	if n == 0 {
		return
	}
	idxOf := make(map[string]int, n)
	for i, f := range files {
		idxOf[f.Path] = i
	}
	// out-edges as index lists; imports of files outside the map were
	// already dropped by resolveImports.
	outs := make([][]int, n)
	for i, f := range files {
		for _, imp := range f.Imports {
			if j, ok := idxOf[imp]; ok {
				outs[i] = append(outs[i], j)
			}
		}
		sort.Ints(outs[i])
	}

	pr := make([]float64, n)
	next := make([]float64, n)
	for i := range pr {
		pr[i] = 1.0 / float64(n)
	}
	for it := 0; it < prIterations; it++ {
		dangling := 0.0
		for i := range next {
			next[i] = 0
		}
		for i, out := range outs {
			if len(out) == 0 {
				dangling += pr[i]
				continue
			}
			share := pr[i] / float64(len(out))
			for _, j := range out {
				next[j] += share
			}
		}
		base := (1-damping)/float64(n) + damping*dangling/float64(n)
		for i := range next {
			next[i] = base + damping*next[i]
		}
		pr, next = next, pr
	}

	maxPR, maxChurn := 0.0, 0
	for i, f := range files {
		if pr[i] > maxPR {
			maxPR = pr[i]
		}
		if f.Churn > maxChurn {
			maxChurn = f.Churn
		}
	}
	for i, f := range files {
		r := 0.0
		if maxPR > 0 {
			r += (1 - churnWeight) * pr[i] / maxPR
		}
		if maxChurn > 0 {
			r += churnWeight * float64(f.Churn) / float64(maxChurn)
		}
		f.Rank = r
	}
}

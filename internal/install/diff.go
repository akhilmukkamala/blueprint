package install

import (
	"fmt"
	"strings"
)

// diffMaxLines bounds the O(n·m) LCS table; installed config files are tiny,
// so beyond this we report a summary instead of a diff.
const diffMaxLines = 4000

// unifiedDiff renders a minimal unified diff (full-file context, no hunk
// coalescing — the audience is a human eyeballing an upgrade, not patch(1)).
func unifiedDiff(relPath, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	a := splitLines(oldContent)
	b := splitLines(newContent)
	if len(a) > diffMaxLines || len(b) > diffMaxLines {
		return fmt.Sprintf("--- %s (installed)\n+++ %s (template)\ncontent differs (%d -> %d lines; too large to diff inline)\n", relPath, relPath, len(a), len(b))
	}
	// LCS table over lines.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s (installed)\n+++ %s (template)\n", relPath, relPath)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			sb.WriteString(" " + a[i] + "\n")
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			sb.WriteString("-" + a[i] + "\n")
			i++
		default:
			sb.WriteString("+" + b[j] + "\n")
			j++
		}
	}
	for ; i < len(a); i++ {
		sb.WriteString("-" + a[i] + "\n")
	}
	for ; j < len(b); j++ {
		sb.WriteString("+" + b[j] + "\n")
	}
	return sb.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

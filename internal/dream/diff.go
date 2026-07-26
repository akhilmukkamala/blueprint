// diff.go — minimal unified-diff emitters for the two shapes dream proposes:
// creating a new [user]-tier file and appending to an existing one. Emitted
// patches must be `git apply`-able against the commit the dream branch forks
// from, so context lines come from the file content read at extraction time.
package dream

import (
	"fmt"
	"strings"
)

// noNewline is the unified-diff marker for a final line without a newline.
const noNewline = "\\ No newline at end of file"

// newFileDiff renders a git-apply-able patch creating relPath (slash form)
// with content (newline-terminated; one is added if missing).
func newFileDiff(relPath, content string) string {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	lines := splitLines(content)
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", relPath, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// appendDiff renders a git-apply-able patch appending added (newline-
// terminated; one is added if missing) to relPath whose current content is
// existing. An empty or missing file degrades to newFileDiff. A file without
// a trailing newline gets its last line rewritten (with the no-newline
// marker) so git apply accepts the append cleanly.
func appendDiff(relPath string, existing []byte, added string) string {
	if len(existing) == 0 {
		return newFileDiff(relPath, added)
	}
	if added != "" && !strings.HasSuffix(added, "\n") {
		added += "\n"
	}
	addLines := splitLines(added)

	content := strings.ReplaceAll(string(existing), "\r\n", "\n")
	terminated := strings.HasSuffix(content, "\n")
	oldLines := splitLines(content)
	n := len(oldLines)

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", relPath, relPath)
	if terminated {
		ctx := 3
		if n < ctx {
			ctx = n
		}
		start := n - ctx + 1
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", start, ctx, start, ctx+len(addLines))
		for _, l := range oldLines[n-ctx:] {
			b.WriteString(" " + l + "\n")
		}
		for _, l := range addLines {
			b.WriteString("+" + l + "\n")
		}
		return b.String()
	}
	// Unterminated final line: remove and re-add it terminated, then append.
	last := oldLines[n-1]
	fmt.Fprintf(&b, "@@ -%d,1 +%d,%d @@\n", n, n, 1+len(addLines))
	b.WriteString("-" + last + "\n" + noNewline + "\n")
	b.WriteString("+" + last + "\n")
	for _, l := range addLines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// splitLines splits newline-terminated text into lines without terminators;
// a trailing unterminated line is kept.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// changedLines counts +/- payload lines in a unified diff (file headers and
// the no-newline marker excluded) — the AC-10 ≤40-line item budget.
func changedLines(patch string) int {
	count := 0
	for _, l := range strings.Split(patch, "\n") {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			count++
		}
	}
	return count
}

package repomap

import (
	"fmt"
	"strings"
)

// DefaultBudget is the DESIGN §9 target: a ~1–2k-token map.
const DefaultBudget = 1500

// Render emits the ranked map inside budgetTokens (~4 chars/token): one line
// per file — path then its top symbols — highest rank first. Files past the
// budget are summarized in a trailer so the reader knows the map is clipped.
func (m *Map) Render(budgetTokens int) string {
	if budgetTokens <= 0 {
		budgetTokens = DefaultBudget
	}
	budgetChars := budgetTokens * 4
	var b strings.Builder
	ranked := m.Ranked()
	shown := 0
	for _, f := range ranked {
		line := renderLine(f)
		if b.Len()+len(line)+1 > budgetChars {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
		shown++
	}
	if shown < len(ranked) {
		fmt.Fprintf(&b, "… %d more files (raise --budget to see them)\n", len(ranked)-shown)
	}
	return b.String()
}

// maxSymbolChars keeps one file from eating the whole budget.
const maxSymbolChars = 120

func renderLine(f *File) string {
	if len(f.Symbols) == 0 {
		return f.Path
	}
	var syms []string
	used := 0
	for _, s := range f.Symbols {
		if used+len(s.Name)+2 > maxSymbolChars {
			syms = append(syms, "…")
			break
		}
		syms = append(syms, s.Name)
		used += len(s.Name) + 2
	}
	return f.Path + ": " + strings.Join(syms, ", ")
}

// TokenEstimate is the ~4-chars/token heuristic used by the budget.
func TokenEstimate(s string) int {
	return (len(s) + 3) / 4
}

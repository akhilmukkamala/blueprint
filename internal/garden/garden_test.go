package garden

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var gNow = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

func writeG(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedGarden builds the DESIGN §9 fixture: a stale doc, an orphan, duplicate
// paragraphs across two files, an oversize file, a closed-but-unarchived
// change, and a map.json older than the last commit.
func seedGarden(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	k := filepath.Join(root, ".blueprint", "knowledge")
	fresh := "---\nreviewed: " + gNow.AddDate(0, 0, -2).Format("2006-01-02") + "\n---\n"

	// Stale doc (reviewed far past the 90d default max-age).
	writeG(t, filepath.Join(k, "architecture.md"), "---\nreviewed: 2025-06-01\n---\n# Arch\nboundary map with plenty of words\n")
	// Orphan (never referenced from AGENTS.md).
	writeG(t, filepath.Join(k, "orphan.md"), fresh+"# Orphan\nnobody links to this file at all\n")
	// Near-duplicate pair: three identical substantive paragraph lines.
	dupLines := "alpha paragraph shared between the two files\n" +
		"beta paragraph shared between the two files\n" +
		"gamma paragraph shared between the two files\n"
	writeG(t, filepath.Join(k, "glossary.md"), fresh+"# Glossary\n"+dupLines)
	writeG(t, filepath.Join(k, "notes.md"), fresh+"# Notes\n"+dupLines+"one line unique to the notes file here\n")
	// Oversize file: 300-line cap, 310 substantive-ish lines.
	var big strings.Builder
	big.WriteString(fresh)
	for i := 0; i < 320; i++ {
		fmt.Fprintf(&big, "unique filler content line number %d for the oversize fixture\n", i)
	}
	writeG(t, filepath.Join(k, "debt.md"), big.String())

	writeG(t, filepath.Join(root, "AGENTS.md"),
		"# Index\n- knowledge/architecture.md\n- knowledge/glossary.md\n- knowledge/notes.md\n- knowledge/debt.md\n")

	// Closed-but-unarchived change.
	writeG(t, filepath.Join(root, ".blueprint", "changes", "2026-06-01-done", "change.md"),
		"+++\nid = \"2026-06-01-done\"\ntitle = \"t\"\ntype = \"fix\"\ntier = \"light\"\nstatus = \"closed\"\nscenario = \"bug\"\n+++\n\n## Tasks\n- [x] T1: done\n")

	// Stale map: mtime one hour before the (stubbed) last commit.
	mapPath := filepath.Join(root, ".blueprint", "map.json")
	writeG(t, mapPath, "{}")
	if err := os.Chtimes(mapPath, gNow.Add(-2*time.Hour), gNow.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return root
}

func gardenOpts() Options {
	return Options{
		Now: func() time.Time { return gNow },
		Git: func(repoRoot string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return fmt.Sprintf("%d", gNow.Add(-1*time.Hour).Unix()), nil
			}
			return "", fmt.Errorf("unexpected git args %v", args)
		},
	}
}

func itemsByCategory(r *Report) map[string][]Item {
	m := map[string][]Item{}
	for _, it := range r.Items {
		m[it.Category] = append(m[it.Category], it)
	}
	return m
}

func TestGardenReport(t *testing.T) {
	root := seedGarden(t)
	rep, err := Run(root, gardenOpts())
	if err != nil {
		t.Fatal(err)
	}
	cats := itemsByCategory(rep)

	lintItems := cats["knowledge-lint"]
	var sawStale, sawOrphan bool
	for _, it := range lintItems {
		if strings.Contains(it.Detail, "knowledge-stale") && strings.Contains(it.File, "architecture.md") {
			sawStale = true
			if it.Priority != PrioLintError {
				t.Errorf("stale doc must be P%d, got %+v", PrioLintError, it)
			}
		}
		if strings.Contains(it.Detail, "knowledge-orphan") && strings.Contains(it.File, "orphan.md") {
			sawOrphan = true
		}
	}
	if !sawStale || !sawOrphan {
		t.Errorf("want stale architecture.md and orphan orphan.md in knowledge-lint items, got %+v", lintItems)
	}

	if ms := cats["map-stale"]; len(ms) != 1 || !strings.Contains(ms[0].Fix, "blueprint map --refresh --quiet") {
		t.Errorf("stale map.json must yield one map-stale item with the exact refresh command, got %+v", ms)
	}
	ac := cats["archive-candidate"]
	if len(ac) != 1 || !strings.Contains(ac[0].Fix, "blueprint close 2026-06-01-done") {
		t.Errorf("closed-but-unarchived change must name the close command, got %+v", ac)
	}
	ov := cats["slop-oversize"]
	if len(ov) != 1 || !strings.Contains(ov[0].File, "debt.md") {
		t.Errorf("323-line debt.md must be flagged oversize, got %+v", ov)
	}
	dup := cats["slop-duplicate"]
	if len(dup) != 1 || !strings.Contains(dup[0].Detail, "shared lines") {
		t.Errorf("glossary/notes duplicate pair must be flagged, got %+v", dup)
	}

	if rep.SuggestedDeletions <= 0 {
		t.Errorf("oversize + duplicates must produce a positive net-lines suggestion, got %d", rep.SuggestedDeletions)
	}
	if !strings.Contains(rep.Summary, fmt.Sprintf("delete %d lines", rep.SuggestedDeletions)) {
		t.Errorf("summary must frame the net-lines-delta suggestion, got %q", rep.Summary)
	}

	// Prioritized: items are sorted non-decreasing by priority.
	for i := 1; i < len(rep.Items); i++ {
		if rep.Items[i].Priority < rep.Items[i-1].Priority {
			t.Fatalf("items not sorted by priority at %d: %+v", i, rep.Items)
		}
	}
	// Every item teaches its fix.
	for _, it := range rep.Items {
		if it.Fix == "" {
			t.Errorf("item without a fix: %+v", it)
		}
	}
	// The report is a JSON-able artifact for --json.
	if _, err := json.Marshal(rep); err != nil {
		t.Fatal(err)
	}
}

func TestGardenReadOnly(t *testing.T) {
	root := seedGarden(t)
	before := snapshot(t, root)
	if _, err := Run(root, gardenOpts()); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, root)
	if before != after {
		t.Fatalf("garden must be read-only\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestGardenCleanRepo(t *testing.T) {
	root := t.TempDir()
	k := filepath.Join(root, ".blueprint", "knowledge")
	writeG(t, filepath.Join(k, "glossary.md"),
		"---\nreviewed: "+gNow.AddDate(0, 0, -2).Format("2006-01-02")+"\n---\n# Glossary\na single fresh well-indexed file\n")
	writeG(t, filepath.Join(root, "AGENTS.md"), "# Index\n- .blueprint/knowledge/glossary.md\n")
	mapPath := filepath.Join(root, ".blueprint", "map.json")
	writeG(t, mapPath, "{}")
	// The fixture clock is in the (real) future; stamp the map as written now
	// so it postdates the stubbed last commit.
	if err := os.Chtimes(mapPath, gNow, gNow); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(root, gardenOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 0 {
		t.Fatalf("clean fixture must produce no items, got %+v", rep.Items)
	}
	if rep.SuggestedDeletions != 0 || !strings.Contains(rep.Summary, "nothing to delete") {
		t.Errorf("clean summary should say there is nothing to delete, got %q", rep.Summary)
	}
}

func TestGardenMissingMap(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(root, gardenOpts())
	if err != nil {
		t.Fatal(err)
	}
	ms := itemsByCategory(rep)["map-stale"]
	if len(ms) != 1 || !strings.Contains(ms[0].Detail, "no repo-map cache") {
		t.Fatalf("missing map.json must be reported, got %+v", rep.Items)
	}
}

// snapshot renders path -> size+mtime for every file under root.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

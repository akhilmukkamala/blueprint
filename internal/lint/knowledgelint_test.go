package lint

import (
	"blueprint/internal/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var kNow = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

func writeK(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rulesOf(fs []core.LintFinding) map[string][]core.LintFinding {
	m := map[string][]core.LintFinding{}
	for _, f := range fs {
		m[f.Rule] = append(m[f.Rule], f)
	}
	return m
}

func TestKnowledgeFilesClassification(t *testing.T) {
	root := t.TempDir()
	k := filepath.Join(root, ".blueprint", "knowledge")
	writeK(t, filepath.Join(k, "architecture.md"), "---\nreviewed: 2026-07-01\n---\nx\n")
	writeK(t, filepath.Join(k, "runbooks", "dev-env.md"), "x\n")
	writeK(t, filepath.Join(k, "decisions", "0001.md"), "---\nstatus: accepted\n---\nx\n")

	infos, err := KnowledgeFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("want 3 infos, got %+v", infos)
	}
	byClass := map[string]KnowledgeFileInfo{}
	for _, i := range infos {
		byClass[i.Class] = i
	}
	if byClass["architecture"].MaxAge != KnowledgeDefaultMaxAge {
		t.Errorf("architecture max-age = %v, want %v", byClass["architecture"].MaxAge, KnowledgeDefaultMaxAge)
	}
	if byClass["runbook"].MaxAge != KnowledgeRunbookMaxAge {
		t.Errorf("runbook max-age = %v, want %v", byClass["runbook"].MaxAge, KnowledgeRunbookMaxAge)
	}
	if byClass["decision"].MaxAge != 0 {
		t.Errorf("accepted ADR must be exempt (MaxAge 0), got %v", byClass["decision"].MaxAge)
	}
	if byClass["architecture"].Reviewed.IsZero() {
		t.Errorf("architecture reviewed date must parse")
	}
}

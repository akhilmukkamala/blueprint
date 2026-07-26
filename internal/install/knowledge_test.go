package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
)

func TestMaterializeKnowledge(t *testing.T) {
	root := t.TempDir()
	created, err := MaterializeKnowledge(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != len(knowledgeRelPaths) {
		t.Fatalf("want %d files created, got %v", len(knowledgeRelPaths), created)
	}
	for _, rel := range knowledgeRelPaths {
		p := filepath.Join(root, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s not written: %v", rel, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", rel)
		}
	}

	// Manifest records every skeleton as [user]-tier.
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range knowledgeRelPaths {
		e, ok := m.Files[rel]
		if !ok {
			t.Fatalf("%s missing from manifest", rel)
		}
		if e.Tier != core.OwnerUser {
			t.Fatalf("%s recorded as %q, want user tier", rel, e.Tier)
		}
	}

	// Idempotent + never overwrites: a curated file survives a re-run.
	curated := filepath.Join(root, ".blueprint", "knowledge", "glossary.md")
	if err := os.WriteFile(curated, []byte("curated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = MaterializeKnowledge(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("second run must create nothing, got %v", created)
	}
	if b, _ := os.ReadFile(curated); string(b) != "curated\n" {
		t.Fatal("MaterializeKnowledge overwrote a [user] file")
	}
}

func TestKnowledgeTemplatesCarryFrontmatterAndExecutableRunbook(t *testing.T) {
	for _, tpl := range Templates() {
		if !strings.HasPrefix(tpl.RelPath, ".blueprint/knowledge/") {
			continue
		}
		b, err := tpl.Content()
		if err != nil {
			t.Fatalf("%s: %v", tpl.RelPath, err)
		}
		if !strings.HasPrefix(string(b), "---\n") {
			t.Fatalf("%s must open with a frontmatter block", tpl.RelPath)
		}
		if tpl.Tier != core.OwnerUser || tpl.Floor {
			t.Fatalf("%s must be optional [user] tier, got tier=%s floor=%v", tpl.RelPath, tpl.Tier, tpl.Floor)
		}
	}
	// The dev-env runbook is executable documentation: doctor --run-dev-env
	// executes its first fenced block, so the template must carry one.
	tf, ok := templateByRelPath(".blueprint/knowledge/runbooks/dev-env.md")
	if !ok {
		t.Fatal("dev-env runbook template missing")
	}
	b, _ := tf.Content()
	if !strings.Contains(string(b), "```sh") {
		t.Fatal("dev-env template must contain a fenced command block")
	}
}

func TestAdoptWritesKnowledgeSkeletons(t *testing.T) {
	root := t.TempDir()
	res, err := Adopt(root, time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Knowledge) != len(knowledgeRelPaths) {
		t.Fatalf("adopt stage 0 must write the full skeleton set, got %v", res.Knowledge)
	}
	if _, err := os.Stat(filepath.Join(root, ".blueprint", "knowledge", "runbooks", "no-egress.md")); err != nil {
		t.Fatal("no-egress runbook missing after adopt")
	}
}

package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/core"
)

// installFixtureChange copies testdata/change.md into a temp repo as change id.
func installFixtureChange(t *testing.T, root, id string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "change.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeChange(t, root, id, string(raw))
}

func writeChange(t *testing.T, root, id, content string) {
	t.Helper()
	dir := filepath.Join(root, ".blueprint", "changes", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "change.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFallbackLoadChange(t *testing.T) {
	root := t.TempDir()
	installFixtureChange(t, root, "c1")
	c, err := Source().LoadChange(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Add rate limiting to the ingest API" {
		t.Errorf("title = %q", c.Title)
	}
	want := core.LoopContract{}
	want.Predicate = "go version"
	if c.Contract.Predicate != "go version" || c.Contract.MaxIterations != 5 ||
		c.Contract.MaxMinutes != 90 || c.Contract.MaxUSD != 10.0 {
		t.Errorf("contract = %+v, want %+v (partial)", c.Contract, want)
	}
	if c.Contract.Breaker.RepeatActionN != 3 || c.Contract.Breaker.MonologueTokens != 4000 {
		t.Errorf("breaker = %+v", c.Contract.Breaker)
	}
	if len(c.Contract.Writable) != 2 || c.Contract.Writable[0] != "src/**" {
		t.Errorf("writable = %v", c.Contract.Writable)
	}
	if len(c.Tasks) != 3 {
		t.Fatalf("tasks = %+v, want 3", c.Tasks)
	}
	if !c.Tasks[0].Done || c.Tasks[0].ID != "T1" {
		t.Errorf("task 1 = %+v, want done T1", c.Tasks[0])
	}
	if c.Tasks[1].Done || c.Tasks[1].ID != "T2" {
		t.Errorf("task 2 = %+v, want open T2", c.Tasks[1])
	}
}

func TestFallbackLoadChangePlusFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeChange(t, root, "c2", "+++\n[loop]\npredicate = \"go version\"\nmax_iterations = 2\n+++\n# Title\n\n- [ ] only task\n")
	c, err := Source().LoadChange(root, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if c.Contract.MaxIterations != 2 {
		t.Errorf("contract = %+v", c.Contract)
	}
	if len(c.Tasks) != 1 || c.Tasks[0].ID != "T1" || c.Tasks[0].Done {
		t.Errorf("tasks = %+v", c.Tasks)
	}
}

func TestFallbackLoadChangeMissingIsRemediation(t *testing.T) {
	_, err := Source().LoadChange(t.TempDir(), "ghost")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "blueprint new") {
		t.Errorf("error should teach remediation, got %q", got)
	}
}

func TestFallbackBadTOMLIsRemediation(t *testing.T) {
	root := t.TempDir()
	writeChange(t, root, "c3", "```toml\n[loop]\npredicate =\n```\n")
	_, err := Source().LoadChange(root, "c3")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); !contains(got, "DESIGN") {
		t.Errorf("error should point at the schema doc, got %q", got)
	}
}

func TestListChanges(t *testing.T) {
	root := t.TempDir()
	if ids, err := Source().ListChanges(root); err != nil || ids != nil {
		t.Fatalf("empty repo: %v %v", ids, err)
	}
	installFixtureChange(t, root, "b-change")
	installFixtureChange(t, root, "a-change")
	ids, err := Source().ListChanges(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a-change" || ids[1] != "b-change" {
		t.Errorf("ids = %v, want sorted [a-change b-change]", ids)
	}
}

func TestRegisterSourceSwapsAndRestores(t *testing.T) {
	stub := stubSource{}
	RegisterSource(stub)
	if _, ok := Source().(stubSource); !ok {
		t.Fatal("RegisterSource did not swap")
	}
	RegisterSource(nil)
	if _, ok := Source().(fallbackSource); !ok {
		t.Fatal("RegisterSource(nil) did not restore fallback")
	}
}

// stubSource is the tiny local stand-in for the internal/spec package
// (per the integration contract: stub in test helpers, never a fake package).
type stubSource struct {
	change *core.Change
	ids    []string
}

func (s stubSource) LoadChange(repoRoot, id string) (*core.Change, error) {
	if s.change != nil {
		return s.change, nil
	}
	return fallbackSource{}.LoadChange(repoRoot, id)
}
func (s stubSource) ListChanges(repoRoot string) ([]string, error) { return s.ids, nil }
func (s stubSource) ChangePath(repoRoot, id string) string {
	return fallbackSource{}.ChangePath(repoRoot, id)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

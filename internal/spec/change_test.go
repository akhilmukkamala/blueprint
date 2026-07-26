package spec

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"blueprint/internal/core"
)

const changeID = "2026-07-21-login-lockout"

func TestLoadChange(t *testing.T) {
	c, err := LoadChange(fixtureRepo(t), changeID)
	if err != nil {
		t.Fatalf("LoadChange: %v", err)
	}
	if c.ID != changeID || c.Title != "Add login lockout" || c.Type != "feat" {
		t.Errorf("identity = %q/%q/%q", c.ID, c.Title, c.Type)
	}
	if c.Tier != core.TierFull || c.Status != core.StatusVerified || c.Scenario != "brownfield-feature" {
		t.Errorf("routing = %q/%q/%q", c.Tier, c.Status, c.Scenario)
	}
	// Loop contract, including [loop.boundaries] nesting.
	if c.Contract.Predicate != "blueprint verify "+changeID {
		t.Errorf("predicate = %q", c.Contract.Predicate)
	}
	if c.Contract.MaxIterations != 12 || c.Contract.MaxMinutes != 90 || c.Contract.MaxUSD != 15.0 {
		t.Errorf("caps = %d/%d/%v", c.Contract.MaxIterations, c.Contract.MaxMinutes, c.Contract.MaxUSD)
	}
	if c.Contract.Breaker.RepeatActionN != 3 || c.Contract.Breaker.OscillationN != 2 || c.Contract.Breaker.MonologueTokens != 4000 {
		t.Errorf("breaker = %+v", c.Contract.Breaker)
	}
	if !reflect.DeepEqual(c.Contract.Writable, []string{"src/**", "tests/**"}) {
		t.Errorf("writable = %v", c.Contract.Writable)
	}
	if !reflect.DeepEqual(c.Contract.ReadOnly, []string{".blueprint/specs/**"}) {
		t.Errorf("readonly = %v", c.Contract.ReadOnly)
	}

	if len(c.Delta) != 3 {
		t.Fatalf("got %d delta entries, want 3", len(c.Delta))
	}
	ops := []core.DeltaOp{c.Delta[0].Op, c.Delta[1].Op, c.Delta[2].Op}
	if !reflect.DeepEqual(ops, []core.DeltaOp{core.DeltaAdded, core.DeltaModified, core.DeltaRemoved}) {
		t.Errorf("ops = %v", ops)
	}
	if c.Delta[0].Area != "auth" || c.Delta[0].Requirement.ID != "REQ-auth-003" {
		t.Errorf("delta[0] = %s in area %q", c.Delta[0].Requirement.ID, c.Delta[0].Area)
	}
	if c.Delta[0].Requirement.Pattern != core.PatternUnwanted {
		t.Errorf("delta[0] pattern = %q", c.Delta[0].Requirement.Pattern)
	}
	if c.Delta[2].Requirement.Text != "" || len(c.Delta[2].Requirement.Verify) != 0 {
		t.Errorf("REMOVED entry carries text/verify: %+v", c.Delta[2].Requirement)
	}

	if len(c.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(c.Tasks))
	}
	t1 := c.Tasks[0]
	if t1.ID != "T1" || !t1.Done {
		t.Errorf("task1 = %+v", t1)
	}
	if !reflect.DeepEqual(t1.Consumes, []string{".blueprint/specs/auth/spec.md"}) {
		t.Errorf("task1 consumes = %v", t1.Consumes)
	}
	if !reflect.DeepEqual(t1.Produces, []string{"tests/auth/lockout_test.go"}) {
		t.Errorf("task1 produces = %v", t1.Produces)
	}
	if c.Tasks[1].Done {
		t.Errorf("task2 should be unchecked")
	}
	if !strings.HasPrefix(c.Design, "Lockout state lives") {
		t.Errorf("design = %q", c.Design)
	}
}

func TestChangeRoundTrip(t *testing.T) {
	repo := tempRepo(t)
	orig, err := LoadChange(repo, changeID)
	if err != nil {
		t.Fatalf("LoadChange: %v", err)
	}
	if err := SaveChange(repo, orig); err != nil {
		t.Fatalf("SaveChange: %v", err)
	}
	again, err := LoadChange(repo, changeID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(orig, again) {
		t.Errorf("round trip drifted:\n orig: %+v\nagain: %+v", orig, again)
	}
}

func TestSaveChangeCreatesFolder(t *testing.T) {
	repo := t.TempDir()
	c := &core.Change{
		ID:     "2026-07-21-new",
		Title:  "New change",
		Type:   "fix",
		Tier:   core.TierLight,
		Status: core.StatusDraft,
		Tasks:  []core.Task{{ID: "T1", Text: "Write the failing regression test in tests/x_test.go"}},
	}
	if err := SaveChange(repo, c); err != nil {
		t.Fatalf("SaveChange: %v", err)
	}
	if _, err := os.Stat(ChangePath(repo, c.ID)); err != nil {
		t.Fatalf("change.md not created: %v", err)
	}
	again, err := LoadChange(repo, c.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(c, again) {
		t.Errorf("round trip drifted:\n orig: %+v\nagain: %+v", c, again)
	}
}

func TestListChanges(t *testing.T) {
	ids, err := ListChanges(fixtureRepo(t))
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{changeID}) {
		t.Errorf("ids = %v", ids)
	}
}

func TestChangePath(t *testing.T) {
	got := ChangePath("root", "abc")
	want := filepath.Join("root", ".blueprint", "changes", "abc", "change.md")
	if got != want {
		t.Errorf("ChangePath = %q, want %q", got, want)
	}
}

func TestLoadChangeIDMismatch(t *testing.T) {
	repo := tempRepo(t)
	// Copy the change folder under a different name; frontmatter id now
	// disagrees with the folder.
	src := ChangePath(repo, changeID)
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".blueprint", "changes", "other-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ChangePath(repo, "other-id"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadChange(repo, "other-id")
	if err == nil || !strings.Contains(err.Error(), "does not match folder name") {
		t.Errorf("err = %v, want folder-name mismatch", err)
	}
}

func TestParseChangeErrors(t *testing.T) {
	cases := []struct {
		name, raw, wantSub string
	}{
		{"no fence", "id = \"x\"\n", "frontmatter fence"},
		{"bad toml", "+++\nid = !!\n+++\nbody\n", "not valid TOML"},
		{"bad delta heading", "+++\nid = \"x\"\n+++\n## Delta\n\n### ADDED nonsense\n", "malformed"},
		{"removed with text", "+++\nid = \"x\"\n+++\n## Delta\n\n### REMOVED REQ-a-001\nleftover text\n", "only the ID"},
		{"bad task line", "+++\nid = \"x\"\n+++\n## Tasks\n\n- [y] broken\n", "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseChange("test.md", tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("parseChange error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

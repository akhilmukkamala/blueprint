package cli

// Verifierpacks feature: `blueprint new` materializes the scenario SKILL.md
// playbook on first use of the scenario. Reuses the runNew/tempRepo harness
// from routecmds_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewMaterializesScenarioPlaybook(t *testing.T) {
	dir := tempRepo(t)
	fixedNow(t)

	out, err := runNew(t, dir, "new", "fix crash on empty cart", "--type", "fix", "--paths", "src/cart.go")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	skill := filepath.Join(dir, ".blueprint", "skills", "bug-fix", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("bug-fix playbook was not materialized: %v", err)
	}
	if !strings.HasPrefix(string(b), "---\nname: bug-fix\n") {
		t.Errorf("playbook frontmatter wrong:\n%s", string(b)[:80])
	}
	if !strings.Contains(out, ".blueprint/skills/bug-fix/SKILL.md") {
		t.Errorf("human output should mention the materialized playbook, got:\n%s", out)
	}

	// User edits survive re-use of the scenario: Materialize never overwrites.
	if err := os.WriteFile(skill, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runNew(t, dir, "new", "fix another bug", "--type", "fix", "--paths", "src/cart.go"); err != nil {
		t.Fatalf("second new: %v", err)
	}
	if got, _ := os.ReadFile(skill); string(got) != "mine" {
		t.Errorf("playbook was overwritten on second use: %q", string(got))
	}
}

func TestNewExemptDoesNotMaterializePlaybook(t *testing.T) {
	dir := tempRepo(t)
	fixedNow(t)
	// tempRepo's registry exempts docs-only markdown changes: no scaffold,
	// so no playbook either (exempt = one worklog line, DESIGN §4).
	if _, err := runNew(t, dir, "new", "fix typo", "--type", "docs", "--paths", "README.md"); err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "skills")); !os.IsNotExist(err) {
		t.Errorf("exempt change must not materialize playbooks (stat err=%v)", err)
	}
}

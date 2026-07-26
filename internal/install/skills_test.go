package install

// Tests for the bundled scenario playbooks and registry defaults
// (verifierpacks feature).

import (
	"strings"
	"testing"

	"blueprint/internal/route"
)

func TestScenarioSkillMaterialization(t *testing.T) {
	root := t.TempDir()
	for _, s := range SkillScenarios {
		created, err := MaterializeScenarioSkill(root, s)
		if err != nil || !created {
			t.Fatalf("MaterializeScenarioSkill(%s): created=%v err=%v", s, created, err)
		}
		content := read(t, root, SkillPath(s))
		// Agent Skills frontmatter: name + description only.
		if !strings.HasPrefix(content, "---\nname: "+s+"\n") {
			t.Errorf("%s: frontmatter must open with name: %s", s, s)
		}
		if !strings.Contains(content, "\ndescription: ") {
			t.Errorf("%s: frontmatter must carry a description", s)
		}
		if !strings.Contains(content, "blueprint ") {
			t.Errorf("%s: playbook body must reference blueprint commands", s)
		}

		// Second call is a no-op: the file is [user]-tier once it lands.
		created, err = MaterializeScenarioSkill(root, s)
		if err != nil || created {
			t.Fatalf("second MaterializeScenarioSkill(%s) must be a no-op: created=%v err=%v", s, created, err)
		}
	}

	// Scenario workflow content spot-checks (DESIGN §5 loop shapes).
	for path, want := range map[string]string{
		SkillPath("bug-fix"):     "red",
		SkillPath("refactor"):    "characterization",
		SkillPath("performance"): "benchmark",
	} {
		if !strings.Contains(strings.ToLower(read(t, root, path)), want) {
			t.Errorf("%s should mention %q", path, want)
		}
	}
}

func TestMaterializeScenarioSkillUnknownScenarioIsNoop(t *testing.T) {
	root := t.TempDir()
	created, err := MaterializeScenarioSkill(root, "sev1-hotfix")
	if err != nil || created {
		t.Fatalf("scenario without a bundled playbook must be (false, nil), got (%v, %v)", created, err)
	}
	if exists(root, SkillPath("sev1-hotfix")) {
		t.Fatal("no file should be written for a scenario without a template")
	}
}

// TestBundledRegistryDefaultsParse proves the shipped registry template loads
// through the real router loader and its classes match as intended.
func TestBundledRegistryDefaultsParse(t *testing.T) {
	root := t.TempDir()
	if _, err := Materialize(root, ".blueprint/registry.toml"); err != nil {
		t.Fatal(err)
	}
	reg, err := route.LoadRegistry(root)
	if err != nil {
		t.Fatalf("bundled registry failed the real loader: %v", err)
	}
	names := map[string]bool{}
	for _, c := range reg.Classes {
		names[c.Name] = true
	}
	for _, want := range []string{"docs-only", "formatting-only", "dep-patch-bump"} {
		if !names[want] {
			t.Errorf("bundled registry missing default class %q", want)
		}
	}

	cases := []struct {
		name       string
		changeType string
		paths      []string
		loc        int
		wantClass  string
	}{
		{"nested markdown", "docs", []string{"docs/guide/setup.md", "README.md"}, 40, "docs-only"},
		{"docs too large", "docs", []string{"README.md"}, 500, ""},
		{"formatting pass", "style", []string{"src/app.go", "src/util.go"}, 150, "formatting-only"},
		{"lockfile bump", "chore", []string{"go.mod", "go.sum"}, 4000, "dep-patch-bump"},
		{"code sneaking into a dep bump", "chore", []string{"go.sum", "src/main.go"}, 10, ""},
		{"feature never exempt", "feat", []string{"src/main.go"}, 10, ""},
	}
	for _, c := range cases {
		got := reg.Match(c.changeType, c.paths, c.loc)
		switch {
		case c.wantClass == "" && got != nil:
			t.Errorf("%s: matched %q, want no exemption", c.name, got.Name)
		case c.wantClass != "" && (got == nil || got.Name != c.wantClass):
			t.Errorf("%s: got %v, want class %q", c.name, got, c.wantClass)
		}
	}
}

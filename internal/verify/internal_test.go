package verify

// White-box tests for the small pure helpers.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"checkov -d infra", []string{"checkov", "-d", "infra"}},
		{`promptfoo eval -c "my config.yaml"`, []string{"promptfoo", "eval", "-c", "my config.yaml"}},
		{`'/path with space/bin' --flag`, []string{"/path with space/bin", "--flag"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{`empty ""`, []string{"empty", ""}},
		{"", nil},
	}
	for _, c := range cases {
		if got := splitCommand(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitCommand(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestAppliesTo(t *testing.T) {
	cases := []struct {
		scopes   []string
		scenario string
		want     bool
	}{
		{[]string{"infra"}, "infra", true},
		{[]string{"infra"}, "app-code", false},
		{[]string{"*"}, "anything", true},
		{nil, "infra", false}, // empty scope never auto-runs
	}
	for _, c := range cases {
		v := Verifier{AppliesTo: c.scopes}
		if got := v.appliesTo(c.scenario); got != c.want {
			t.Errorf("appliesTo(%v, %q) = %v, want %v", c.scopes, c.scenario, got, c.want)
		}
	}
}

func TestLoadVerifiersValidation(t *testing.T) {
	root := t.TempDir()

	// Missing file: valid, no verifiers.
	vs, err := loadVerifiers(root)
	if err != nil || vs != nil {
		t.Fatalf("missing verifiers.toml should be (nil, nil), got %v, %v", vs, err)
	}

	write := func(content string) {
		t.Helper()
		p := filepath.Join(root, ".blueprint", "verifiers.toml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("[[verifier]]\nname = \"x\"\n") // command missing
	if _, err := loadVerifiers(root); err == nil || !strings.Contains(err.Error(), "command are required") {
		t.Fatalf("want required-field remediation, got %v", err)
	}

	write("not toml at all [")
	if _, err := loadVerifiers(root); err == nil || !strings.Contains(err.Error(), "[[verifier]]") {
		t.Fatalf("want TOML remediation, got %v", err)
	}
}

func TestFindSkipMarkers(t *testing.T) {
	cases := []struct {
		content string
		hit     bool
	}{
		{"func TestX(t *testing.T) { t.Skip(\"later\") }", true},
		{"it.only('works', () => {})", true},
		{"xit('disabled', () => {})", true},
		{"@pytest.mark.skip\ndef test_x(): pass", true},
		{"#[ignore]\nfn test_x() {}", true},
		{"func TestX(t *testing.T) { t.Fatal(\"no\") }", false},
		{"the benefit(of, doubt)", false}, // 'fit(' inside a word is not a marker
	}
	for _, c := range cases {
		if got := len(findSkipMarkers(c.content)) > 0; got != c.hit {
			t.Errorf("findSkipMarkers(%q) hit=%v, want %v", c.content, got, c.hit)
		}
	}
}

func TestCompositeHashDeterministic(t *testing.T) {
	a := &Lock{Files: map[string]string{"b": "2", "a": "1"}, CapsHash: "c", SpecLinkedTests: 2}
	b := &Lock{Files: map[string]string{"a": "1", "b": "2"}, CapsHash: "c", SpecLinkedTests: 2}
	if compositeHash(a) != compositeHash(b) {
		t.Fatal("composite hash must be independent of map iteration order")
	}
	c := &Lock{Files: map[string]string{"a": "1", "b": "2"}, CapsHash: "c", SpecLinkedTests: 1}
	if compositeHash(a) == compositeHash(c) {
		t.Fatal("test-count change must change the composite")
	}
}

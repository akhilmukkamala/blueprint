package route

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
)

var testNow = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func TestChangeID(t *testing.T) {
	cases := []struct{ intent, want string }{
		{"Fix login redirect loop", "2026-07-21-fix-login-redirect-loop"},
		{"  weird!!chars??  ", "2026-07-21-weird-chars"},
		{"", "2026-07-21-change"},
		{strings.Repeat("very long intent ", 10), "2026-07-21-very-long-intent-very-long-intent-very-long-inte"},
	}
	for _, c := range cases {
		if got := ChangeID(c.intent, testNow); got != c.want {
			t.Errorf("ChangeID(%q) = %q, want %q", c.intent, got, c.want)
		}
	}
}

func TestUniqueChangeIDCollision(t *testing.T) {
	root := t.TempDir()
	first := UniqueChangeID(root, "same intent", testNow)
	if err := os.MkdirAll(strings.TrimSuffix(DefaultChangePath(root, first), "change.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	second := UniqueChangeID(root, "same intent", testNow)
	if second == first || !strings.HasPrefix(second, first) {
		t.Errorf("collision handling: first=%q second=%q", first, second)
	}
}

// frontmatter re-parses the +++-fenced TOML so the scaffold stays loadable by
// the spec package (same change.md contract).
func frontmatter(t *testing.T, path string) (map[string]any, string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "+++", 3)
	if len(parts) != 3 {
		t.Fatalf("change.md must have a +++ TOML fence, got:\n%s", raw)
	}
	var meta map[string]any
	if err := toml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		t.Fatalf("frontmatter is not valid TOML: %v\n%s", err, parts[1])
	}
	return meta, parts[2]
}

func TestWriteChangeFileLight(t *testing.T) {
	root := t.TempDir()
	c := &core.Change{
		ID:       "2026-07-21-fix-login",
		Title:    "fix login",
		Type:     "fix",
		Tier:     core.TierLight,
		Status:   core.StatusDraft,
		Scenario: "bug-fix",
		Contract: DefaultContract("2026-07-21-fix-login", core.TierLight),
	}
	if err := WriteChangeFile(root, c); err != nil {
		t.Fatal(err)
	}
	meta, body := frontmatter(t, DefaultChangePath(root, c.ID))
	if meta["id"] != c.ID || meta["tier"] != "light" || meta["status"] != "draft" {
		t.Errorf("frontmatter identity wrong: %v", meta)
	}
	loop, ok := meta["loop"].(map[string]any)
	if !ok || loop["predicate"] != "blueprint verify 2026-07-21-fix-login" {
		t.Errorf("loop contract missing or wrong: %v", meta["loop"])
	}
	if _, ok := loop["breaker"].(map[string]any); !ok {
		t.Errorf("breaker table missing: %v", loop)
	}
	if strings.Contains(body, "## Design") {
		t.Error("light tier must not scaffold a Design section")
	}
	if !strings.Contains(body, "## Delta") || !strings.Contains(body, "## Tasks") {
		t.Error("Delta/Tasks sections missing")
	}
}

func TestWriteChangeFileFullAndSev1(t *testing.T) {
	root := t.TempDir()
	sla := testNow.Add(72 * time.Hour)
	c := &core.Change{
		ID:       "2026-07-21-hotfix",
		Title:    "hotfix payments outage",
		Type:     "fix",
		Tier:     core.TierFull,
		Status:   core.StatusBackfill,
		Scenario: "sev1-hotfix",
		Contract: DefaultContract("2026-07-21-hotfix", core.TierFull),
		SLA:      &sla,
	}
	if err := WriteChangeFile(root, c); err != nil {
		t.Fatal(err)
	}
	meta, body := frontmatter(t, DefaultChangePath(root, c.ID))
	if meta["status"] != string(core.StatusBackfill) {
		t.Errorf("status = %v, want backfill-due", meta["status"])
	}
	if meta["sla"] != "2026-07-24T12:00:00Z" {
		t.Errorf("sla = %v", meta["sla"])
	}
	if !strings.Contains(body, "## Design") {
		t.Error("full tier must scaffold a Design section")
	}
}

func TestWriteChangeFileRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	c := &core.Change{ID: "dup", Title: "x", Tier: core.TierLight, Status: core.StatusDraft,
		Contract: DefaultContract("dup", core.TierLight)}
	if err := WriteChangeFile(root, c); err != nil {
		t.Fatal(err)
	}
	err := WriteChangeFile(root, c)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("overwrite must be refused with remediation, got %v", err)
	}
}

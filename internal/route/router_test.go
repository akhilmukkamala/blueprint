package route

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

func fixtureRouter(t *testing.T) *Router {
	t.Helper()
	r, err := Load(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	return r
}

func reasonFor(d Decision, axis string) (Reason, bool) {
	for _, r := range d.Reasons {
		if r.Axis == axis {
			return r, true
		}
	}
	return Reason{}, false
}

func TestDecideTable(t *testing.T) {
	r := fixtureRouter(t)
	cases := []struct {
		name     string
		in       Inputs
		want     core.CeremonyTier
		wantAxis string // axis expected to carry the deciding tier
	}{
		{
			name:     "default is light",
			in:       Inputs{ChangeType: "feat", Paths: []string{"src/a.go"}, ChangedLOC: 10},
			want:     core.TierLight,
			wantAxis: "default",
		},
		{
			name:     "registry match exempts docs",
			in:       Inputs{ChangeType: "docs", Paths: []string{"README.md", "docs/intro.md"}, ChangedLOC: 50},
			want:     core.TierExempt,
			wantAxis: "registry",
		},
		{
			name: "registry miss when one path outside class",
			in:   Inputs{ChangeType: "docs", Paths: []string{"README.md", "src/a.go"}, ChangedLOC: 50},
			want: core.TierLight,
		},
		{
			name: "registry miss when over class max_loc",
			in:   Inputs{ChangeType: "docs", Paths: []string{"README.md"}, ChangedLOC: 500},
			want: core.TierFull, // also over escalate_loc 400
		},
		{
			name:     "sensitive path floors at full and overrides registry",
			in:       Inputs{ChangeType: "docs", Paths: []string{"auth/README.md"}, ChangedLOC: 5},
			want:     core.TierFull,
			wantAxis: "sensitive",
		},
		{
			name:     "changed LOC over threshold escalates",
			in:       Inputs{ChangeType: "feat", Paths: []string{"src/a.go"}, ChangedLOC: 400},
			want:     core.TierFull,
			wantAxis: "loc",
		},
		{
			name:     "one-way reversibility bumps light to full",
			in:       Inputs{ChangeType: "feat", Paths: []string{"migrations/002_add.sql"}, ChangedLOC: 20},
			want:     core.TierFull,
			wantAxis: "reversibility",
		},
		{
			name: "two-way reversibility class does not bump",
			in:   Inputs{ChangeType: "feat", Paths: []string{"flags/new.toml"}, ChangedLOC: 20},
			want: core.TierLight,
		},
		{
			name:     "blast radius over threshold escalates",
			in:       Inputs{ChangeType: "feat", Paths: manyPaths(12), ChangedLOC: 30},
			want:     core.TierFull,
			wantAxis: "blast-radius",
		},
		{
			name: "no paths, no LOC routes light",
			in:   Inputs{ChangeType: "feat"},
			want: core.TierLight,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := r.Decide(c.in)
			if d.Tier != c.want {
				t.Fatalf("tier = %s, want %s; reasons: %v", d.Tier, c.want, reasonStrings(d.Reasons))
			}
			if d.Cost.Tier != c.want || len(d.Cost.Artifacts) == 0 || d.Cost.Tokens == 0 {
				t.Errorf("cost not projected for %s: %+v", c.want, d.Cost)
			}
			if c.wantAxis != "" {
				rs, ok := reasonFor(d, c.wantAxis)
				if !ok || rs.Tier != c.want {
					t.Errorf("expected axis %q to argue %s; reasons: %v", c.wantAxis, c.want, reasonStrings(d.Reasons))
				}
			}
			// Every axis reports its contribution, escalating or not.
			for _, axis := range []string{"registry", "blast-radius", "loc", "reversibility", "sensitive"} {
				if _, ok := reasonFor(d, axis); !ok {
					t.Errorf("axis %q missing from reasons", axis)
				}
			}
		})
	}
}

func manyPaths(n int) []string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = filepath.ToSlash(filepath.Join("src", "pkg", "f"+string(rune('a'+i))+".go"))
	}
	return ps
}

func TestDecideIsDeterministic(t *testing.T) {
	r := fixtureRouter(t)
	in := Inputs{ChangeType: "feat", Paths: []string{"b.go", "a.go", "b.go"}, ChangedLOC: 12}
	d1, d2 := r.Decide(in), r.Decide(in)
	if d1.Tier != d2.Tier || len(d1.Reasons) != len(d2.Reasons) {
		t.Fatalf("same inputs produced different decisions: %+v vs %+v", d1, d2)
	}
	for i := range d1.Reasons {
		if d1.Reasons[i] != d2.Reasons[i] {
			t.Errorf("reason %d differs: %+v vs %+v", i, d1.Reasons[i], d2.Reasons[i])
		}
	}
}

func TestDecideDefaultsWithoutConfigFiles(t *testing.T) {
	r, err := Load(t.TempDir()) // no .blueprint at all
	if err != nil {
		t.Fatalf("Load on empty repo: %v", err)
	}
	if r.Config.EscalateLOC != 400 || !r.Config.EscalateOnSensitiveTouch || r.Config.EscalateRadius != DefaultRadiusEscalate {
		t.Errorf("defaults wrong: %+v", r.Config)
	}
	d := r.Decide(Inputs{ChangeType: "feat", Paths: []string{"a.go"}, ChangedLOC: 10})
	if d.Tier != core.TierLight {
		t.Errorf("empty config must still route light, got %s", d.Tier)
	}
}

func TestSensitiveEscalationCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".blueprint", "config.toml"),
		"[router]\nescalate_on_sensitive_touch = false\n")
	mustWrite(t, filepath.Join(root, ".blueprint", "safety.toml"),
		"[safety]\nsensitive = [\"auth/**\"]\n")
	r, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	d := r.Decide(Inputs{ChangeType: "feat", Paths: []string{"auth/login.go"}, ChangedLOC: 5})
	if d.Tier != core.TierLight {
		t.Errorf("with escalation disabled sensitive touch stays light, got %s", d.Tier)
	}
}

func TestReevaluateNeverDeescalates(t *testing.T) {
	r := fixtureRouter(t)
	root := t.TempDir()
	c := &core.Change{ID: "c-full", Type: "feat", Tier: core.TierFull}
	d, escalated, err := r.Reevaluate(root, c, DiffStats{Paths: []string{"src/a.go"}, ChangedLOC: 5})
	if err != nil {
		t.Fatal(err)
	}
	if escalated || d.Tier != core.TierFull || c.Tier != core.TierFull {
		t.Errorf("de-escalation must not happen: tier=%s escalated=%v", d.Tier, escalated)
	}
	evs, _, _ := worklog.Read(root)
	if len(evs) != 0 {
		t.Errorf("staying put must not log, got %d events", len(evs))
	}
}

func TestReevaluateEscalatesAndLogs(t *testing.T) {
	r := fixtureRouter(t)
	root := t.TempDir()
	c := &core.Change{ID: "c-light", Type: "feat", Tier: core.TierLight}
	d, escalated, err := r.Reevaluate(root, c, DiffStats{Paths: []string{"src/a.go"}, ChangedLOC: 450})
	if err != nil {
		t.Fatal(err)
	}
	if !escalated || d.Tier != core.TierFull {
		t.Fatalf("expected escalation to full, got tier=%s escalated=%v", d.Tier, escalated)
	}
	if c.Tier != core.TierFull {
		t.Errorf("change tier not updated: %s", c.Tier)
	}
	evs, warns, err := worklog.Read(root)
	if err != nil || len(warns) != 0 {
		t.Fatalf("worklog read: %v %v", err, warns)
	}
	if len(evs) != 1 || evs[0].Kind != "route-escalate" || evs[0].ChangeID != "c-light" {
		t.Fatalf("escalation must append one route-escalate event, got %+v", evs)
	}
	if evs[0].Data["from"] != "light" || evs[0].Data["to"] != "full" {
		t.Errorf("event data = %v", evs[0].Data)
	}
}

func TestCostLinePerTier(t *testing.T) {
	for _, tier := range []core.CeremonyTier{core.TierExempt, core.TierLight, core.TierFull} {
		c := CostFor(tier)
		line := c.Line()
		if !strings.Contains(line, string(tier)) || !strings.Contains(line, "tokens") {
			t.Errorf("cost line for %s malformed: %q", tier, line)
		}
	}
	if CostFor(core.TierFull).Tokens <= CostFor(core.TierLight).Tokens ||
		CostFor(core.TierLight).Tokens <= CostFor(core.TierExempt).Tokens {
		t.Error("token estimates must grow with ceremony")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

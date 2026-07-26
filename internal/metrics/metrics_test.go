package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// t0 anchors every fixture timestamp; folds must be pure functions of the
// seeded journals (CONTRACTS rule 5), so tests inject clock and git.
var t0 = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ev(t time.Time, kind, changeID string, data map[string]any) string {
	m := map[string]any{"time": t.Format(time.RFC3339), "kind": kind}
	if changeID != "" {
		m["change_id"] = changeID
	}
	if data != nil {
		m["data"] = data
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func changeMD(id, tier, status string) string {
	return fmt.Sprintf("+++\nid = %q\ntitle = \"t\"\ntype = \"fix\"\ntier = %q\nstatus = %q\nscenario = \"bug\"\n+++\n\n## Tasks\n- [x] T1: done\n", id, tier, status)
}

// seedRepo builds the full fixture: worklog with adoption + routing history,
// one active verified change with cost events, one archived change whose
// files a later fix commit touches, knowledge dir, map.json, baselines.
func seedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ns := filepath.Join(root, ".blueprint")

	writeLines(t, filepath.Join(ns, "log", "worklog.ndjson"),
		ev(t0, "adopt-stage", "", map[string]any{"stage": 0}),
		ev(t0.Add(30*time.Minute), "adopt-stage", "", map[string]any{"stage": 1}),
		ev(t0.Add(2*time.Hour), "adopt-stage", "", map[string]any{"stage": 4}),
		ev(t0.Add(1*time.Minute), "init", "", map[string]any{"duration_seconds": 42.5}),
		ev(t0.Add(3*time.Hour), "route", "chg-active", map[string]any{"tier": "light"}),
		ev(t0.Add(4*time.Hour), "route", "chg-arch", map[string]any{"tier": "full"}),
		ev(t0.Add(5*time.Hour), "route", "", map[string]any{"tier": "exempt"}),
		ev(t0.Add(5*time.Hour), "override", "", map[string]any{"tier": "light", "reason": "touches auth"}),
		ev(t0.Add(30*time.Hour), "close", "chg-arch", map[string]any{"areas": []string{"auth"}}),
	)

	// Active change: light tier, green verdict, cost events.
	writeLines(t, filepath.Join(ns, "changes", "chg-active", "change.md"), changeMD("chg-active", "light", "verified"))
	writeLines(t, filepath.Join(ns, "changes", "chg-active", "journal.ndjson"),
		ev(t0.Add(6*time.Hour), "iteration", "chg-active", map[string]any{"n": 1, "files": []string{"a.go"}, "usd": 0.5}),
		ev(t0.Add(7*time.Hour), "cost", "chg-active", map[string]any{"usd": 1.5, "tokens": 2000}),
		ev(t0.Add(8*time.Hour), "verdict", "chg-active", map[string]any{"pass": true}),
	)

	// Archived change: full tier, green verdict, files that get "fixed" later.
	arch := filepath.Join(ns, "archive", "chg-arch")
	writeLines(t, filepath.Join(arch, "change.md"), changeMD("chg-arch", "full", "closed"))
	writeLines(t, filepath.Join(arch, "journal.ndjson"),
		ev(t0.Add(10*time.Hour), "iteration", "chg-arch", map[string]any{"n": 1, "files": []string{"pkg/auth.go", "pkg/auth_test.go"}}),
		ev(t0.Add(11*time.Hour), "cost", "chg-arch", map[string]any{"usd": 4.0, "tokens": 8000}),
		ev(t0.Add(12*time.Hour), "verdict", "chg-arch", map[string]any{"pass": true}),
		ev(t0.Add(12*time.Hour), "incident", "chg-arch", map[string]any{"sev": 2}),
	)

	writeLines(t, filepath.Join(ns, "knowledge", "glossary.md"), "# Glossary")
	writeLines(t, filepath.Join(ns, "map.json"), "{}")
	writeLines(t, filepath.Join(ns, "baselines.json"), `{"rework_rate": 0.4, "not_a_metric": 1}`)
	return root
}

// fakeGit serves the two git calls the folds make.
func fakeGit(lastCommit time.Time, log string) GitRunner {
	return func(repoRoot string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "log" && args[1] == "-1" {
			return fmt.Sprintf("%d\n", lastCommit.Unix()), nil
		}
		return log, nil
	}
}

func computeFixture(t *testing.T) *Report {
	t.Helper()
	root := seedRepo(t)
	// One rework commit: a fix touching the archived change's file 2 days
	// after close; one unrelated fix outside the 30d window's file set.
	gitLog := "\x01aaa111\t" + fmt.Sprint(t0.Add(78*time.Hour).Unix()) + "\tfix: auth regression\n\npkg/auth.go\n" +
		"\x01bbb222\t" + fmt.Sprint(t0.Add(80*time.Hour).Unix()) + "\tfeat: unrelated\n\nother.go\n"
	r, err := Compute(root, Options{
		Now: func() time.Time { return t0.Add(100 * time.Hour) },
		Git: fakeGit(t0.Add(90*time.Hour), gitLog),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestComputeEmitsEveryKey(t *testing.T) {
	r := computeFixture(t)
	if len(r.Metrics) != len(Keys) {
		t.Fatalf("got %d metrics, want %d", len(r.Metrics), len(Keys))
	}
	for _, k := range Keys {
		v, ok := r.Metrics[k]
		if !ok {
			t.Errorf("metric %q missing — every DESIGN 15 row must be present", k)
			continue
		}
		if v.Value == nil && v.Reason == "" {
			t.Errorf("metric %q is null without a reason — silent omission is forbidden", k)
		}
	}
}

func TestMeasuredFolds(t *testing.T) {
	r := computeFixture(t)
	tests := []struct {
		key  string
		want float64
	}{
		// stage-0 at t0 -> first green verdict at t0+8h.
		{"time_to_first_verified_change", 8 * 3600},
		{"time_to_install", 42.5},
		// stage 0 -> stage 4 spans 2h.
		{"time_to_onboard", 2 * 3600},
		// 3 routes, 1 override.
		{"ceremony_fit", 1 - 1.0/3.0},
		// 1 archived change, reworked by the fix commit.
		{"rework_rate", 1.0},
		// 1 incident / 2 verified changes.
		{"escaped_defect_rate", 0.5},
	}
	for _, tc := range tests {
		v := r.Metrics[tc.key]
		got, ok := v.Value.(float64)
		if !ok {
			t.Errorf("%s: value = %v (reason %q), want float", tc.key, v.Value, v.Reason)
			continue
		}
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s = %g, want %g", tc.key, got, tc.want)
		}
	}

	if got := r.Metrics["supervision_ratio"].Value; got != 1 {
		t.Errorf("supervision_ratio = %v, want 1 active loop", got)
	}
	// map.json mtime is "now-ish" (just written), last commit t0+90h: lag negative (map fresher).
	if _, ok := r.Metrics["index_freshness"].Value.(float64); !ok {
		t.Errorf("index_freshness should be measured, got %+v", r.Metrics["index_freshness"])
	}
	if got := r.Metrics["knowledge_health"].Value; got != 1 {
		t.Errorf("knowledge_health = %v, want 1 artifact", got)
	}
}

func TestCostPerVerifiedGroupsByTier(t *testing.T) {
	r := computeFixture(t)
	buckets, ok := r.Metrics["cost_per_verified_change"].Value.(map[string]costBucket)
	if !ok {
		t.Fatalf("cost value = %#v, want per-tier buckets", r.Metrics["cost_per_verified_change"].Value)
	}
	light := buckets["light"]
	if light.Changes != 1 || light.USD != 2.0 || light.Tokens != 2000 {
		t.Errorf("light bucket = %+v, want 1 change, $2.00, 2000 tokens", light)
	}
	full := buckets["full"]
	if full.Changes != 1 || full.USD != 4.0 || full.USDPer != 4.0 {
		t.Errorf("full bucket = %+v, want 1 change at $4.00", full)
	}
}

func TestBaselineMergedOnlyOntoKnownKeys(t *testing.T) {
	r := computeFixture(t)
	if got := r.Metrics["rework_rate"].Baseline; got != 0.4 {
		t.Errorf("rework_rate baseline = %v, want 0.4", got)
	}
	if _, ok := r.Metrics["not_a_metric"]; ok {
		t.Error("unknown baseline key must not create a metric row")
	}
}

func TestEmptyRepoIsAllNullWithReasons(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Compute(root, Options{
		Now: func() time.Time { return t0 },
		Git: func(string, ...string) (string, error) { return "", fmt.Errorf("no commits") },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range Keys {
		v := r.Metrics[k]
		if k == "supervision_ratio" { // zero active loops is a real measurement
			if v.Value != 0 {
				t.Errorf("supervision_ratio on empty repo = %v, want 0", v.Value)
			}
			continue
		}
		if v.Value != nil {
			t.Errorf("%s on empty repo = %v, want null", k, v.Value)
		}
		if v.Reason == "" {
			t.Errorf("%s on empty repo has no reason string", k)
		}
	}
}

func TestReworkRateNoFileDataCannotMatch(t *testing.T) {
	root := t.TempDir()
	ns := filepath.Join(root, ".blueprint")
	writeLines(t, filepath.Join(ns, "log", "worklog.ndjson"),
		ev(t0, "close", "chg-x", nil))
	arch := filepath.Join(ns, "archive", "chg-x")
	writeLines(t, filepath.Join(arch, "change.md"), changeMD("chg-x", "light", "closed"))
	writeLines(t, filepath.Join(arch, "journal.ndjson"),
		ev(t0.Add(-time.Hour), "verdict", "chg-x", map[string]any{"pass": true}))
	gitLog := "\x01ccc333\t" + fmt.Sprint(t0.Add(24*time.Hour).Unix()) + "\tfix: something\n\nany.go\n"
	r, err := Compute(root, Options{
		Now: func() time.Time { return t0.Add(48 * time.Hour) },
		Git: fakeGit(t0, gitLog),
	})
	if err != nil {
		t.Fatal(err)
	}
	v := r.Metrics["rework_rate"]
	if v.Value != 0.0 {
		t.Errorf("rework_rate = %v, want 0 (no file data to match)", v.Value)
	}
	if v.Detail["changes_without_file_data"] != 1 {
		t.Errorf("detail should flag 1 change without file data, got %v", v.Detail)
	}
}

func TestWriteReport(t *testing.T) {
	root := seedRepo(t)
	r, err := Compute(root, Options{
		Now: func() time.Time { return t0 },
		Git: fakeGit(t0, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteReport(root, r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ReportPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var round Report
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("metrics.json is not valid JSON: %v", err)
	}
	if len(round.Metrics) != len(Keys) {
		t.Errorf("round-tripped report has %d metrics, want %d", len(round.Metrics), len(Keys))
	}
}

func TestCorruptJournalLineIsSkippedNotFatal(t *testing.T) {
	root := t.TempDir()
	ns := filepath.Join(root, ".blueprint")
	writeLines(t, filepath.Join(ns, "changes", "chg-y", "change.md"), changeMD("chg-y", "light", "draft"))
	writeLines(t, filepath.Join(ns, "changes", "chg-y", "journal.ndjson"),
		ev(t0, "iteration", "chg-y", map[string]any{"n": 1}),
		`{not json`,
		ev(t0.Add(time.Hour), "verdict", "chg-y", map[string]any{"pass": true}))
	src, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Changes) != 1 || len(src.Changes[0].Events) != 2 {
		t.Fatalf("want 1 change with 2 parsed events, got %+v", src.Changes)
	}
}

func TestFormatProm(t *testing.T) {
	r := computeFixture(t)
	out := FormatProm(r)
	for _, want := range []string{
		"# TYPE blueprint_ceremony_fit gauge",
		"blueprint_time_to_install 42.5",
		`blueprint_cost_per_verified_change_usd{tier="light"} 2`,
		`blueprint_cost_per_verified_change_tokens{tier="full"} 8000`,
		"# blueprint_self_improvement_velocity unavailable:",
		"# blueprint_upgrade_success unavailable:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prom output missing %q\n---\n%s", want, out)
		}
	}
}

func TestFormatHumanShowsReasons(t *testing.T) {
	r := computeFixture(t)
	out := FormatHuman(r)
	if !strings.Contains(out, "dream") {
		t.Errorf("human output should surface the self-improvement null reason\n%s", out)
	}
	if !strings.Contains(out, "[baseline 0.4]") {
		t.Errorf("human output should show the merged baseline\n%s", out)
	}
}

func TestCorruptBaselinesIsRemediationError(t *testing.T) {
	root := t.TempDir()
	ns := filepath.Join(root, ".blueprint")
	writeLines(t, filepath.Join(ns, "baselines.json"), "{broken")
	_, err := Compute(root, Options{
		Now: func() time.Time { return t0 },
		Git: fakeGit(t0, ""),
	})
	if err == nil || !strings.Contains(err.Error(), "baselines.json") {
		t.Fatalf("want remediation error naming baselines.json, got %v", err)
	}
}

package bench

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func writeSuite(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "retrieval.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validSuite = `
name = "s"

[[tasks]]
id = "T1"
prompt = "where is the router decision"
expected_files = ["internal/route/router.go"]
commit_ref = "abc123"

[[tasks]]
id = "T2"
prompt = "which function evaluates the breaker"
expected_symbols = ["EvaluateBreaker"]
commit_ref = "def456"
`

func TestLoadSuiteValid(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 2 || s.Tasks[0].ID != "T1" || s.Tasks[1].ExpectedSymbols[0] != "EvaluateBreaker" {
		t.Fatalf("parsed suite = %+v", s)
	}
}

func TestLoadSuiteRemediationErrors(t *testing.T) {
	tests := []struct {
		name, body, wantHint string
	}{
		{"no tasks", `name = "s"`, "[[tasks]]"},
		{"missing id", "[[tasks]]\nprompt = \"p\"\nexpected_files = [\"f\"]\ncommit_ref = \"c\"\n", "missing id"},
		{"duplicate id", strings.Replace(validSuite, `id = "T2"`, `id = "T1"`, 1), "duplicate id"},
		{"missing prompt", "[[tasks]]\nid = \"T1\"\nexpected_files = [\"f\"]\ncommit_ref = \"c\"\n", "missing prompt"},
		{"no expectations", "[[tasks]]\nid = \"T1\"\nprompt = \"p\"\ncommit_ref = \"c\"\n", "expected_files"},
		{"missing commit ref", "[[tasks]]\nid = \"T1\"\nprompt = \"p\"\nexpected_files = [\"f\"]\n", "post-cutoff"},
		{"bad toml", "[[tasks", "TOML"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadSuite(writeSuite(t, tc.body))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q should mention %q", err, tc.wantHint)
			}
		})
	}
}

func TestLoadSuiteMissingFilePointsAtInit(t *testing.T) {
	_, err := LoadSuite(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("missing suite should point at --init, got %v", err)
	}
}

func TestWriteSampleSuiteRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench", "retrieval.toml")
	if err := WriteSampleSuite(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuite(path); err != nil {
		t.Fatalf("sample suite must parse: %v", err)
	}
	if err := WriteSampleSuite(path); err == nil {
		t.Fatal("second --init must refuse to overwrite")
	}
}

func TestPlanIsFullGridInOrder(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}
	got := Plan(s, DefaultTiers)
	want := []Run{
		{"T1", Tier0Grep}, {"T1", Tier1Map},
		{"T2", Tier0Grep}, {"T2", Tier1Map},
	}
	if len(got) != len(want) {
		t.Fatalf("plan = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("plan[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestManualRunnerRecordThenLastWins(t *testing.T) {
	root := t.TempDir()
	task := Task{ID: "T1", Prompt: "p", ExpectedFiles: []string{"f"}, CommitRef: "c"}
	m := ManualRunner{RepoRoot: root}

	_, err := m.RunTask(task, Tier0Grep)
	if !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("unrecorded run must return ErrNotRecorded, got %v", err)
	}
	if !strings.Contains(err.Error(), "--record-task T1") {
		t.Errorf("pending error should embed the recording instructions: %v", err)
	}

	for _, res := range []Result{
		{TaskID: "T1", Tier: Tier0Grep, Tokens: 100, ToolCalls: 3, WallClockS: 60, Success: false},
		{TaskID: "T1", Tier: Tier0Grep, Tokens: 120, ToolCalls: 4, WallClockS: 70, Success: true},
	} {
		if err := Record(root, res); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.RunTask(task, Tier0Grep)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens != 120 || !got.Success {
		t.Errorf("last recorded entry must win, got %+v", got)
	}
}

func TestRecordRejectsAnonymousResult(t *testing.T) {
	if err := Record(t.TempDir(), Result{}); err == nil {
		t.Fatal("recording without task/tier must fail with remediation")
	}
}

func TestBuildReportShape(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}

	// Only T1/tier0 recorded: no margin, three pending, row fields nil.
	rep := BuildReport(s, DefaultTiers, []Result{
		{TaskID: "T1", Tier: Tier0Grep, Tokens: 100, ToolCalls: 3, WallClockS: 60, Success: true},
	}, now)
	if rep.Margin != nil {
		t.Error("margin must be nil until a task completes in BOTH tiers — a token ratio without a success delta is forbidden")
	}
	if len(rep.Pending) != 3 {
		t.Errorf("pending = %v, want 3 runs", rep.Pending)
	}
	if len(rep.Rows) != 4 {
		t.Fatalf("rows = %d, want full grid of 4", len(rep.Rows))
	}
	for _, row := range rep.Rows {
		if row.Recorded {
			if row.Tokens == nil || row.Success == nil {
				t.Errorf("recorded row %s/%s must carry tokens AND success jointly", row.TaskID, row.Tier)
			}
		} else if row.Tokens != nil || row.Success != nil {
			t.Errorf("pending row %s/%s must not leak partial numbers", row.TaskID, row.Tier)
		}
	}
	if len(rep.Summaries) != 1 || rep.Summaries[0].Tier != Tier0Grep {
		t.Fatalf("summaries = %+v", rep.Summaries)
	}
	if rep.Summaries[0].SuccessRate != 1 || rep.Summaries[0].MeanTokens != 100 {
		t.Errorf("tier0 summary = %+v", rep.Summaries[0])
	}
	if !strings.Contains(rep.Method, "success") {
		t.Error("report method note must state the joint-reporting rule")
	}
}

func TestBuildReportMarginOverPairedTasks(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}
	results := []Result{
		{TaskID: "T1", Tier: Tier0Grep, Tokens: 1000, ToolCalls: 10, WallClockS: 100, Success: true},
		{TaskID: "T1", Tier: Tier1Map, Tokens: 500, ToolCalls: 5, WallClockS: 80, Success: false},
		// T2 recorded in one tier only — must not enter the margin.
		{TaskID: "T2", Tier: Tier1Map, Tokens: 1, ToolCalls: 1, WallClockS: 1, Success: true},
	}
	rep := BuildReport(s, DefaultTiers, results, now)
	m := rep.Margin
	if m == nil {
		t.Fatal("margin should exist: T1 completed in both tiers")
	}
	if m.Tasks != 1 {
		t.Errorf("margin tasks = %d, want 1 (only paired tasks count)", m.Tasks)
	}
	if m.TokenRatio != 0.5 || m.ToolCallRatio != 0.5 || m.WallRatio != 0.8 {
		t.Errorf("ratios = %+v, want 0.5/0.5/0.8", m)
	}
	if m.SuccessDelta != -1.0 {
		t.Errorf("success delta = %g, want -1.0 — cheaper but wrong must be visible", m.SuccessDelta)
	}
}

func TestBuildReportIgnoresUnknownResults(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}
	rep := BuildReport(s, DefaultTiers, []Result{{TaskID: "ghost", Tier: Tier0Grep, Tokens: 9}}, now)
	if len(rep.Rows) != 4 || len(rep.Pending) != 4 {
		t.Errorf("stale results must not invent rows: %+v", rep.Rows)
	}
}

func TestWriteReport(t *testing.T) {
	root := t.TempDir()
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}
	rep := BuildReport(s, DefaultTiers, nil, now)
	if err := WriteReport(root, rep); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ReportPath(root))
	if err != nil {
		t.Fatal(err)
	}
	// The margin key must be present (and null) even when unmeasured — the
	// honest-degradation contract, mirrored from metrics.
	if !strings.Contains(string(raw), `"margin": null`) {
		t.Errorf("report.json should carry an explicit null margin:\n%s", raw)
	}
}

func TestInstructionsCoverProtocol(t *testing.T) {
	task := Task{ID: "T1", Prompt: "find the router", ExpectedFiles: []string{"r.go"}, ExpectedSymbols: []string{"Decide"}, CommitRef: "abc123"}
	for _, tier := range DefaultTiers {
		out := Instructions(task, tier)
		for _, want := range []string{"git checkout abc123", "find the router", "r.go", "Decide", "--record-task T1"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s instructions missing %q\n%s", tier, want, out)
			}
		}
	}
	if !strings.Contains(Instructions(task, Tier1Map), "blueprint map") {
		t.Error("tier1 instructions must say to enable the repo map")
	}
	if !strings.Contains(Instructions(task, Tier0Grep), "ripgrep") {
		t.Error("tier0 instructions must describe the optimized grep baseline")
	}
}

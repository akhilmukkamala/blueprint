package bench

// benchcorpus feature tests: suite auto-generation from the repo's own
// history, query-class heuristics, the dynamic tier grid, the per-class
// report breakdown, and the per-cell manual protocol.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- query-class heuristics ---

func TestTaskClassHeuristics(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want string
	}{
		{"declared class wins", Task{QueryClass: ClassRefactor, ExpectedFiles: []string{"a", "b", "c"}}, ClassRefactor},
		{"single file + camelCase token", Task{Prompt: "where is EvaluateBreaker enforced", ExpectedFiles: []string{"internal/loop/breaker.go"}}, ClassExactSymbol},
		{"single file + expected symbol", Task{Prompt: "find the breaker", ExpectedFiles: []string{"b.go"}, ExpectedSymbols: []string{"EvaluateBreaker"}}, ClassExactSymbol},
		{"single file + snake_case token", Task{Prompt: "who writes journal_event lines", ExpectedFiles: []string{"w.go"}}, ClassExactSymbol},
		{"three files is cross-file", Task{Prompt: "how does routing flow into verification", ExpectedFiles: []string{"a.go", "b.go", "c.go"}}, ClassCrossFile},
		{"refactor wording", Task{Prompt: "refactor the config loading", ExpectedFiles: []string{"a.go", "b.go"}}, ClassRefactor},
		{"rename wording", Task{Prompt: "renamed the tier field everywhere", ExpectedFiles: []string{"a.go"}}, ClassRefactor},
		{"plain question is conceptual", Task{Prompt: "where is the ceremony tier decided", ExpectedFiles: []string{"r.go"}}, ClassConceptual},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TaskClass(tc.task); got != tc.want {
				t.Errorf("TaskClass(%+v) = %q, want %q", tc.task, got, tc.want)
			}
		})
	}
}

func TestLoadSuiteRejectsUnknownQueryClass(t *testing.T) {
	body := "[[tasks]]\nid = \"T1\"\nprompt = \"p\"\nexpected_files = [\"f\"]\ncommit_ref = \"c\"\nquery_class = \"vibes\"\n"
	_, err := LoadSuite(writeSuite(t, body))
	if err == nil || !strings.Contains(err.Error(), "query_class") {
		t.Fatalf("unknown query_class must fail with remediation, got %v", err)
	}
}

// --- dynamic tier grid ---

func TestActiveTiersDefaultsWithoutConfig(t *testing.T) {
	tiers := ActiveTiers(t.TempDir())
	if len(tiers) != 2 || tiers[0] != Tier0Grep || tiers[1] != Tier1Map {
		t.Fatalf("tiers = %v, want defaults only", tiers)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".blueprint", "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestActiveTiersReadsRetrievalConfig(t *testing.T) {
	// Canonical [retrieval] shape (internal/retrieval): non-empty value =
	// enabled; unset tier2_lsp stays out of the grid.
	root := writeConfig(t, `
[retrieval]
tier2_packing = "repomix"

[retrieval.graph]
command = "graph-backend --mcp"
`)
	tiers := ActiveTiers(root)
	want := []Tier{Tier0Grep, Tier1Map, Tier2Packing, Tier3Graph}
	if len(tiers) != len(want) {
		t.Fatalf("tiers = %v, want %v (unset tier2_lsp must stay out)", tiers, want)
	}
	for i := range want {
		if tiers[i] != want[i] {
			t.Errorf("tiers[%d] = %s, want %s", i, tiers[i], want[i])
		}
	}
}

func TestActiveTiersBadTOMLDegradesToDefaults(t *testing.T) {
	// Misconfigured optional tiers must never break the bench (doctor reports
	// the config error); the grid degrades to the always-on baseline pair.
	root := writeConfig(t, "[retrieval\n")
	tiers := ActiveTiers(root)
	if len(tiers) != 2 {
		t.Fatalf("bad TOML must degrade to default grid, got %v", tiers)
	}
}

func TestPlanExpandsDynamicGrid(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, validSuite))
	if err != nil {
		t.Fatal(err)
	}
	tiers := []Tier{Tier0Grep, Tier1Map, Tier2Packing, Tier2LSP, Tier3Graph}
	got := Plan(s, tiers)
	if len(got) != len(s.Tasks)*len(tiers) {
		t.Fatalf("plan = %d cells, want %d", len(got), len(s.Tasks)*len(tiers))
	}
	if got[2].Tier != Tier2Packing || got[2].TaskID != "T1" {
		t.Errorf("cell order must be task-major, tier order preserved: %v", got[:5])
	}
	if !ValidTier("tier3-graph", tiers) || ValidTier("tier3-graph", DefaultTiers) {
		t.Error("ValidTier must be grid-relative: opt-in tiers valid only when configured")
	}
}

// --- per-class report shape ---

const classedSuite = `
name = "s"

[[tasks]]
id = "T1"
prompt = "where is EvaluateBreaker enforced"
expected_files = ["internal/loop/breaker.go"]
commit_ref = "abc"

[[tasks]]
id = "T2"
prompt = "how does verification decide the verdict"
expected_files = ["internal/verify/verify.go"]
commit_ref = "def"
`

func TestBuildReportPerClassBreakdown(t *testing.T) {
	s, err := LoadSuite(writeSuite(t, classedSuite))
	if err != nil {
		t.Fatal(err)
	}
	results := []Result{
		{TaskID: "T1", Tier: Tier0Grep, Tokens: 1000, ToolCalls: 10, WallClockS: 100, Success: true},
		{TaskID: "T1", Tier: Tier1Map, Tokens: 400, ToolCalls: 4, WallClockS: 50, Success: true},
		// T2 (conceptual) recorded in one tier only: class summary yes,
		// class margin no.
		{TaskID: "T2", Tier: Tier0Grep, Tokens: 2000, ToolCalls: 20, WallClockS: 200, Success: false},
	}
	rep := BuildReport(s, DefaultTiers, results, now)

	for _, row := range rep.Rows {
		if row.QueryClass == "" {
			t.Errorf("row %s/%s missing query_class", row.TaskID, row.Tier)
		}
	}

	wantSummaries := map[string]int{
		ClassExactSymbol + "/" + string(Tier0Grep): 1,
		ClassExactSymbol + "/" + string(Tier1Map):  1,
		ClassConceptual + "/" + string(Tier0Grep):  1,
	}
	if len(rep.ClassSummaries) != len(wantSummaries) {
		t.Fatalf("class summaries = %+v, want %d cells", rep.ClassSummaries, len(wantSummaries))
	}
	for _, cs := range rep.ClassSummaries {
		key := cs.QueryClass + "/" + string(cs.Tier)
		if wantSummaries[key] != cs.Recorded {
			t.Errorf("class summary %s recorded = %d, want %d", key, cs.Recorded, wantSummaries[key])
		}
		if cs.MeanTokens > 0 && cs.Recorded == 0 {
			t.Errorf("class summary %s reports tokens without recorded runs (joint-reporting invariant)", key)
		}
	}
	// Success rate travels with every mean: the failing conceptual run must
	// show 0 success beside its 2000 mean tokens.
	for _, cs := range rep.ClassSummaries {
		if cs.QueryClass == ClassConceptual && (cs.SuccessRate != 0 || cs.MeanTokens != 2000) {
			t.Errorf("conceptual/tier0 = %+v, want success 0 with mean tokens 2000", cs)
		}
	}

	if len(rep.ClassMargins) != 1 {
		t.Fatalf("class margins = %+v, want exactly the paired exact-symbol class", rep.ClassMargins)
	}
	cm := rep.ClassMargins[0]
	if cm.QueryClass != ClassExactSymbol || cm.Tasks != 1 {
		t.Errorf("class margin = %+v", cm)
	}
	if cm.TokenRatio != 0.4 || cm.SuccessDelta != 0 {
		t.Errorf("exact-symbol margin = %+v, want token ratio 0.4 with success delta 0", cm)
	}
}

func TestBuildReportHonorsDeclaredClass(t *testing.T) {
	body := strings.Replace(classedSuite, `commit_ref = "abc"`, "commit_ref = \"abc\"\nquery_class = \"refactor\"", 1)
	s, err := LoadSuite(writeSuite(t, body))
	if err != nil {
		t.Fatal(err)
	}
	rep := BuildReport(s, DefaultTiers, []Result{
		{TaskID: "T1", Tier: Tier0Grep, Tokens: 1, ToolCalls: 1, WallClockS: 1, Success: true},
	}, now)
	if len(rep.ClassSummaries) != 1 || rep.ClassSummaries[0].QueryClass != ClassRefactor {
		t.Fatalf("declared query_class must override the heuristic: %+v", rep.ClassSummaries)
	}
}

// --- instructions for opt-in tiers ---

func TestInstructionsForOptInTiers(t *testing.T) {
	task := Task{ID: "T1", Prompt: "p", ExpectedFiles: []string{"f"}, CommitRef: "c"}
	for tier, want := range map[Tier]string{
		Tier2Packing: "retrieval.packing",
		Tier2LSP:     "retrieval.lsp",
		Tier3Graph:   "retrieval.graph",
	} {
		out := Instructions(task, tier)
		if !strings.Contains(out, want) {
			t.Errorf("%s instructions must name the %s config table:\n%s", tier, want, out)
		}
		if !strings.Contains(out, "git checkout c") || !strings.Contains(out, "--record-tier "+string(tier)) {
			t.Errorf("%s instructions must keep the checkout + record protocol:\n%s", tier, out)
		}
	}
}

// --- suite generation from a seeded fixture repo ---

func gitFixture(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		base := []string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}
		out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	return root, git
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const archivedChangeMD = `+++
id = "2026-07-01-add-auth"
title = "Add auth check"
type = "feat"
tier = "full"
status = "closed"
scenario = "greenfield"
+++

# Add auth check

## Delta

### ADDED REQ-auth-001 (event-driven)

WHEN a request arrives, the system SHALL check its token.

verify:
- test: TestAuthCheck
`

func TestGenerateSuiteFromArchiveAndHistory(t *testing.T) {
	root, git := gitFixture(t)

	// Commit 1: initial (root commit — never a history task).
	write(t, root, "internal/auth/handler.go", "package auth\n")
	git("add", ".")
	git("commit", "-q", "-m", "initial layout")
	// Commit 2: a fix commit — becomes a history task pinned to commit 1.
	write(t, root, "internal/parse/parser.go", "package parse\n")
	git("add", ".")
	git("commit", "-q", "-m", "fix: parser drops trailing line")
	fixSHA := git("rev-parse", "HEAD")
	parentSHA := git("rev-parse", "HEAD^")

	// Archived change with journaled route paths + a trace-annotated test.
	write(t, root, ".blueprint/archive/2026-07-01-add-auth/change.md", archivedChangeMD)
	write(t, root, ".blueprint/archive/2026-07-01-add-auth/journal.ndjson",
		`{"kind":"route","change_id":"2026-07-01-add-auth","data":{"paths":["internal/auth/handler.go"]}}`+"\n"+
			"not json — must be skipped\n")
	write(t, root, ".blueprint/log/worklog.ndjson",
		`{"kind":"route","change_id":"2026-07-01-add-auth","data":{"paths":["internal/auth/service.go"]}}`+"\n"+
			`{"kind":"route","change_id":"other-change","data":{"paths":["internal/unrelated/x.go"]}}`+"\n")
	write(t, root, "internal/auth/auth_test.go", "package auth\n\n// verifies: REQ-auth-001\nfunc TestAuthCheck() {}\n")
	// Commit 3: the close-time commit mentioning the change id.
	git("add", ".")
	git("commit", "-q", "-m", "close 2026-07-01-add-auth")
	closeSHA := git("rev-parse", "HEAD")

	s, err := GenerateSuite(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]Task{}
	for _, task := range s.Tasks {
		byID[task.ID] = task
	}

	arch, ok := byID["2026-07-01-add-auth"]
	if !ok {
		t.Fatalf("no task generated from the archived change; got %+v", s.Tasks)
	}
	if !strings.Contains(arch.Prompt, "Add auth check") || !strings.Contains(arch.Prompt, "check its token") {
		t.Errorf("archive prompt must carry title + delta REQ text: %q", arch.Prompt)
	}
	wantFiles := []string{"internal/auth/auth_test.go", "internal/auth/handler.go", "internal/auth/service.go"}
	if strings.Join(arch.ExpectedFiles, ",") != strings.Join(wantFiles, ",") {
		t.Errorf("archive expected_files = %v, want route paths + traced test %v", arch.ExpectedFiles, wantFiles)
	}
	if arch.CommitRef != closeSHA {
		t.Errorf("archive commit_ref = %s, want close commit %s", arch.CommitRef, closeSHA)
	}
	if arch.QueryClass != ClassCrossFile {
		t.Errorf("3 expected files must classify cross-file, got %q", arch.QueryClass)
	}

	hist, ok := byID["g-"+fixSHA[:12]]
	if !ok {
		t.Fatalf("archives are sparse (<5) so the fix commit must become a task; got %+v", s.Tasks)
	}
	if !strings.Contains(hist.Prompt, "parser drops trailing line") {
		t.Errorf("history prompt must carry the subject: %q", hist.Prompt)
	}
	if len(hist.ExpectedFiles) != 1 || hist.ExpectedFiles[0] != "internal/parse/parser.go" {
		t.Errorf("history expected_files = %v, want the commit's diff paths", hist.ExpectedFiles)
	}
	if hist.CommitRef != parentSHA {
		t.Errorf("history commit_ref = %s, want the PARENT %s (pre-fix state)", hist.CommitRef, parentSHA)
	}

	// The close commit says "close ...", not fix/merge — it must not become a
	// history task.
	if _, ok := byID["g-"+closeSHA[:12]]; ok {
		t.Error("non-fix/merge commits must not generate tasks")
	}

	// Round-trip: the generated suite must satisfy the loader's own contract.
	path := filepath.Join(root, ".blueprint", "bench", "retrieval.toml")
	if err := WriteGeneratedSuite(path, s); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuite(path); err != nil {
		t.Fatalf("generated suite must round-trip through LoadSuite: %v", err)
	}
	if err := WriteGeneratedSuite(path, s); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("regeneration must refuse to overwrite, got %v", err)
	}
}

func TestGenerateSuiteDedupesAndCaps(t *testing.T) {
	// Fake git: 60 fix commits, two sharing a subject (dedupe by prompt).
	var log []string
	for i := 0; i < 60; i++ {
		subject := "fix: issue " + string(rune('a'+i%26)) + string(rune('a'+(i/26)))
		if i == 1 {
			subject = "fix: issue aa" // duplicate of i==0
		}
		log = append(log, "sha"+itoa(i)+"\x1f"+"parent"+itoa(i)+"\x1f"+subject)
	}
	fake := func(repoRoot string, args ...string) (string, error) {
		switch args[0] {
		case "log":
			return strings.Join(log, "\n") + "\n", nil
		case "diff-tree":
			return "file-" + args[len(args)-1] + ".go\n", nil
		case "rev-parse":
			return "headsha\n", nil
		}
		return "", nil
	}
	s, err := GenerateSuite(t.TempDir(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 25 {
		t.Fatalf("generated %d tasks, want cap of 25", len(s.Tasks))
	}
	seen := map[string]bool{}
	for _, task := range s.Tasks {
		if seen[task.Prompt] {
			t.Fatalf("duplicate prompt survived dedupe: %q", task.Prompt)
		}
		seen[task.Prompt] = true
		if task.QueryClass == "" {
			t.Errorf("task %s missing query_class", task.ID)
		}
	}
}

func TestGenerateSuiteEmptyRepoIsRemediation(t *testing.T) {
	fake := func(repoRoot string, args ...string) (string, error) {
		return "", os.ErrNotExist
	}
	_, err := GenerateSuite(t.TempDir(), fake)
	if err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("nothing to generate must point at the manual path, got %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

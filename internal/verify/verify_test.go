package verify_test

// End-to-end tests for the verify orchestrator + tamper stack. internal/spec
// is a parallel-branch package, so LoadChange is stubbed here per the
// spec-package contract (tiny local stub in test helpers — never a fake
// internal/spec package). Repos are synthesized in t.TempDir() from
// testdata/ fixtures.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/verify"
)

var fixedNow = func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// testChange builds the in-memory core.Change the stub LoadChange serves.
func testChange(tier core.CeremonyTier) *core.Change {
	return &core.Change{
		ID:       "add-subtraction",
		Title:    "Add subtraction support",
		Type:     "feat",
		Tier:     tier,
		Status:   core.StatusDraft,
		Scenario: "app-code",
		Contract: core.LoopContract{Predicate: "blueprint verify add-subtraction", MaxIterations: 12, MaxUSD: 15},
		Delta: []core.DeltaEntry{
			{Op: core.DeltaAdded, Area: "calc", Requirement: core.Requirement{
				ID: "REQ-calc-001", Pattern: core.PatternEventDriven,
				Text:   "WHEN two operands are added, the system SHALL return their sum.",
				Verify: []core.VerifyMethod{{Kind: "test", Ref: "TestAddCarries"}},
			}},
			{Op: core.DeltaAdded, Area: "calc", Requirement: core.Requirement{
				ID: "REQ-calc-002", Pattern: core.PatternEventDriven,
				Text:   "WHEN one operand is subtracted, the system SHALL return the difference.",
				Verify: []core.VerifyMethod{{Kind: "test", Ref: "TestSubBorrows"}},
			}},
		},
		Tasks: []core.Task{{ID: "T1", Text: "implement subtraction", Done: true}},
	}
}

func stubOpts(c *core.Change) verify.Options {
	return verify.Options{
		Now: fixedNow,
		Hooks: &verify.Hooks{
			LoadChange: func(repoRoot, id string) (*core.Change, error) {
				if id != c.ID {
					return nil, fmt.Errorf("no change %q in .blueprint/changes/; run `blueprint new` first", id)
				}
				return c, nil
			},
		},
	}
}

// scaffoldRepo lays out a minimal .blueprint repo with one spec area and one
// spec-linked test file (2 trace annotations).
func scaffoldRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, ".blueprint/specs/calc/spec.md", readFixture(t, "spec.md.fixture"))
	writeFile(t, root, ".blueprint/changes/add-subtraction/change.md", "# change: add-subtraction\n(frontmatter parsed by internal/spec; hashed verbatim here)\n")
	writeFile(t, root, "calc/calc_test.go", readFixture(t, "calc_test.go.fixture"))
	return root
}

func TestApproveThenVerifyGreen(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)

	lk, err := verify.Approve(root, c.ID, false, opts)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if lk.SpecLinkedTests != 2 {
		t.Fatalf("spec-linked tests = %d, want 2", lk.SpecLinkedTests)
	}
	if len(lk.TestFiles) != 1 || lk.TestFiles[0] != "calc/calc_test.go" {
		t.Fatalf("test files = %v", lk.TestFiles)
	}
	wantHashed := []string{
		".blueprint/changes/add-subtraction/change.md",
		".blueprint/specs/calc/spec.md",
		".blueprint/verifiers.toml",
		"calc/calc_test.go",
	}
	for _, p := range wantHashed {
		if _, ok := lk.Files[p]; !ok {
			t.Errorf("lock missing %s", p)
		}
	}

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Pass || v.Tamper {
		t.Fatalf("want green verdict, got pass=%v tamper=%v checks=%+v", v.Pass, v.Tamper, v.Checks)
	}

	// Verdict written; journal has approve + verdict events.
	if _, err := os.Stat(filepath.Join(root, ".blueprint", "changes", c.ID, "verdict", "verdict.json")); err != nil {
		t.Fatalf("verdict.json: %v", err)
	}
	events := readJournal(t, root, c.ID)
	if len(events) != 2 || events[0].Kind != "approve" || events[1].Kind != "verdict" {
		t.Fatalf("journal events = %+v", events)
	}
}

func TestApproveTwiceNeedsAmend(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)

	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	_, err := verify.Approve(root, c.ID, false, opts)
	if err == nil || !strings.Contains(err.Error(), "--amend") {
		t.Fatalf("second approve should demand --amend, got %v", err)
	}
	if _, err := verify.Approve(root, c.ID, true, opts); err != nil {
		t.Fatalf("amend approve: %v", err)
	}
	events := readJournal(t, root, c.ID)
	last := events[len(events)-1]
	if last.Kind != "approve" || last.Data["amend"] != true {
		t.Fatalf("amend not journaled: %+v", last)
	}
}

func TestVerifyDetectsSpecDrift(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)

	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	// Maker weakens the approved living spec after approval.
	writeFile(t, root, ".blueprint/specs/calc/spec.md", "REQ-calc-001: anything goes now\n")

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Tamper || v.Pass {
		t.Fatalf("want tamper verdict, got pass=%v tamper=%v", v.Pass, v.Tamper)
	}
	detail := findCheck(t, v, "tamper").Detail
	if !strings.Contains(detail, "modified: .blueprint/specs/calc/spec.md") {
		t.Fatalf("tamper diff should name the drifted spec, got:\n%s", detail)
	}
	if !strings.Contains(detail, "--amend") {
		t.Fatalf("tamper detail should teach the remediation path, got:\n%s", detail)
	}
	// Journal recorded the tamper event.
	events := readJournal(t, root, c.ID)
	kinds := map[string]bool{}
	for _, e := range events {
		kinds[e.Kind] = true
	}
	if !kinds["tamper"] {
		t.Fatalf("tamper event missing from journal: %+v", events)
	}
}

func TestVerifyDetectsTestCountDrop(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)

	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	// Delete one trace annotation: count drops 2 -> 1.
	fixture := readFixture(t, "calc_test.go.fixture")
	writeFile(t, root, "calc/calc_test.go", strings.Replace(fixture, "// verifies: REQ-calc-002\n", "", 1))

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Tamper {
		t.Fatal("annotation removal must trip TAMPER")
	}
	if d := findCheck(t, v, "tamper").Detail; !strings.Contains(d, "test count dropped: approved 2 -> current 1") {
		t.Fatalf("detail should state the count floor, got:\n%s", d)
	}
}

func TestVerifyDetectsSkipMarkers(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)

	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	// Skip a test without touching annotations or count... still caught,
	// because the file hash drifts AND the marker scan names the edit.
	fixture := readFixture(t, "calc_test.go.fixture")
	writeFile(t, root, "calc/calc_test.go", strings.Replace(fixture,
		"func TestSubBorrows(t *testing.T) {\n", "func TestSubBorrows(t *testing.T) {\n\tt.Skip(\"flaky\")\n", 1))

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Tamper {
		t.Fatal("skip marker must trip TAMPER")
	}
	if d := findCheck(t, v, "tamper").Detail; !strings.Contains(d, "skip/only marker in approved test file calc/calc_test.go") {
		t.Fatalf("detail should name the skip marker, got:\n%s", d)
	}
}

func TestVerifyWithoutApprovalFailsTamper(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Tamper {
		t.Fatal("verify before approve must fail the tamper stage")
	}
	if d := findCheck(t, v, "tamper").Detail; !strings.Contains(d, "blueprint approve") {
		t.Fatalf("detail should point at the approve command, got:\n%s", d)
	}
}

func TestDeclaredChecksAndScopedVerifiers(t *testing.T) {
	c := testChange(core.TierLight)
	// REQ-calc-001 declares a check verifier by name.
	c.Delta[0].Requirement.Verify = append(c.Delta[0].Requirement.Verify, core.VerifyMethod{Kind: "check", Ref: "toolchain"})
	root := scaffoldRepo(t)
	writeFile(t, root, ".blueprint/verifiers.toml", `
[[verifier]]
name = "toolchain"
command = "go version"
config_path = ""
report_format = "text"
applies_to = ["infra"]

[[verifier]]
name = "scoped-fail"
command = "go definitely-not-a-subcommand"
config_path = ""
report_format = "text"
applies_to = ["app-code"]

[[verifier]]
name = "out-of-scope"
command = "go version"
config_path = ""
report_format = "text"
applies_to = ["infra"]
`)
	opts := stubOpts(c)
	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if v.Pass {
		t.Fatal("scoped failing verifier must fail the verdict")
	}

	tc := findCheck(t, v, "toolchain")
	if !tc.Pass || tc.ReqID != "REQ-calc-001" {
		t.Fatalf("declared check: %+v", tc)
	}
	if tc.ReportPath == "" {
		t.Fatal("declared check should write a report")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(tc.ReportPath))); err != nil {
		t.Fatalf("report file: %v", err)
	}

	sf := findCheck(t, v, "scoped-fail")
	if sf.Pass || sf.ExitCode == 0 {
		t.Fatalf("scope-matched verifier should run and fail: %+v", sf)
	}
	for _, ck := range v.Checks {
		if ck.Name == "out-of-scope" {
			t.Fatal("verifier scoped to another scenario must not run")
		}
	}

	// Failure-set fingerprint is deterministic across identical reruns.
	v2, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if v.Fingerprint != v2.Fingerprint || v.Fingerprint == "" {
		t.Fatalf("fingerprints differ: %s vs %s", v.Fingerprint, v2.Fingerprint)
	}
}

func TestDeclaredCheckMissingVerifierFails(t *testing.T) {
	c := testChange(core.TierLight)
	c.Delta[0].Requirement.Verify = []core.VerifyMethod{{Kind: "check", Ref: "ghost"}}
	root := scaffoldRepo(t)
	opts := stubOpts(c)
	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if v.Pass {
		t.Fatal("undeclared verifier reference must fail")
	}
	g := findCheck(t, v, "ghost")
	if g.Pass || !strings.Contains(g.Detail, "verifiers.toml") {
		t.Fatalf("missing verifier should teach the fix: %+v", g)
	}
}

func TestModelCheckerSkippedWhenUnconfigured(t *testing.T) {
	c := testChange(core.TierFull)
	root := scaffoldRepo(t)
	opts := stubOpts(c)
	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	mc := findCheck(t, v, "model-checker")
	if !mc.Pass || !strings.Contains(mc.Detail, "skipped: no [checker] command") {
		t.Fatalf("unconfigured checker should skip with reason: %+v", mc)
	}
}

func TestModelCheckerSkippedOnNonFullTier(t *testing.T) {
	c := testChange(core.TierLight)
	root := scaffoldRepo(t)
	opts := stubOpts(c)
	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	mc := findCheck(t, v, "model-checker")
	if !mc.Pass || !strings.Contains(mc.Detail, "full-tier changes only") {
		t.Fatalf("light tier should skip the checker: %+v", mc)
	}
}

func TestModelCheckerReceivesFullSpecPacket(t *testing.T) {
	c := testChange(core.TierFull)
	root := scaffoldRepo(t)
	opts := stubOpts(c)
	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	packetOut := filepath.Join(t.TempDir(), "packet.json")
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("HELPER_MODE", "dump-stdin")
	t.Setenv("HELPER_OUT", packetOut)
	writeFile(t, root, ".blueprint/config.toml", fmt.Sprintf(
		"[checker]\ncommand = \"'%s' -test.run=TestHelperProcess\"\nmax_usd = 3.5\n", exe))

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	mc := findCheck(t, v, "model-checker")
	if !mc.Pass {
		t.Fatalf("checker helper should pass: %+v", mc)
	}

	b, err := os.ReadFile(packetOut)
	if err != nil {
		t.Fatalf("checker never received the stdin packet: %v", err)
	}
	var packet struct {
		ChangeID string            `json:"change_id"`
		ChangeMD string            `json:"change_md"`
		Specs    map[string]string `json:"specs"`
		Rubric   string            `json:"rubric"`
		MaxUSD   float64           `json:"max_usd"`
	}
	if err := json.Unmarshal(b, &packet); err != nil {
		t.Fatal(err)
	}
	// Checker-context rule: FULL spec text, never diff-only.
	if !strings.Contains(packet.Specs["calc"], "REQ-calc-001") {
		t.Fatalf("packet must carry the full living-spec text, got: %+v", packet.Specs)
	}
	if !strings.Contains(packet.ChangeMD, "add-subtraction") {
		t.Fatal("packet must carry the full change.md text")
	}
	if !strings.Contains(packet.Rubric, "verifier-weakening") {
		t.Fatal("rubric must hunt verifier-weakening edits")
	}
	if packet.MaxUSD != 3.5 {
		t.Fatalf("max_usd override not passed: %v", packet.MaxUSD)
	}
	if packet.ChangeID != c.ID {
		t.Fatalf("change id = %q", packet.ChangeID)
	}
}

func TestModelCheckerFailureFailsVerdict(t *testing.T) {
	c := testChange(core.TierFull)
	root := scaffoldRepo(t)
	opts := stubOpts(c)
	if _, err := verify.Approve(root, c.ID, false, opts); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("HELPER_MODE", "fail")
	writeFile(t, root, ".blueprint/config.toml", fmt.Sprintf(
		"[checker]\ncommand = \"'%s' -test.run=TestHelperProcess\"\n", exe))

	v, err := verify.Run(root, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if v.Pass {
		t.Fatal("failing checker must fail the verdict")
	}
	mc := findCheck(t, v, "model-checker")
	if mc.Pass || mc.ExitCode != 3 {
		t.Fatalf("checker result: %+v", mc)
	}
}

func TestUnwiredSpecHookErrorsWithRemediation(t *testing.T) {
	_, err := verify.Run(t.TempDir(), "x", verify.Options{Hooks: &verify.Hooks{}})
	if err == nil || !strings.Contains(err.Error(), "internal/spec is not wired") {
		t.Fatalf("want wiring remediation error, got %v", err)
	}
}

// TestHelperProcess is not a real test: it is the subprocess body for checker
// hook tests (classic os/exec helper pattern).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "dump-stdin":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(os.Getenv("HELPER_OUT"), b, 0o644); err != nil {
			os.Exit(1)
		}
	case "fail":
		os.Exit(3)
	}
	os.Exit(0)
}

func findCheck(t *testing.T, v *core.Verdict, name string) core.CheckResult {
	t.Helper()
	for _, c := range v.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q missing from verdict: %+v", name, v.Checks)
	return core.CheckResult{}
}

func readJournal(t *testing.T, root, id string) []core.JournalEvent {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".blueprint", "changes", id, "journal.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var out []core.JournalEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var ev core.JournalEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("journal line %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

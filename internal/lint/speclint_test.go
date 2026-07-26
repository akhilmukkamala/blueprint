package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/core"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const cleanSpec = `---
id: auth
status: approved
owner: alice
reviewed: 2026-07-21
---

# auth

### REQ-auth-001 (event-driven)

When a login request carries invalid credentials, the system shall respond 401.

verify:
- test: TestLoginRejectsInvalid
`

func rulesByFile(findings []core.LintFinding) map[string][]string {
	out := map[string][]string{}
	for _, f := range findings {
		out[f.Rule] = append(out[f.Rule], f.File)
	}
	return out
}

func TestSpecCleanRepo(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", cleanSpec)
	findings, err := Spec(repo, Config{})
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("clean repo produced findings: %+v", findings)
	}
}

func TestSpecEmptyRepo(t *testing.T) {
	findings, err := Spec(t.TempDir(), Config{})
	if err != nil || len(findings) != 0 {
		t.Errorf("empty repo = (%v, %v), want no findings", findings, err)
	}
}

func TestSpecLintViolations(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/bad/spec.md", `---
id: bad
status: wip
owner: bob
reviewed: 2026-07-21
---

# bad

### REQ-bad-001 (event-driven)

The system shall be robust and user-friendly.

### REQ-bad-002 (ubiquitous)

The system shall respond quickly. It logs. It retries. It reports.

verify:
- test: TestX

### REQ-bad-002 (ubiquitous)

The system shall log every request.

verify:
- fuzz: whatever

### REQ-other-004 (ubiquitous)

The system shall exist as of today.

verify:
- human: does this look right?
`)

	findings, err := Spec(repo, Config{})
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	rules := rulesByFile(findings)
	for _, want := range []string{
		"spec-status",      // status: wip
		"ears-conformance", // REQ-bad-001 declared event-driven, text is ubiquitous-shaped
		"vague-word",       // robust, user-friendly, quickly
		"verify-missing",   // REQ-bad-001 has no verify block
		"req-id-duplicate", // REQ-bad-002 twice
		"verify-kind",      // fuzz:
		"req-id-area",      // REQ-other-004 in specs/bad/
		"relative-date",    // "today"
		"sentence-budget",  // 4 sentences
		"verify-human",     // human: escape hatch warning
	} {
		if len(rules[want]) == 0 {
			t.Errorf("missing expected rule %q; got %v", want, rules)
		}
	}
	if got := len(rules["vague-word"]); got != 3 {
		t.Errorf("vague-word findings = %d, want 3", got)
	}
	for _, f := range findings {
		if f.Remediation == "" {
			t.Errorf("finding %q has no remediation", f.Rule)
		}
		if f.File != ".blueprint/specs/bad/spec.md" {
			t.Errorf("finding file = %q, want repo-relative forward-slash path", f.File)
		}
	}
}

func TestSpecLintSeverities(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", cleanSpec)
	f, err := Spec(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if HasErrors(f) {
		t.Errorf("clean repo reports errors")
	}
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", strings.Replace(cleanSpec, "respond 401", "respond robust", 1))
	f, err = Spec(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !HasErrors(f) {
		t.Errorf("vague word did not produce an error-severity finding")
	}
}

func TestSpecLintParseErrorBecomesFinding(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/broken/spec.md", "no frontmatter here\n")
	findings, err := Spec(repo, Config{})
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if len(findings) != 1 || findings[0].Rule != "spec-parse" || findings[0].Severity != SevError {
		t.Errorf("findings = %+v, want one spec-parse error", findings)
	}
}

func TestChangeLintDeltaAndTasks(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", cleanSpec)
	writeFile(t, repo, ".blueprint/changes/c1/change.md", `+++
id = "c1"
title = "Bad change"
type = "feat"
tier = "full"
status = "draft"
scenario = "greenfield"
+++

# Bad change

## Delta

### ADDED REQ-auth-001 (event-driven)

When a duplicate is added, the system shall complain.

verify:
- test: TestDup

### MODIFIED REQ-ghost-009 (ubiquitous)

The system shall haunt.

verify:
- test: TestGhost

### REMOVED REQ-ghost-010

## Tasks

- [ ] T1: TBD
- [ ] T2: similar to task 1
- [ ] T3: Implement the lockout counter in src/auth/lockout.go
  - Consumes: .blueprint/specs/auth/spec.md
  - Produces: src/auth/lockout.go
`)

	findings, err := Spec(repo, Config{})
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	rules := rulesByFile(findings)
	if len(rules["req-id-reused"]) != 1 {
		t.Errorf("req-id-reused = %v", rules["req-id-reused"])
	}
	if len(rules["req-id-dangling"]) != 2 {
		t.Errorf("req-id-dangling = %v", rules["req-id-dangling"])
	}
	if len(rules["task-placeholder"]) != 2 {
		t.Errorf("task-placeholder = %v", rules["task-placeholder"])
	}
	// T1 and T2 lack Consumes/Produces on a full-tier change; T3 has both.
	if len(rules["task-handoff"]) != 2 {
		t.Errorf("task-handoff = %v", rules["task-handoff"])
	}
	for _, f := range findings {
		if f.File != ".blueprint/changes/c1/change.md" {
			t.Errorf("finding file = %q", f.File)
		}
	}
}

func TestLightTierTasksNeedNoHandoff(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/changes/c2/change.md", `+++
id = "c2"
title = "Light fix"
type = "fix"
tier = "light"
status = "draft"
scenario = "bug-fix"
+++

## Tasks

- [ ] T1: Write the failing regression test in tests/y_test.go
`)
	findings, err := Spec(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("light-tier task without handoff flagged: %+v", findings)
	}
}

func TestConfigurableBannedWords(t *testing.T) {
	repo := t.TempDir()
	spec := strings.Replace(cleanSpec, "respond 401", "respond 401 frobnicate", 1)
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", spec)

	f, err := Spec(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 0 {
		t.Errorf("default config flagged frobnicate: %+v", f)
	}
	f, err = Spec(repo, Config{ExtraBannedWords: []string{"frobnicate"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 || f[0].Rule != "vague-word" {
		t.Errorf("extra banned word not applied: %+v", f)
	}
	// Replacing the list entirely drops the defaults.
	spec = strings.Replace(cleanSpec, "respond 401", "respond robust", 1)
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", spec)
	f, err = Spec(repo, Config{BannedWords: []string{"frobnicate"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 0 {
		t.Errorf("replaced list still flags defaults: %+v", f)
	}
}

func TestEARSConformance(t *testing.T) {
	cases := []struct {
		pattern core.EARSPattern
		text    string
		ok      bool
	}{
		{core.PatternUbiquitous, "The system shall log every request.", true},
		{core.PatternUbiquitous, "Log every request.", false},
		{core.PatternEventDriven, "When a request arrives, the system shall log it.", true},
		{core.PatternEventDriven, "The system shall log requests.", false},
		{core.PatternStateDriven, "While maintenance mode is active, the system shall reject writes.", true},
		{core.PatternStateDriven, "When maintenance mode starts, the system shall reject writes.", false},
		{core.PatternOptional, "Where the audit feature is enabled, the system shall record actor IDs.", true},
		{core.PatternUnwanted, "If the disk is full, then the system shall stop accepting uploads.", true},
		{core.PatternUnwanted, "If the disk is full the system shall stop.", false},
		{core.PatternComplex, "While a deploy is running, if a health check fails, then the system shall roll back.", true},
		{core.PatternComplex, "The system shall roll back.", false},
		{core.EARSPattern("bogus"), "The system shall parse.", false},
	}
	for _, tc := range cases {
		ok, _ := earsConforms(tc.pattern, tc.text)
		if ok != tc.ok {
			t.Errorf("earsConforms(%s, %q) = %v, want %v", tc.pattern, tc.text, ok, tc.ok)
		}
	}
}

func TestSentenceCount(t *testing.T) {
	if n := sentenceCount("One. Two! Three? Four."); n != 4 {
		t.Errorf("sentenceCount = %d, want 4", n)
	}
	if n := sentenceCount("The system shall respond 401."); n != 1 {
		t.Errorf("sentenceCount = %d, want 1", n)
	}
}

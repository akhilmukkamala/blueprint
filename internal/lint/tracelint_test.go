package lint

import (
	"testing"

	"blueprint/internal/core"
)

const tracedSpec = `---
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

### REQ-auth-002 (ubiquitous)

The system shall store passwords only as argon2id hashes.

verify:
- test: TestPasswordHashing
`

func TestTraceCoverageAndDangling(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", tracedSpec)
	// Draft specs are not yet held to the coverage bar.
	writeFile(t, repo, ".blueprint/specs/draftarea/spec.md", `---
id: draftarea
status: draft
---

# draftarea

### REQ-draftarea-001 (ubiquitous)

The system shall exist.

verify:
- test: TestExists
`)
	// Any comment syntax counts; one covered REQ, one dangling annotation.
	writeFile(t, repo, "tests/auth_test.go", "package auth\n\n// verifies: REQ-auth-001\nfunc TestLoginRejectsInvalid(t *T) {}\n")
	writeFile(t, repo, "scripts/check.py", "# verifies: REQ-ghost-001\n")
	// Annotations under skipped dirs are invisible.
	writeFile(t, repo, "node_modules/pkg/index.js", "// verifies: REQ-auth-002\n")
	// Non-source extensions are not scanned.
	writeFile(t, repo, "notes.md", "verifies: REQ-auth-002\n")

	findings, err := Trace(repo, Config{})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	rules := rulesByFile(findings)
	if len(rules["trace-dangling"]) != 1 || rules["trace-dangling"][0] != "scripts/check.py" {
		t.Errorf("trace-dangling = %v", rules["trace-dangling"])
	}
	if len(rules["trace-uncovered"]) != 1 || rules["trace-uncovered"][0] != ".blueprint/specs/auth/spec.md" {
		t.Errorf("trace-uncovered = %v", rules["trace-uncovered"])
	}
	for _, f := range findings {
		if f.Severity != SevError || f.Remediation == "" {
			t.Errorf("finding %+v: trace findings are errors with remediation", f)
		}
	}
	if !HasErrors(findings) {
		t.Errorf("HasErrors = false")
	}
}

func TestTraceCleanWhenCovered(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".blueprint/specs/auth/spec.md", tracedSpec)
	writeFile(t, repo, "tests/auth_test.go",
		"package auth\n// verifies: REQ-auth-001\n// verifies: REQ-auth-002\n")
	findings, err := Trace(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("covered repo produced findings: %+v", findings)
	}
}

func TestTraceKnowsOpenChangeDeltas(t *testing.T) {
	repo := t.TempDir()
	// A REQ that exists only as an ADDED delta in an open change is a valid
	// annotation target while the change is in flight.
	writeFile(t, repo, ".blueprint/changes/c1/change.md", `+++
id = "c1"
title = "New feature"
type = "feat"
tier = "light"
status = "draft"
scenario = "greenfield"
+++

## Delta

### ADDED REQ-new-001 (ubiquitous)

The system shall parse the new format.

verify:
- test: TestNewFormat
`)
	writeFile(t, repo, "tests/new_test.go", "package n\n// verifies: REQ-new-001\n")
	findings, err := Trace(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("open-change delta annotation flagged: %+v", findings)
	}
}

func TestTraceFindingShape(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "src/a.go", "package a\n\n// verifies: REQ-nope-001\n")
	findings, err := Trace(repo, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	f := findings[0]
	if f.File != "src/a.go" || f.Line != 3 || f.Rule != "trace-dangling" {
		t.Errorf("finding = %+v, want src/a.go:3 trace-dangling", f)
	}
	var _ core.LintFinding = f
}

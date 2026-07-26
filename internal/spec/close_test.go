package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/core"
)

func TestClose(t *testing.T) {
	repo := tempRepo(t)
	if err := Close(repo, changeID); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err := LoadSpec(repo, "auth")
	if err != nil {
		t.Fatalf("LoadSpec after close: %v", err)
	}
	byID := map[string]core.Requirement{}
	for _, r := range s.Requirements {
		byID[r.ID] = r
	}
	if _, ok := byID["REQ-auth-002"]; ok {
		t.Errorf("REMOVED REQ-auth-002 still present after close")
	}
	if r, ok := byID["REQ-auth-003"]; !ok {
		t.Errorf("ADDED REQ-auth-003 missing after close")
	} else if r.Pattern != core.PatternUnwanted {
		t.Errorf("REQ-auth-003 pattern = %q", r.Pattern)
	}
	if r, ok := byID["REQ-auth-001"]; !ok {
		t.Errorf("REQ-auth-001 missing after close")
	} else if !strings.Contains(r.Text, "within 200 ms") {
		t.Errorf("MODIFIED text not merged: %q", r.Text)
	}

	// Frontmatter metadata survives the rewrite.
	full, err := loadFullSpec(repo, "auth")
	if err != nil {
		t.Fatal(err)
	}
	if full.Owner != "alice" || full.Reviewed != "2026-07-21" {
		t.Errorf("owner/reviewed dropped by close: %+v", full)
	}

	// Design harvested into the touched area's design.md.
	design, err := os.ReadFile(filepath.Join(repo, ".blueprint", "specs", "auth", "design.md"))
	if err != nil {
		t.Fatalf("design.md not harvested: %v", err)
	}
	if !strings.Contains(string(design), "Lockout state lives") || !strings.Contains(string(design), changeID) {
		t.Errorf("design.md content: %s", design)
	}

	// Folder archived verbatim, stamped closed; changes/ entry gone.
	if _, err := os.Stat(ChangePath(repo, changeID)); !os.IsNotExist(err) {
		t.Errorf("change folder still under changes/: %v", err)
	}
	archived := filepath.Join(repo, ".blueprint", "archive", changeID, "change.md")
	b, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("archived change.md: %v", err)
	}
	if !strings.Contains(string(b), `status = "closed"`) {
		t.Errorf("archived change not stamped closed")
	}

	// One close event appended to the worklog.
	log, err := os.ReadFile(filepath.Join(repo, ".blueprint", "log", "worklog.ndjson"))
	if err != nil {
		t.Fatalf("worklog: %v", err)
	}
	if !strings.Contains(string(log), `"kind":"close"`) || !strings.Contains(string(log), changeID) {
		t.Errorf("worklog line: %s", log)
	}
}

func TestCloseRefusesUnverified(t *testing.T) {
	repo := tempRepo(t)
	c, err := LoadChange(repo, changeID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = core.StatusDraft
	if err := SaveChange(repo, c); err != nil {
		t.Fatal(err)
	}
	err = Close(repo, changeID)
	if err == nil || !strings.Contains(err.Error(), "only verified changes close") {
		t.Errorf("err = %v, want verified-only refusal", err)
	}
}

func TestCloseRefusesReusedID(t *testing.T) {
	repo := tempRepo(t)
	c, err := LoadChange(repo, changeID)
	if err != nil {
		t.Fatal(err)
	}
	// Turn the ADDED entry into a collision with an existing living REQ.
	c.Delta[0].Requirement.ID = "REQ-auth-001"
	c.Delta[0].Area = "auth"
	if err := SaveChange(repo, c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(repo, ".blueprint", "specs", "auth", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	err = Close(repo, changeID)
	if err == nil || !strings.Contains(err.Error(), "never reused") {
		t.Fatalf("err = %v, want never-reused refusal", err)
	}
	// The failed close must leave the living spec untouched.
	after, err := os.ReadFile(filepath.Join(repo, ".blueprint", "specs", "auth", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("failed close mutated the living spec")
	}
	if _, err := os.Stat(ChangePath(repo, changeID)); err != nil {
		t.Errorf("failed close moved the change folder: %v", err)
	}
}

func TestCloseCreatesNewAreaSpec(t *testing.T) {
	repo := tempRepo(t)
	c, err := LoadChange(repo, changeID)
	if err != nil {
		t.Fatal(err)
	}
	c.Delta = append(c.Delta, core.DeltaEntry{
		Op:   core.DeltaAdded,
		Area: "billing",
		Requirement: core.Requirement{
			ID:      "REQ-billing-001",
			Pattern: core.PatternUbiquitous,
			Text:    "The system shall record every invoice mutation in the audit table.",
			Verify:  []core.VerifyMethod{{Kind: "test", Ref: "TestInvoiceAudit"}},
		},
	})
	if err := SaveChange(repo, c); err != nil {
		t.Fatal(err)
	}
	if err := Close(repo, changeID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s, err := LoadSpec(repo, "billing")
	if err != nil {
		t.Fatalf("billing spec not created: %v", err)
	}
	if len(s.Requirements) != 1 || s.Requirements[0].ID != "REQ-billing-001" {
		t.Errorf("billing requirements = %+v", s.Requirements)
	}
	// Design harvested into both touched areas.
	if _, err := os.Stat(filepath.Join(repo, ".blueprint", "specs", "billing", "design.md")); err != nil {
		t.Errorf("billing design.md missing: %v", err)
	}
}

func TestCloseMissingChange(t *testing.T) {
	err := Close(t.TempDir(), "nope")
	if err == nil || !strings.Contains(err.Error(), "no change") {
		t.Errorf("err = %v, want no-change remediation", err)
	}
}

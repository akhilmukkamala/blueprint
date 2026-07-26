package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
)

// specTestRoot mirrors cmd/blueprint's root: persistent --json + this
// feature's commands.
func specTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(newSpecLintCmd(), newSpecCloseCmd())
	return root
}

func specWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func specRun(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	root := specTestRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

const cliSpec = `---
id: auth
status: draft
owner: alice
reviewed: 2026-07-21
---

# auth

### REQ-auth-001 (event-driven)

When a login request carries invalid credentials, the system shall respond 401.

verify:
- test: TestLoginRejectsInvalid
`

func TestLintCmdCleanJSON(t *testing.T) {
	repo := t.TempDir()
	specWriteFile(t, repo, ".blueprint/specs/auth/spec.md", cliSpec)
	out, err := specRun(t, repo, "lint", "spec", "--json")
	if err != nil {
		t.Fatalf("lint spec --json: %v\n%s", err, out)
	}
	var parsed struct {
		Findings []core.LintFinding `json:"findings"`
		Errors   int                `json:"errors"`
		Warnings int                `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if parsed.Errors != 0 || len(parsed.Findings) != 0 {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestLintCmdErrorExit(t *testing.T) {
	repo := t.TempDir()
	specWriteFile(t, repo, ".blueprint/specs/auth/spec.md",
		strings.Replace(cliSpec, "respond 401", "be robust", 1))
	out, err := specRun(t, repo, "lint")
	if err == nil {
		t.Fatalf("lint on vague spec should exit non-zero\n%s", out)
	}
	if !strings.Contains(out, "vague-word") || !strings.Contains(out, "fix:") {
		t.Errorf("human output missing rule/remediation:\n%s", out)
	}
}

func TestLintCmdNoRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := specRun(t, dir, "lint")
	if err == nil || !strings.Contains(err.Error(), "blueprint init") {
		t.Errorf("err = %v, want init remediation", err)
	}
}

func TestLintCmdConfigTOML(t *testing.T) {
	repo := t.TempDir()
	specWriteFile(t, repo, ".blueprint/specs/auth/spec.md",
		strings.Replace(cliSpec, "respond 401", "frobnicate the request", 1))
	out, err := specRun(t, repo, "lint", "spec")
	if err != nil {
		t.Fatalf("frobnicate not banned by default: %v\n%s", err, out)
	}
	specWriteFile(t, repo, ".blueprint/config.toml", "[lint]\nextra_banned_words = [\"frobnicate\"]\n")
	out, err = specRun(t, repo, "lint", "spec")
	if err == nil {
		t.Fatalf("config.toml extra_banned_words ignored\n%s", out)
	}
	if !strings.Contains(out, "frobnicate") {
		t.Errorf("output:\n%s", out)
	}
}

const cliChange = `+++
id = "c1"
title = "Add lockout"
type = "feat"
tier = "light"
status = "verified"
scenario = "bug-fix"
+++

## Delta

### ADDED REQ-auth-002 (ubiquitous)

The system shall store passwords only as argon2id hashes.

verify:
- test: TestPasswordHashing
`

func TestCloseCmd(t *testing.T) {
	repo := t.TempDir()
	specWriteFile(t, repo, ".blueprint/specs/auth/spec.md", cliSpec)
	specWriteFile(t, repo, ".blueprint/changes/c1/change.md", cliChange)
	out, err := specRun(t, repo, "close", "c1", "--json")
	if err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if parsed["closed"] != "c1" {
		t.Errorf("parsed = %v", parsed)
	}
	if _, err := os.Stat(filepath.Join(repo, ".blueprint", "archive", "c1", "change.md")); err != nil {
		t.Errorf("change not archived: %v", err)
	}
	spec, err := os.ReadFile(filepath.Join(repo, ".blueprint", "specs", "auth", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spec), "REQ-auth-002") {
		t.Errorf("delta not merged:\n%s", spec)
	}
}

func TestCloseCmdRefusesDraft(t *testing.T) {
	repo := t.TempDir()
	specWriteFile(t, repo, ".blueprint/changes/c1/change.md",
		strings.Replace(cliChange, `status = "verified"`, `status = "draft"`, 1))
	_, err := specRun(t, repo, "close", "c1")
	if err == nil || !strings.Contains(err.Error(), "only verified changes close") {
		t.Errorf("err = %v", err)
	}
}

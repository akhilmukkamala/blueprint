package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// runNew executes `blueprint new` under a root that mimics cmd/blueprint
// (persistent --json), inside dir, and returns stdout.
func runNew(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(newCommand())
	root.AddCommand(registryCommand())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// tempRepo builds a .blueprint fixture with a registry + safety config.
func tempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ns := filepath.Join(dir, ".blueprint")
	if err := os.MkdirAll(ns, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ns, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("registry.toml", "[[class]]\nname = \"docs-only\"\ntype = \"docs\"\nglobs = [\"**/*.md\"]\nmax_loc = 200\nchecks = [\"markdown-lint\"]\n")
	write("safety.toml", "[safety]\nsensitive = [\"auth/**\"]\n")
	return dir
}

func fixedNow(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	prev := routeNow
	routeNow = func() time.Time { return now }
	t.Cleanup(func() { routeNow = prev })
	return now
}

func TestNewRoutesLightAndScaffolds(t *testing.T) {
	dir := tempRepo(t)
	fixedNow(t)
	out, err := runNew(t, dir, "new", "add profile page", "--type", "feat", "--paths", "src/profile.go", "--json")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var res newResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if res.Tier != core.TierLight || res.Status != core.StatusDraft {
		t.Errorf("tier=%s status=%s", res.Tier, res.Status)
	}
	if res.ChangeID != "2026-07-21-add-profile-page" {
		t.Errorf("change id = %q", res.ChangeID)
	}
	if _, err := os.Stat(res.ChangePath); err != nil {
		t.Errorf("change.md not written at %s: %v", res.ChangePath, err)
	}
	if len(res.Reasons) == 0 || res.Cost.Tokens == 0 {
		t.Errorf("reasons/cost missing: %+v", res)
	}
	evs, _, err := worklog.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "route" || evs[0].ChangeID != res.ChangeID {
		t.Errorf("expected one route event, got %+v", evs)
	}
}

func TestNewExemptWritesOnlyWorklogLine(t *testing.T) {
	dir := tempRepo(t)
	fixedNow(t)
	out, err := runNew(t, dir, "new", "fix typos", "--type", "docs", "--paths", "README.md")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !strings.Contains(out, "tier: exempt") || !strings.Contains(out, "no change folder") {
		t.Errorf("human output wrong:\n%s", out)
	}
	if !strings.Contains(out, "projected ceremony (exempt)") {
		t.Errorf("cost line missing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "changes")); !os.IsNotExist(err) {
		t.Error("exempt change must not scaffold a change folder")
	}
	evs, _, _ := worklog.Read(dir)
	if len(evs) != 1 || evs[0].Kind != "route" || evs[0].Data["tier"] != "exempt" {
		t.Errorf("worklog = %+v", evs)
	}
}

func TestNewSensitivePathFloorsFull(t *testing.T) {
	dir := tempRepo(t)
	fixedNow(t)
	out, err := runNew(t, dir, "new", "tweak auth docs", "--type", "docs", "--paths", "auth/README.md", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res newResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Tier != core.TierFull {
		t.Errorf("sensitive touch must floor full, got %s", res.Tier)
	}
}

func TestNewOverrideRequiresReason(t *testing.T) {
	dir := tempRepo(t)
	_, err := runNew(t, dir, "new", "risky thing", "--tier", "full")
	if err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("override without reason must fail with remediation, got %v", err)
	}
}

func TestNewOverrideIsLogged(t *testing.T) {
	dir := tempRepo(t)
	fixedNow(t)
	out, err := runNew(t, dir, "new", "small but scary", "--paths", "src/a.go",
		"--tier", "full", "--reason", "touches undocumented invariant", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res newResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Tier != core.TierFull {
		t.Errorf("override not applied: %s", res.Tier)
	}
	last := res.Reasons[len(res.Reasons)-1]
	if last.Axis != "override" || !strings.Contains(last.Detail, "undocumented invariant") {
		t.Errorf("override reason missing: %+v", res.Reasons)
	}
	evs, _, _ := worklog.Read(dir)
	if len(evs) != 2 || evs[1].Kind != "override" || evs[1].Data["reason"] != "touches undocumented invariant" {
		t.Errorf("override must append its own worklog event, got %+v", evs)
	}
}

func TestNewSev1OpensBackfillRecord(t *testing.T) {
	dir := tempRepo(t)
	now := fixedNow(t)
	out, err := runNew(t, dir, "new", "prod outage hotfix", "--type", "fix", "--paths", "src/hot.go", "--sev1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res newResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != core.StatusBackfill {
		t.Errorf("status = %s, want backfill-due", res.Status)
	}
	wantSLA := now.Add(72 * time.Hour).Format(time.RFC3339)
	if res.SLA != wantSLA {
		t.Errorf("sla = %q, want %q", res.SLA, wantSLA)
	}
	raw, err := os.ReadFile(res.ChangePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), wantSLA) {
		t.Error("SLA not written into change.md frontmatter")
	}
}

func TestNewWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	fixedNow(t)
	out, err := runNew(t, dir, "new", "parallel change", "--paths", "src/p.go", "--worktree", "--json")
	if err != nil {
		t.Fatalf("new --worktree: %v\n%s", err, out)
	}
	var res newResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Worktree == "" {
		t.Fatal("worktree path missing from result")
	}
	if _, err := os.Stat(res.Worktree); err != nil {
		t.Errorf("worktree not created at %s: %v", res.Worktree, err)
	}
	branches, _ := exec.Command("git", "-C", dir, "branch", "--list", "agent/"+res.ChangeID).Output()
	if !strings.Contains(string(branches), "agent/"+res.ChangeID) {
		t.Errorf("branch agent/%s not created: %q", res.ChangeID, branches)
	}
}

func TestNewRejectsUnknownTypeAndTier(t *testing.T) {
	dir := tempRepo(t)
	if _, err := runNew(t, dir, "new", "x", "--type", "banana"); err == nil {
		t.Error("unknown --type must fail")
	}
	if _, err := runNew(t, dir, "new", "x", "--tier", "medium", "--reason", "r"); err == nil {
		t.Error("unknown --tier must fail")
	}
}

func TestRegistryList(t *testing.T) {
	dir := tempRepo(t)
	out, err := runNew(t, dir, "registry", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "docs-only") || !strings.Contains(out, "markdown-lint") {
		t.Errorf("registry list output:\n%s", out)
	}
	out, err = runNew(t, dir, "registry", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Classes []struct {
			Name string `json:"name"`
		} `json:"classes"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("registry list --json not JSON: %v\n%s", err, out)
	}
	if len(parsed.Classes) != 1 || parsed.Classes[0].Name != "docs-only" {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestRegistryListEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runNew(t, dir, "registry", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "registry is empty") {
		t.Errorf("empty registry must teach how to fill it:\n%s", out)
	}
}

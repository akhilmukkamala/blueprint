package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runGardenCLI executes a gardenhooks-feature command under a root mimicking
// cmd/blueprint (persistent --json), inside dir.
func runGardenCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(gardenCommand())
	root.AddCommand(mapCommand())
	root.AddCommand(newDoctorCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func writeGardenFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gardenFixture: an orphaned knowledge file and no map cache — enough to make
// the report non-empty without git history.
func gardenFixture(t *testing.T) string {
	dir := t.TempDir()
	writeGardenFile(t, filepath.Join(dir, ".blueprint", "knowledge", "orphan.md"),
		"---\nreviewed: 2026-07-20\n---\n# Orphan\nnothing links here\n")
	writeGardenFile(t, filepath.Join(dir, "AGENTS.md"), "# Index\n(no knowledge pointers yet)\n")
	return dir
}

func TestGardenCmdJSON(t *testing.T) {
	dir := gardenFixture(t)
	out, err := runGardenCLI(t, dir, "garden", "--json")
	if err != nil {
		t.Fatalf("garden must not fail on findings (it is a report, not a gate): %v\n%s", err, out)
	}
	var rep struct {
		Items []struct {
			Priority int    `json:"priority"`
			Category string `json:"category"`
			Fix      string `json:"fix"`
		} `json:"items"`
		SuggestedDeletions int    `json:"suggested_deletions_lines"`
		Summary            string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if len(rep.Items) == 0 || rep.Summary == "" {
		t.Fatalf("fixture must yield items and a summary: %s", out)
	}
	for _, it := range rep.Items {
		if it.Fix == "" {
			t.Errorf("item without fix in %s", out)
		}
	}
}

func TestGardenCmdHuman(t *testing.T) {
	dir := gardenFixture(t)
	out, err := runGardenCLI(t, dir, "garden")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fix:") || !strings.Contains(out, "knowledge-lint") {
		t.Fatalf("human output must list prioritized items with fixes:\n%s", out)
	}
	if !strings.Contains(out, "net-lines") {
		t.Fatalf("report must end with the net-lines framing:\n%s", out)
	}
}

func TestMapQuietPrintsNothing(t *testing.T) {
	dir := t.TempDir()
	writeGardenFile(t, filepath.Join(dir, ".blueprint", "config.toml"), "")
	writeGardenFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	out, err := runGardenCLI(t, dir, "map", "--refresh", "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("map --quiet must print nothing, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "map.json")); err != nil {
		t.Fatalf("map --quiet must still refresh the cache: %v", err)
	}
}

func TestDoctorInstallHooksCmd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runGardenCLI(t, dir, "doctor", "--install-hooks")
	if err != nil {
		t.Fatalf("install-hooks in a git repo must succeed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "post-commit") {
		t.Fatalf("output should name the written hooks:\n%s", out)
	}
	for _, hook := range []string{"post-commit", "post-merge"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", hook)); err != nil {
			t.Errorf("%s not written: %v", hook, err)
		}
	}

	// JSON mode reports the written paths.
	out, err = runGardenCLI(t, dir, "doctor", "--install-hooks", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		HooksDir string   `json:"hooks_dir"`
		Written  []string `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if len(res.Written) != 2 || res.HooksDir == "" {
		t.Fatalf("unexpected install result: %s", out)
	}

	// Foreign hook + no --force refuses via a remediation error.
	writeGardenFile(t, filepath.Join(dir, ".git", "hooks", "post-commit"), "#!/bin/sh\necho mine\n")
	_, err = runGardenCLI(t, dir, "doctor", "--install-hooks")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("foreign hook must refuse and point at --force, got %v", err)
	}
	if _, err := runGardenCLI(t, dir, "doctor", "--install-hooks", "--force"); err != nil {
		t.Fatalf("--force must replace the foreign hook: %v", err)
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"blueprint/internal/autonomy"
	"blueprint/internal/core"
)

// runDoctorCLI executes a doctor-feature command under a root that mimics
// cmd/blueprint (persistent --json), inside dir, and returns stdout.
func runDoctorCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newAutonomyCmd())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestDoctorCmdJSON(t *testing.T) {
	dir := t.TempDir()
	// Real git/LookPath run here: the temp dir is not a repo and `blueprint`
	// is not on PATH, so doctor must exit non-zero with a remediation error
	// while still printing the structured table.
	out, err := runDoctorCLI(t, dir, "doctor", "--json")
	if err == nil {
		t.Fatal("doctor in a bare temp dir must fail (not a git repo)")
	}
	if !strings.Contains(err.Error(), "remediation") && !strings.Contains(err.Error(), "re-run") {
		t.Errorf("error should point at remediation, got %q", err.Error())
	}
	var rep struct {
		Checks []struct {
			Check string `json:"check"`
			Pass  bool   `json:"pass"`
		} `json:"checks"`
		Pass bool `json:"pass"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &rep); jsonErr != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", jsonErr, out)
	}
	if rep.Pass || len(rep.Checks) == 0 {
		t.Fatalf("expected failing checks in report: %s", out)
	}
	names := map[string]bool{}
	for _, c := range rep.Checks {
		names[c.Check] = true
	}
	for _, want := range []string{"git", "forge", "binary-on-path", "dev-env-runbook", "hooks-liveness"} {
		if !names[want] {
			t.Errorf("check %q missing from JSON report: %s", want, out)
		}
	}
}

func TestDoctorCmdHumanTable(t *testing.T) {
	dir := t.TempDir()
	out, _ := runDoctorCLI(t, dir, "doctor")
	if !strings.Contains(out, "check") || !strings.Contains(out, "remediation") {
		t.Fatalf("human output should be a check/pass/remediation table:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("bare temp dir should show failing rows:\n%s", out)
	}
}

func TestDoctorCmdRejectsBadStage(t *testing.T) {
	dir := t.TempDir()
	if _, err := runDoctorCLI(t, dir, "doctor", "--adopt-stage", "9"); err == nil {
		t.Fatal("stage 9 must be rejected")
	}
}

func TestAutonomyShowJSON(t *testing.T) {
	dir := t.TempDir()
	if err := autonomy.Save(dir, &autonomy.File{
		Profile: core.EnforcementProfile{Forge: "github", Enforced: true},
		Classes: map[string]autonomy.ClassState{"bugfix": {Level: core.L2Branch}},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := runDoctorCLI(t, dir, "autonomy", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var f autonomy.File
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("show --json not parseable: %v\n%s", err, out)
	}
	if f.Profile.Forge != "github" || f.Classes["bugfix"].Level != core.L2Branch {
		t.Fatalf("unexpected show output: %s", out)
	}
}

func TestAutonomyShowHuman(t *testing.T) {
	dir := t.TempDir()
	out, err := runDoctorCLI(t, dir, "autonomy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "forge: unknown") || !strings.Contains(out, "L1") {
		t.Fatalf("default show should explain the empty ladder:\n%s", out)
	}
}

func TestAutonomyRecomputeCmd(t *testing.T) {
	dir := t.TempDir()
	// Empty repo: recompute succeeds with nothing to evaluate.
	out, err := runDoctorCLI(t, dir, "autonomy", "recompute")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no runs recorded") {
		t.Fatalf("empty recompute should say so:\n%s", out)
	}
	// autonomy.json got materialized with refreshed (empty) state.
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "autonomy.json")); err != nil {
		t.Fatalf("recompute must persist autonomy.json: %v", err)
	}

	out, err = runDoctorCLI(t, dir, "autonomy", "recompute", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res autonomy.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("recompute --json not parseable: %v\n%s", err, out)
	}
}

func TestAutonomyUnknownSubcommand(t *testing.T) {
	dir := t.TempDir()
	if _, err := runDoctorCLI(t, dir, "autonomy", "explode"); err == nil ||
		!strings.Contains(err.Error(), "recompute") {
		t.Fatal("unknown subcommand must error naming the valid ones")
	}
}

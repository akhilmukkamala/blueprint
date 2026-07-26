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

// installTestRoot mirrors cmd/blueprint's root: persistent --json + this
// feature's commands.
func installTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(initCommand(), adoptCommand(), upgradeCommand(), uninstallCommand())
	return root
}

func installRun(t *testing.T, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	root := installTestRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func installExists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func TestInitCommandPlanThenWrite(t *testing.T) {
	dir := t.TempDir()
	out, err := installRun(t, dir, "", "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "plan:") || !strings.Contains(out, "AGENTS.md") {
		t.Errorf("plan not printed first:\n%s", out)
	}
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".blueprint/config.toml", ".blueprint/manifest.json"} {
		if !installExists(dir, rel) {
			t.Errorf("%s missing after init", rel)
		}
	}
	// Idempotent re-run reports skips, exit zero.
	out, err = installRun(t, dir, "", "init")
	if err != nil {
		t.Fatalf("re-init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skip-exists") {
		t.Errorf("re-init did not report skips:\n%s", out)
	}
}

func TestInitCommandJSON(t *testing.T) {
	dir := t.TempDir()
	out, err := installRun(t, dir, "", "init", "--offline", "--json")
	if err != nil {
		t.Fatalf("init --json: %v\n%s", err, out)
	}
	var res struct {
		Version string `json:"version"`
		Plan    []any  `json:"plan"`
		Written []any  `json:"written"`
		Offline bool   `json:"offline"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("init --json is not JSON: %v\n%s", err, out)
	}
	if res.Version == "" || len(res.Plan) == 0 || len(res.Written) == 0 || !res.Offline {
		t.Errorf("json shape wrong: %+v", res)
	}
}

func TestAdoptCommandJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("legacy notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := installRun(t, dir, "", "adopt", "--json")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	var res struct {
		Baselines struct {
			WindowDays int `json:"window_days"`
		} `json:"baselines"`
		Imported []string `json:"imported"`
		Shim     bool     `json:"claude_shim_written"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("adopt --json is not JSON: %v\n%s", err, out)
	}
	if res.Baselines.WindowDays != 90 || len(res.Imported) != 1 || !res.Shim {
		t.Errorf("adopt result wrong: %+v", res)
	}
	if !installExists(dir, ".blueprint/baselines.json") || !installExists(dir, ".blueprint/steering/imported-claude.md") {
		t.Error("adopt artifacts missing")
	}
}

func TestUpgradeCommandDryRunAndJSON(t *testing.T) {
	dir := t.TempDir()
	if out, err := installRun(t, dir, "", "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := installRun(t, dir, "", "upgrade", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("upgrade --dry-run: %v\n%s", err, out)
	}
	var res struct {
		DryRun bool  `json:"dry_run"`
		Files  []any `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("upgrade --json is not JSON: %v\n%s", err, out)
	}
	if !res.DryRun || len(res.Files) == 0 {
		t.Errorf("upgrade result wrong: %+v", res)
	}
	// No manifest anywhere: remediation error, non-zero.
	empty := t.TempDir()
	if _, err := installRun(t, empty, "", "upgrade"); err == nil {
		t.Error("upgrade without an install must fail")
	}
}

func TestUninstallCommandPurgePrompt(t *testing.T) {
	dir := t.TempDir()
	if out, err := installRun(t, dir, "", "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// Refusing the prompt aborts.
	if _, err := installRun(t, dir, "no\n", "uninstall", "--purge"); err == nil {
		t.Fatal("purge without a yes must abort")
	}
	if !installExists(dir, ".blueprint") {
		t.Fatal("aborted purge still deleted .blueprint")
	}
	// Answering yes purges.
	out, err := installRun(t, dir, "yes\n", "uninstall", "--purge")
	if err != nil {
		t.Fatalf("purge: %v\n%s", err, out)
	}
	if installExists(dir, ".blueprint") || installExists(dir, "CLAUDE.md") {
		t.Error("purge left tool files behind")
	}
}

func TestUninstallCommandJSONNonInteractive(t *testing.T) {
	dir := t.TempDir()
	if out, err := installRun(t, dir, "", "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// --json is the non-interactive path: no prompt, structured result.
	out, err := installRun(t, dir, "", "uninstall", "--purge", "--json")
	if err != nil {
		t.Fatalf("uninstall --json: %v\n%s", err, out)
	}
	var res struct {
		Purged  bool     `json:"purged"`
		Removed []string `json:"removed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("uninstall --json is not JSON: %v\n%s", err, out)
	}
	if !res.Purged || len(res.Removed) == 0 {
		t.Errorf("uninstall result wrong: %+v", res)
	}
}

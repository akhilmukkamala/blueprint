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

// runMapCLI executes commands from this feature's files under a root that
// mimics cmd/blueprint (persistent --json), inside dir.
func runMapCLI(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(dir)
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(mapCommand())
	root.AddCommand(mcpCommand())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

// mapRepo seeds a minimal repo: a .blueprint marker plus one Go file.
func mapRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":  "module example.com/clifix\n\ngo 1.22\n",
		"tool.go": "package tool\n\nfunc Widget() {}\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMapCommandHuman(t *testing.T) {
	dir := mapRepo(t)
	out, errb, err := runMapCLI(t, dir, "map")
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if !strings.Contains(out, "tool.go: Widget") {
		t.Errorf("human output missing ranked line, got:\n%s", out)
	}
	if !strings.Contains(errb, "files") {
		t.Errorf("stats line goes to stderr, got: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "map.json")); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func TestMapCommandJSON(t *testing.T) {
	dir := mapRepo(t)
	out, _, err := runMapCLI(t, dir, "map", "--json", "--budget", "700")
	if err != nil {
		t.Fatalf("map --json: %v", err)
	}
	var res mapResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if res.Budget != 700 || res.Files == 0 || !strings.Contains(res.Map, "tool.go") {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestMapCommandRejectsBadBudget(t *testing.T) {
	dir := mapRepo(t)
	_, _, err := runMapCLI(t, dir, "map", "--budget", "-5")
	if err == nil || !strings.Contains(err.Error(), "--budget") {
		t.Errorf("negative budget should fail with remediation, got %v", err)
	}
}

// TestMCPServeCommand smoke-tests the wiring: a handshake over the command's
// stdin/stdout (the deeper protocol tests live in internal/mcpserve).
func TestMCPServeCommand(t *testing.T) {
	dir := mapRepo(t)
	t.Chdir(dir)
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(mcpCommand())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"))
	root.SetArgs([]string{"mcp", "serve"})
	if err := root.Execute(); err != nil {
		t.Fatalf("mcp serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("initialize response is not JSON: %v\n%s", err, out.String())
	}
	if resp["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Errorf("bad initialize result: %v", resp)
	}
}

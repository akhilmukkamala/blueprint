package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/dream"
	"blueprint/internal/worklog"
)

// runDream executes `blueprint dream` under a root that mimics cmd/blueprint
// (persistent --json), inside dir, and returns stdout.
func runDream(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(newDreamCmd())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func dreamRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDreamNoSignalPrintsNoOpLine(t *testing.T) {
	dir := dreamRepo(t)
	out, err := runDream(t, dir, "dream")
	if err != nil {
		t.Fatalf("dream: %v", err)
	}
	if !strings.Contains(out, "no signal, nothing to dream about") {
		t.Fatalf("stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "dream")); !os.IsNotExist(err) {
		t.Fatal("no-op run must write nothing")
	}
}

func TestDreamNoSignalJSON(t *testing.T) {
	dir := dreamRepo(t)
	out, err := runDream(t, dir, "dream", "--json")
	if err != nil {
		t.Fatalf("dream --json: %v", err)
	}
	var res dream.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if !res.NoSignal {
		t.Fatalf("res = %+v", res)
	}
}

func TestDreamDryRunPrintsProposal(t *testing.T) {
	dir := dreamRepo(t)
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("c%d", i)
		at := time.Date(2026, 7, i, 9, 0, 0, 0, time.UTC)
		if err := worklog.Append(dir, core.JournalEvent{Time: at, Kind: "route", ChangeID: id,
			Data: map[string]any{"tier": "light", "type": "feat"}}); err != nil {
			t.Fatal(err)
		}
		if err := worklog.Append(dir, core.JournalEvent{Time: at.Add(time.Hour), Kind: "override", ChangeID: id,
			Data: map[string]any{"tier": "full", "reason": "auth"}}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runDream(t, dir, "dream", "--branch=false")
	if err != nil {
		t.Fatalf("dream --branch=false: %v", err)
	}
	if !strings.Contains(out, "# Blueprint dream proposal") || !strings.Contains(out, "dry run: nothing written") {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(out, "override-cluster") {
		t.Fatalf("proposal must list the extracted signal:\n%s", out)
	}
}

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// syncRoot builds a root command with the persistent --json flag so the sync
// subcommand sees the real flag wiring.
func syncRoot() *cobra.Command {
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(newSyncCmd())
	return root
}

// seedSyncRepo creates a minimal adapters-ready repo and chdirs into it.
func seedSyncRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("AGENTS.md", "# index\n")
	write(".blueprint/steering/style.md", "---\nid: style\ndescription: d\nglobs: [\"**/*.go\"]\nactivation: glob\n---\nrule body\n")
	write(".blueprint/config.toml", "[mcp.servers.blueprint]\ncommand = \"blueprint\"\nargs = [\"mcp\"]\n")
	t.Chdir(root)
	return root
}

func TestSyncCommandWritesAndIsIdempotent(t *testing.T) {
	root := seedSyncRepo(t)

	out, err := run(t, syncRoot(), "sync")
	if err != nil {
		t.Fatalf("sync failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "wrote CLAUDE.md") {
		t.Errorf("sync output missing CLAUDE.md write:\n%s", out)
	}
	for _, p := range []string{
		"CLAUDE.md",
		".claude/commands/blueprint-new.md",
		".cursor/rules/style.mdc",
		".codex/prompts/blueprint-status.md",
		".mcp.json",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected generated file %s: %v", p, err)
		}
	}

	// Second run reports zero writes (idempotence) in --json form.
	out, err = run(t, syncRoot(), "--json", "sync")
	if err != nil {
		t.Fatalf("second sync failed: %v\n%s", err, out)
	}
	var payload struct {
		Mode      string   `json:"mode"`
		Written   []string `json:"written"`
		Unchanged []string `json:"unchanged"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if payload.Mode != "sync" || len(payload.Written) != 0 || len(payload.Unchanged) == 0 {
		t.Errorf("second sync not idempotent: %+v", payload)
	}
}

func TestSyncCheckFailsOnDrift(t *testing.T) {
	root := seedSyncRepo(t)
	if out, err := run(t, syncRoot(), "sync"); err != nil {
		t.Fatalf("sync failed: %v\n%s", err, out)
	}
	if out, err := run(t, syncRoot(), "sync", "--check"); err != nil {
		t.Fatalf("check on clean tree failed: %v\n%s", err, out)
	}

	// Tamper with a generated file: check must exit non-zero and name it.
	p := filepath.Join(root, ".cursor", "rules", "style.mdc")
	if err := os.WriteFile(p, []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, syncRoot(), "sync", "--check")
	if err == nil {
		t.Fatalf("check on drifted tree succeeded:\n%s", out)
	}
	if !strings.Contains(out, ".cursor/rules/style.mdc") {
		t.Errorf("drift listing missing the drifted file:\n%s", out)
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	root := seedSyncRepo(t)
	out, err := run(t, syncRoot(), "sync", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("dry-run did not list pending writes:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("dry-run wrote CLAUDE.md")
	}
}

func TestSyncRevertRestores(t *testing.T) {
	root := seedSyncRepo(t)
	orig := "my own claude file\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, syncRoot(), "sync"); err != nil {
		t.Fatalf("sync failed: %v\n%s", err, out)
	}
	if out, err := run(t, syncRoot(), "sync", "--revert"); err != nil {
		t.Fatalf("revert failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil || string(got) != orig {
		t.Errorf("CLAUDE.md not restored from .bak: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".cursor", "rules", "style.mdc")); !os.IsNotExist(err) {
		t.Error("generated-only rule file survived revert")
	}
}

func TestSyncModeFlagsAreExclusive(t *testing.T) {
	seedSyncRepo(t)
	if _, err := run(t, syncRoot(), "sync", "--check", "--revert"); err == nil {
		t.Fatal("want error for --check with --revert")
	}
}

// adapterscmds.go registers `blueprint sync` (adapters feature, DESIGN §10).
// Owned by the adapters feature — other features register their commands in
// their own files.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"blueprint/internal/adapters"
)

func init() {
	Register(newSyncCmd())
}

// adaptersJSONMode reads the root --json persistent flag; false when the
// command runs detached from the root (tests).
func adaptersJSONMode(cmd *cobra.Command) bool {
	f := cmd.Root().PersistentFlags().Lookup("json")
	if f == nil {
		return false
	}
	v, _ := strconv.ParseBool(f.Value.String())
	return v
}

// adaptersFindRepoRoot walks upward from the working directory to the first
// parent containing .blueprint/.
func adaptersFindRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, ".blueprint")); err == nil && st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .blueprint/ directory found in %s or any parent — run `blueprint init` (greenfield) or `blueprint adopt` (brownfield) first", dir)
		}
		dir = parent
	}
}

type syncJSONOutput struct {
	Mode       string           `json:"mode"` // sync | dry-run | check | revert
	Written    []string         `json:"written,omitempty"`
	BackedUp   []string         `json:"backed_up,omitempty"`
	Unchanged  []string         `json:"unchanged,omitempty"`
	Restored   []string         `json:"restored,omitempty"`
	Removed    []string         `json:"removed,omitempty"`
	WouldWrite []string         `json:"would_write,omitempty"`
	Drift      []adapters.Drift `json:"drift,omitempty"`
}

func newSyncCmd() *cobra.Command {
	var checkFlag, revertFlag, dryRunFlag bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Generate the per-tool adapter surfaces from .blueprint/ sources",
		Long: "Projects AGENTS.md, .blueprint/steering, and the [mcp.servers] config onto Claude Code, Cursor,\n" +
			"Codex CLI, Windsurf, GitHub Copilot, and Gemini CLI surfaces (shims, rules, command stubs,\n" +
			"MCP configs, managed .gitignore block).\n" +
			"Idempotent; non-generated files get a .bak before their first overwrite. --check is the CI drift\n" +
			"gate (exit 1 + file list); --revert restores the .bak set; --dry-run prints the would-be writes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := 0
			for _, b := range []bool{checkFlag, revertFlag, dryRunFlag} {
				if b {
					modes++
				}
			}
			if modes > 1 {
				return fmt.Errorf("--check, --revert, and --dry-run are exclusive modes — pass at most one")
			}
			repoRoot, err := adaptersFindRepoRoot()
			if err != nil {
				return err
			}
			plan, err := adapters.Build(repoRoot)
			if err != nil {
				return err
			}
			jsonMode := adaptersJSONMode(cmd)
			out := cmd.OutOrStdout()

			switch {
			case checkFlag:
				tmp, err := os.MkdirTemp("", "blueprint-sync-check-*")
				if err != nil {
					return fmt.Errorf("cannot create temp dir for drift check: %w", err)
				}
				defer os.RemoveAll(tmp)
				drifts, err := adapters.Check(repoRoot, plan, tmp)
				if err != nil {
					return err
				}
				if jsonMode {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					_ = enc.Encode(syncJSONOutput{Mode: "check", Drift: drifts})
				} else if len(drifts) == 0 {
					fmt.Fprintln(out, "sync check: clean — generated surfaces match the canonical sources")
				} else {
					fmt.Fprintf(out, "sync check: %d file(s) drifted from the canonical sources:\n", len(drifts))
					for _, d := range drifts {
						fmt.Fprintf(out, "  %s (%s)\n", d.Path, d.Reason)
					}
				}
				if len(drifts) > 0 {
					return fmt.Errorf("adapter surfaces drifted — run `blueprint sync` to regenerate, or edit the canonical source (.blueprint/steering, config.toml, AGENTS.md), never the generated file")
				}
				return nil

			case revertFlag:
				res, err := adapters.Revert(repoRoot, plan)
				if err != nil {
					return err
				}
				if jsonMode {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					_ = enc.Encode(syncJSONOutput{Mode: "revert", Restored: res.Restored, Removed: res.Removed})
				} else {
					for _, p := range res.Restored {
						fmt.Fprintf(out, "restored %s (from %s.bak)\n", p, p)
					}
					for _, p := range res.Removed {
						fmt.Fprintf(out, "removed %s\n", p)
					}
					if len(res.Restored)+len(res.Removed) == 0 {
						fmt.Fprintln(out, "nothing to revert: no .bak files and no generated surfaces present")
					}
				}
				return nil

			case dryRunFlag:
				var would []string
				for _, f := range plan.Files {
					existing, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(f.Path)))
					if err == nil && string(existing) == string(f.Content) {
						continue
					}
					would = append(would, f.Path)
				}
				if jsonMode {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					_ = enc.Encode(syncJSONOutput{Mode: "dry-run", WouldWrite: would})
				} else if len(would) == 0 {
					fmt.Fprintln(out, "dry run: everything up to date, nothing to write")
				} else {
					fmt.Fprintf(out, "dry run: would write %d file(s):\n", len(would))
					for _, p := range would {
						fmt.Fprintf(out, "  %s\n", p)
					}
				}
				return nil

			default:
				res, err := adapters.Sync(repoRoot, plan)
				if err != nil {
					return err
				}
				if jsonMode {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					_ = enc.Encode(syncJSONOutput{
						Mode: "sync", Written: res.Written,
						BackedUp: res.BackedUp, Unchanged: res.Unchanged,
					})
				} else {
					for _, p := range res.BackedUp {
						fmt.Fprintf(out, "backed up %s\n", p)
					}
					for _, p := range res.Written {
						fmt.Fprintf(out, "wrote %s\n", p)
					}
					fmt.Fprintf(out, "sync: %d written, %d unchanged, %d backed up\n",
						len(res.Written), len(res.Unchanged), len(res.BackedUp))
				}
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&checkFlag, "check", false, "regenerate to a temp dir and fail (exit 1) listing any drifted files")
	cmd.Flags().BoolVar(&revertFlag, "revert", false, "restore the .bak set and remove generated-only surfaces")
	cmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "print the files sync would write, write nothing")
	return cmd
}

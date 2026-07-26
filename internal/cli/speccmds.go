// speccmds.go registers the spec-feature commands: `blueprint lint` (spec /
// trace / all) and `blueprint close <id>`. Owned by the spec feature — other
// features register their commands in their own files.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/lint"
	"blueprint/internal/spec"
)

func init() {
	Register(newSpecLintCmd())
	Register(newSpecCloseCmd())
}

// specJSONMode reads the root --json persistent flag; false when the command runs
// detached from the root (tests).
func specJSONMode(cmd *cobra.Command) bool {
	f := cmd.Root().PersistentFlags().Lookup("json")
	if f == nil {
		return false
	}
	v, _ := strconv.ParseBool(f.Value.String())
	return v
}

// specFindRepoRoot walks upward from the working directory to the first parent
// containing .blueprint/.
func specFindRepoRoot() (string, error) {
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

// specLintFileConfig is the optional [lint] table of .blueprint/config.toml.
type specLintFileConfig struct {
	Lint struct {
		BannedWords       []string `toml:"banned_words"`        // replaces the default list
		ExtraBannedWords  []string `toml:"extra_banned_words"`  // appends to it
		RelativeDateWords []string `toml:"relative_date_words"` // replaces the default list
	} `toml:"lint"`
}

func specLoadLintConfig(repoRoot string) lint.Config {
	var fileCfg specLintFileConfig
	path := filepath.Join(repoRoot, ".blueprint", "config.toml")
	// Best-effort: a missing or partially-foreign config.toml never blocks
	// linting; unknown keys belong to other features.
	if _, err := os.Stat(path); err == nil {
		_, _ = toml.DecodeFile(path, &fileCfg)
	}
	return lint.Config{
		BannedWords:       fileCfg.Lint.BannedWords,
		ExtraBannedWords:  fileCfg.Lint.ExtraBannedWords,
		RelativeDateWords: fileCfg.Lint.RelativeDateWords,
	}
}

func newSpecLintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "lint [spec|trace|backfill|knowledge|all]",
		Short:     "Run the deterministic spec and traceability linters",
		Long:      "Pure static checks, no model, no network: EARS conformance, banned vague words, verify blocks, REQ ID hygiene, task quality (spec); bidirectional `verifies: REQ-...` coverage (trace); knowledge-store freshness, dead links, orphans, index caps, human-verify budget (knowledge). Exit 1 on any error-severity finding.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"spec", "trace", "backfill", "knowledge", "all"},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := "all"
			if len(args) == 1 {
				mode = args[0]
			}
			repoRoot, err := specFindRepoRoot()
			if err != nil {
				return err
			}
			cfg := specLoadLintConfig(repoRoot)

			var findings []core.LintFinding
			switch mode {
			case "spec":
				findings, err = lint.Spec(repoRoot, cfg)
			case "trace":
				findings, err = lint.Trace(repoRoot, cfg)
			case "backfill":
				findings, err = lint.Backfill(repoRoot, time.Now().UTC())
			case "knowledge":
				findings, err = knowledgeLintFindings(repoRoot)
			case "all":
				findings, err = lint.All(repoRoot, cfg)
				if err == nil {
					bf, berr := lint.Backfill(repoRoot, time.Now().UTC())
					if berr != nil {
						return berr
					}
					findings = append(findings, bf...)
					kf, kerr := knowledgeLintFindings(repoRoot)
					if kerr != nil {
						return kerr
					}
					findings = append(findings, kf...)
				}
			default:
				return fmt.Errorf("unknown lint target %q — use spec, trace, backfill, knowledge, or all", mode)
			}
			if err != nil {
				return err
			}
			return specEmitFindings(cmd, findings)
		},
	}
	return cmd
}

func specEmitFindings(cmd *cobra.Command, findings []core.LintFinding) error {
	errs, warns := 0, 0
	for _, f := range findings {
		if f.Severity == lint.SevError {
			errs++
		} else {
			warns++
		}
	}
	if specJSONMode(cmd) {
		out := struct {
			Findings []core.LintFinding `json:"findings"`
			Errors   int                `json:"errors"`
			Warnings int                `json:"warnings"`
		}{Findings: findings, Errors: errs, Warnings: warns}
		if out.Findings == nil {
			out.Findings = []core.LintFinding{}
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		w := cmd.OutOrStdout()
		for _, f := range findings {
			fmt.Fprintf(w, "%s:%d: %s: [%s] %s\n    fix: %s\n", f.File, f.Line, f.Severity, f.Rule, f.Message, f.Remediation)
		}
		fmt.Fprintf(w, "lint: %d error(s), %d warning(s)\n", errs, warns)
	}
	if errs > 0 {
		return fmt.Errorf("%d error-severity finding(s) — apply the fix lines above and re-run", errs)
	}
	return nil
}

func newSpecCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <id>",
		Short: "Merge a verified change into the living specs and archive it",
		Long:  "Mechanically merges the change's ADDED/MODIFIED/REMOVED delta into .blueprint/specs/<area>/spec.md by REQ ID, harvests its Design section into specs/<area>/design.md, and moves the change folder to .blueprint/archive/.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			repoRoot, err := specFindRepoRoot()
			if err != nil {
				return err
			}
			if err := spec.Close(repoRoot, id); err != nil {
				return err
			}
			if specJSONMode(cmd) {
				enc := json.NewEncoder(cmd.OutOrStdout())
				return enc.Encode(map[string]string{
					"closed":   id,
					"archived": filepath.ToSlash(filepath.Join(".blueprint", "archive", id)),
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "closed %s: delta merged into living specs; folder archived at .blueprint/archive/%s\n", id, id)
			return nil
		},
	}
}

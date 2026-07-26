// doctorcmds.go — CLI surface for the doctor+autonomy feature (DESIGN §8,
// §14): `blueprint doctor` and `blueprint autonomy [recompute|show]`. Owned
// by the doctor feature; other features never edit this file (CONTRACTS
// package map).
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"blueprint/internal/autonomy"
	"blueprint/internal/core"
	"blueprint/internal/doctor"
	"blueprint/internal/worklog"
	"strings"
	"time"
)

func init() {
	Register(newDoctorCmd())
	Register(newAutonomyCmd())
}

func doctorRepoRoot() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %v", err)
	}
	return root, nil
}

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health-check the environment: git, forge enforcement profile, PATH, dev-env runbook, hook liveness, adoption stage exits",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := doctorRepoRoot()
			if err != nil {
				return err
			}
			jsonOut, _ := cmd.Flags().GetBool("json")
			stage, _ := cmd.Flags().GetInt("adopt-stage")
			runDevEnv, _ := cmd.Flags().GetBool("run-dev-env")
			installHooks, _ := cmd.Flags().GetBool("install-hooks")
			force, _ := cmd.Flags().GetBool("force")

			if installHooks {
				res, err := doctor.InstallHooks(root, force)
				if err != nil {
					return err
				}
				if jsonOut {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(res)
				}
				for _, p := range res.Written {
					fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", p)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "map-freshness shims installed — they run `blueprint map --refresh --quiet` when blueprint is on PATH")
				return nil
			}

			rep, err := doctor.Run(root, doctor.Options{AdoptStage: stage, RunDevEnv: runDevEnv})
			if err != nil {
				return err
			}

			// A green stage-exit is journaled so time_to_onboard can compute
			// the stage-0..4 span (worked-example friction #1). Only the
			// stage checks gate the event, not unrelated doctor findings.
			if stage >= 0 {
				stagePass := true
				for _, c := range rep.Checks {
					if strings.HasPrefix(c.Name, fmt.Sprintf("adopt-%d", stage)) && !c.Pass {
						stagePass = false
					}
				}
				if stagePass {
					_ = worklog.Append(root, core.JournalEvent{
						Time: time.Now().UTC(),
						Kind: "adopt-stage",
						Data: map[string]any{"stage": stage},
					})
				}
			}

			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			} else {
				printDoctorTable(cmd, rep)
			}
			if stage >= 0 {
				// Stage-exit mode is a machine-settleable gate (AC-9): only
				// the adopt-<n>-* checks decide the exit code; general
				// hygiene findings still print above as guidance.
				for _, c := range rep.Checks {
					if strings.HasPrefix(c.Name, fmt.Sprintf("adopt-%d", stage)) && !c.Pass {
						return fmt.Errorf("adoption stage %d exit checks failed — follow the remediation column above, then re-run `blueprint doctor --adopt-stage %d`", stage, stage)
					}
				}
				return nil
			}
			if !rep.Pass {
				return fmt.Errorf("doctor found failing checks — follow the remediation column above and re-run `blueprint doctor`")
			}
			return nil
		},
	}
	cmd.Flags().Int("adopt-stage", -1, "also run the exit checks for adoption stage N (0-4, see ADOPT.md)")
	cmd.Flags().Bool("run-dev-env", false, "actually execute the dev-env runbook's first fenced code block (must exit 0)")
	cmd.Flags().Bool("install-hooks", false, "write post-commit/post-merge shims that run `blueprint map --refresh --quiet` (repo-map freshness)")
	cmd.Flags().Bool("force", false, "with --install-hooks: replace existing hook files that are not blueprint shims")
	return cmd
}

func printDoctorTable(cmd *cobra.Command, rep *doctor.Report) {
	w := cmd.OutOrStdout()
	nameW := len("check")
	for _, c := range rep.Checks {
		if len(c.Name) > nameW {
			nameW = len(c.Name)
		}
	}
	fmt.Fprintf(w, "%-*s  %-4s  %s\n", nameW, "check", "pass", "remediation")
	for _, c := range rep.Checks {
		mark := "ok"
		note := c.Detail
		if !c.Pass {
			mark = "FAIL"
			note = c.Remediation
		}
		fmt.Fprintf(w, "%-*s  %-4s  %s\n", nameW, c.Name, mark, note)
	}
}

func newAutonomyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autonomy [recompute|show]",
		Short: "Autonomy ladder: show per-class levels, or recompute stats (proposes promotions as a diff, auto-applies demotions)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := doctorRepoRoot()
			if err != nil {
				return err
			}
			jsonOut, _ := cmd.Flags().GetBool("json")
			sub := "show"
			if len(args) == 1 {
				sub = args[0]
			}
			switch sub {
			case "show":
				return autonomyShow(cmd, root, jsonOut)
			case "recompute":
				return autonomyRecompute(cmd, root, jsonOut)
			default:
				return fmt.Errorf("unknown autonomy subcommand %q — use `blueprint autonomy show` or `blueprint autonomy recompute`", sub)
			}
		},
	}
	return cmd
}

func autonomyShow(cmd *cobra.Command, root string, jsonOut bool) error {
	f, err := autonomy.Load(root)
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(f)
	}
	w := cmd.OutOrStdout()
	mode := "advisory"
	if f.Profile.Enforced {
		mode = "enforced"
	}
	fmt.Fprintf(w, "forge: %s (%s)\n", f.Profile.Forge, mode)
	if f.Profile.Notes != "" {
		fmt.Fprintf(w, "notes: %s\n", f.Profile.Notes)
	}
	if len(f.Classes) == 0 {
		fmt.Fprintln(w, "classes: none recorded — every scenario class starts at L1; run `blueprint autonomy recompute` after verified changes accumulate")
		return nil
	}
	for _, s := range f.Scenarios() {
		c := f.Classes[s]
		fmt.Fprintf(w, "  %s: L%d (%d run(s) in window)\n", s, int(c.Level), len(c.Window))
	}
	return nil
}

func autonomyRecompute(cmd *cobra.Command, root string, jsonOut bool) error {
	res, err := autonomy.Recompute(root, autonomy.Options{})
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	w := cmd.OutOrStdout()
	for _, cs := range res.Classes {
		fmt.Fprintf(w, "%s: L%d — runs %d, clean %.1f%%, rework %.1f%%, tampers %d — %s\n",
			cs.Scenario, int(cs.Level), cs.Runs, cs.CleanRate*100, cs.ReworkRate*100, cs.Tampers, cs.Reason)
	}
	for _, d := range res.Demotions {
		fmt.Fprintf(w, "DEMOTED %s: L%d -> L%d (%s) — journaled\n", d.Scenario, int(d.From), int(d.To), d.Reason)
	}
	if res.ProposalPath != "" {
		fmt.Fprintf(w, "PROMOTION PROPOSAL written to %s\n%s\n", res.ProposalPath, res.Instructions)
	} else if len(res.Classes) == 0 {
		fmt.Fprintln(w, "no runs recorded yet — verified changes feed the window; nothing to evaluate")
	} else {
		fmt.Fprintln(w, "no class eligible for promotion; no proposal emitted")
	}
	return nil
}

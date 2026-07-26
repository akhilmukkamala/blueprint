// Metrics-feature commands (DESIGN §15, AC-8): `blueprint metrics` folds the
// worklog + change journals + git into reports/metrics.json, and `blueprint
// bench retrieval` is the AC-8 harness skeleton. This file is owned by the
// metrics feature — other features register commands in their own files.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/bench"
	"blueprint/internal/metrics"
)

func init() {
	Register(newMetricsCmd())
	Register(newBenchCmd())
}

// metricsJSONMode reads the root --json persistent flag; false when the
// command runs detached from the root (tests).
func metricsJSONMode(cmd *cobra.Command) bool {
	f := cmd.Root().PersistentFlags().Lookup("json")
	if f == nil {
		return false
	}
	v, _ := strconv.ParseBool(f.Value.String())
	return v
}

// metricsFindRepoRoot walks upward from the working directory to the first
// parent containing .blueprint/.
func metricsFindRepoRoot() (string, error) {
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

func newMetricsCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Fold worklog, change journals, and git into reports/metrics.json (DESIGN §15)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && format != "prom" {
				return fmt.Errorf("unknown --format %q — supported: prom (Prometheus text); use --json for the full report", format)
			}
			repoRoot, err := metricsFindRepoRoot()
			if err != nil {
				return err
			}
			report, err := metrics.Compute(repoRoot, metrics.Options{})
			if err != nil {
				return err
			}
			if err := metrics.WriteReport(repoRoot, report); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case metricsJSONMode(cmd):
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			case format == "prom":
				fmt.Fprint(out, metrics.FormatProm(report))
			default:
				fmt.Fprint(out, metrics.FormatHuman(report))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: prom (Prometheus text exposition)")
	return cmd
}

func newBenchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Measurement harnesses (AC-8)",
	}
	cmd.AddCommand(newBenchRetrievalCmd())
	return cmd
}

func newBenchRetrievalCmd() *cobra.Command {
	var (
		tasksPath    string
		initSuite    bool
		generate     bool
		instructions string
		instrTier    string
		recordTask   string
		recordTier   string
		tokens       int
		toolCalls    int
		seconds      float64
		success      bool
		notes        string
	)
	cmd := &cobra.Command{
		Use:   "retrieval",
		Short: "A/B retrieval benchmark: tier0 grep baseline vs configured retrieval tiers, joint token+success reporting",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := metricsFindRepoRoot()
			if err != nil {
				return err
			}
			path := tasksPath
			if path == "" {
				path = bench.DefaultSuitePath(repoRoot)
			}
			if initSuite {
				if err := bench.WriteSampleSuite(path); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "sample task suite written to %s — edit the tasks, pin post-cutoff commit_refs, then re-run `blueprint bench retrieval`\n", path)
				return nil
			}
			if generate {
				suite, err := bench.GenerateSuite(repoRoot, nil)
				if err != nil {
					return err
				}
				if err := bench.WriteGeneratedSuite(path, suite); err != nil {
					return err
				}
				if metricsJSONMode(cmd) {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{"path": path, "tasks": len(suite.Tasks), "suite": suite})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "generated %d task(s) from this repo's history into %s — review the prompts, then run `blueprint bench retrieval`\n", len(suite.Tasks), path)
				return nil
			}
			tiers := bench.ActiveTiers(repoRoot)
			suite, err := bench.LoadSuite(path)
			if err != nil {
				return err
			}

			if instructions != "" {
				if instrTier == "" {
					return fmt.Errorf("--instructions needs --tier — e.g. --instructions %s --tier %s", instructions, bench.Tier0Grep)
				}
				if !bench.ValidTier(instrTier, tiers) {
					return fmt.Errorf("unknown or unconfigured tier %q — active tiers: %s (opt-in tiers join via [retrieval] tables in .blueprint/config.toml)", instrTier, bench.TierNames(tiers))
				}
				for _, t := range suite.Tasks {
					if t.ID == instructions {
						text := bench.Instructions(t, bench.Tier(instrTier))
						if metricsJSONMode(cmd) {
							enc := json.NewEncoder(cmd.OutOrStdout())
							enc.SetIndent("", "  ")
							return enc.Encode(map[string]any{"task_id": t.ID, "tier": instrTier, "instructions": text})
						}
						fmt.Fprint(cmd.OutOrStdout(), text)
						return nil
					}
				}
				return fmt.Errorf("task %q is not in %s — check the suite's [[tasks]] ids", instructions, path)
			}

			if recordTask != "" || recordTier != "" {
				if recordTask == "" || recordTier == "" {
					return fmt.Errorf("recording needs both --record-task and --record-tier — e.g. --record-task T1 --record-tier %s", bench.Tier0Grep)
				}
				if !bench.ValidTier(recordTier, tiers) {
					return fmt.Errorf("unknown or unconfigured tier %q — active tiers: %s (opt-in tiers join via [retrieval] tables in .blueprint/config.toml)", recordTier, bench.TierNames(tiers))
				}
				if !taskInSuite(suite, recordTask) {
					return fmt.Errorf("task %q is not in %s — check the suite's [[tasks]] ids", recordTask, path)
				}
				for _, req := range []string{"tokens", "tool-calls", "seconds", "success"} {
					if !cmd.Flags().Changed(req) {
						return fmt.Errorf("--%s is required when recording — tokens, tool calls, wall clock, and the success verdict are always recorded together (RESEARCH 3.5: never a token number without the quality bar)", req)
					}
				}
				if err := bench.Record(repoRoot, bench.Result{
					TaskID: recordTask, Tier: bench.Tier(recordTier),
					Tokens: tokens, ToolCalls: toolCalls, WallClockS: seconds,
					Success: success, Notes: notes,
				}); err != nil {
					return err
				}
			}

			results, warns, err := bench.ReadResults(repoRoot)
			if err != nil {
				return err
			}
			for _, w := range warns {
				fmt.Fprintln(cmd.ErrOrStderr(), "note:", w)
			}
			report := bench.BuildReport(suite, tiers, results, time.Now().UTC())
			if err := bench.WriteReport(repoRoot, report); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if metricsJSONMode(cmd) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			printBenchHuman(out, repoRoot, suite, report)
			return nil
		},
	}
	cmd.Flags().StringVar(&tasksPath, "tasks", "", "task suite TOML (default .blueprint/bench/retrieval.toml)")
	cmd.Flags().BoolVar(&initSuite, "init", false, "scaffold a sample task suite and exit")
	cmd.Flags().BoolVar(&generate, "generate", false, "auto-generate the task suite from this repo's archive + git history and exit")
	cmd.Flags().StringVar(&instructions, "instructions", "", "print the manual-run protocol for one task id (requires --tier) and exit")
	cmd.Flags().StringVar(&instrTier, "tier", "", "tier for --instructions (e.g. tier0-grep, tier1-map)")
	cmd.Flags().StringVar(&recordTask, "record-task", "", "record a human-run result: task id")
	cmd.Flags().StringVar(&recordTier, "record-tier", "", "record a human-run result: tier (tier0-grep | tier1-map)")
	cmd.Flags().IntVar(&tokens, "tokens", 0, "recorded tokens consumed")
	cmd.Flags().IntVar(&toolCalls, "tool-calls", 0, "recorded tool calls")
	cmd.Flags().Float64Var(&seconds, "seconds", 0, "recorded wall clock in seconds")
	cmd.Flags().BoolVar(&success, "success", false, "recorded verified task success")
	cmd.Flags().StringVar(&notes, "notes", "", "optional notes for the recorded run")
	return cmd
}

func taskInSuite(s *bench.Suite, id string) bool {
	for _, t := range s.Tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

func printBenchHuman(out io.Writer, repoRoot string, suite *bench.Suite, report *bench.Report) {
	fmt.Fprintf(out, "bench retrieval — suite %q → %s\n", report.Suite, bench.ReportPath(repoRoot))
	for _, s := range report.Summaries {
		fmt.Fprintf(out, "  %-11s %d recorded · success %.0f%% · mean %.0f tokens · %.1f tool calls · %.0fs\n",
			s.Tier, s.Recorded, s.SuccessRate*100, s.MeanTokens, s.MeanToolCalls, s.MeanWallClock)
	}
	if m := report.Margin; m != nil {
		fmt.Fprintf(out, "  margin over %d paired task(s): tokens ×%.2f · tool calls ×%.2f · wall ×%.2f · success delta %+.0f%%\n",
			m.Tasks, m.TokenRatio, m.ToolCallRatio, m.WallRatio, m.SuccessDelta*100)
	}
	for _, cm := range report.ClassMargins {
		fmt.Fprintf(out, "  %-13s margin over %d task(s): tokens ×%.2f · tool calls ×%.2f · wall ×%.2f · success delta %+.0f%%\n",
			cm.QueryClass, cm.Tasks, cm.TokenRatio, cm.ToolCallRatio, cm.WallRatio, cm.SuccessDelta*100)
	}
	if len(report.Pending) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%d run(s) pending. Next up:\n", len(report.Pending))
	next := report.Pending[0]
	for _, t := range suite.Tasks {
		if t.ID == next.TaskID {
			fmt.Fprint(out, bench.Instructions(t, next.Tier))
			break
		}
	}
	if len(report.Pending) > 1 {
		fmt.Fprint(out, "remaining:")
		for _, p := range report.Pending[1:] {
			fmt.Fprintf(out, " %s/%s", p.TaskID, p.Tier)
		}
		fmt.Fprintln(out)
	}
}

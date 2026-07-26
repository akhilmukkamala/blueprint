// Install-lifecycle commands (DESIGN §2, §11, §14): `blueprint init` /
// `adopt` / `upgrade` / `uninstall`. This file is owned by the install
// feature — other features register commands in their own files.
package cli

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/adapters"
	"blueprint/internal/install"
)

// installNow is injected in tests; the wall clock is used only for explicit
// timestamps written to baselines/worklog (CONTRACTS rule 5).
var installNow = time.Now

func init() {
	Register(initCommand())
	Register(adoptCommand())
	Register(upgradeCommand())
	Register(uninstallCommand())
}

func initCommand() *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install the Blueprint floor into this repo (greenfield; idempotent)",
		Long: "Writes the four-file floor — AGENTS.md, CLAUDE.md, .blueprint/config.toml,\n" +
			".blueprint/manifest.json — printing the plan before writing. Existing files are\n" +
			"never overwritten; use `blueprint adopt` on a repo with existing agent files.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			plan, err := install.PlanInit(repoRoot)
			if err != nil {
				return err
			}
			if !jsonFlag(cmd) {
				fmt.Fprintln(cmd.OutOrStdout(), "plan:")
				for _, a := range plan {
					line := fmt.Sprintf("  %-11s %-28s [%s]", a.Op, a.Path, a.Tier)
					if a.Note != "" {
						line += " — " + a.Note
					}
					fmt.Fprintln(cmd.OutOrStdout(), line)
				}
			}
			res, err := install.Init(repoRoot, install.InitOptions{Offline: offline})
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "initialized blueprint %s: %d written, %d skipped\n",
				res.Version, len(res.Written), len(res.Skipped))
			fmt.Fprintln(cmd.OutOrStdout(), "next: describe your first change with `blueprint new \"<intent>\"`")
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "air-gapped install (templates are embedded; nothing changes except the record)")
	return cmd
}

func adoptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Adopt Blueprint in an existing repo (stage 0: import, baselines, floor)",
		Long: "Runs adoption stage 0 (DESIGN §14): imports existing CLAUDE.md / .cursorrules /\n" +
			".cursor/rules into .blueprint/steering/ as provenance-tagged drafts, installs the\n" +
			"floor, captures trailing-90-day git baselines into .blueprint/baselines.json, and\n" +
			"records the stage-0 worklog event.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			res, err := install.Adopt(repoRoot, installNow())
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, res)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "adopted (stage 0): floor installed, %d file(s) imported into .blueprint/steering/\n", len(res.Imported))
			for _, p := range res.Imported {
				fmt.Fprintf(out, "  imported %s\n", p)
			}
			if res.Shim {
				fmt.Fprintln(out, "  CLAUDE.md replaced with the @AGENTS.md shim (original content preserved in steering/)")
			}
			b := res.Baselines
			if b.Note != "" {
				fmt.Fprintf(out, "baselines: %s\n", b.Note)
			} else {
				fmt.Fprintf(out, "baselines (trailing %dd): %d commits, revert rate %.1f%%, fix-commit rate %.1f%%\n",
					b.WindowDays, b.Commits, 100*b.RevertRate, 100*b.FixRate)
			}
			fmt.Fprintln(out, "next: curate AGENTS.md to <=120 lines (stage 1), then wire `blueprint verify` into CI (stage 2)")
			return nil
		},
	}
	return cmd
}

func upgradeCommand() *cobra.Command {
	var dryRun, diff bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade installed files from the current templates, honoring ownership tiers",
		Long: "Three-way merge (DESIGN §11): [tool] files are replaced, [user] files are never\n" +
			"touched, [mixed] files are merged only inside the blueprint:managed region — a\n" +
			"hand-edited region gets inline conflict markers. Refuses on a dirty git tree;\n" +
			"committing the result (exactly one commit) is yours to do.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			res, err := install.Upgrade(repoRoot, install.UpgradeOptions{DryRun: dryRun, WithDiff: diff})
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, res)
			}
			out := cmd.OutOrStdout()
			mode := "upgraded"
			if res.DryRun {
				mode = "would upgrade (dry run)"
			}
			fmt.Fprintf(out, "%s %s -> %s\n", mode, res.FromVersion, res.ToVersion)
			for _, f := range res.Files {
				line := fmt.Sprintf("  %-14s %-28s [%s]", f.Action, f.Path, f.Tier)
				if f.Note != "" {
					line += " — " + f.Note
				}
				fmt.Fprintln(out, line)
				if f.Diff != "" {
					fmt.Fprintln(out, indent(f.Diff, "    "))
				}
			}
			if res.Conflicts > 0 {
				fmt.Fprintf(out, "%d conflict(s): resolve the inline markers, then commit everything as one upgrade commit\n", res.Conflicts)
			} else if !res.DryRun {
				// Upgrade ends by regenerating adapters (DESIGN §11/G15):
				// new templates may change generated surfaces.
				if plan, aerr := adapters.Build(repoRoot); aerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: adapter regeneration failed (%v) — run `blueprint sync` manually\n", aerr)
				} else if _, serr := adapters.Sync(repoRoot, plan); serr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: adapter sync failed (%v) — run `blueprint sync` manually\n", serr)
				} else {
					fmt.Fprintln(out, "adapters regenerated (`blueprint sync`)")
				}
				fmt.Fprintln(out, "done — commit the result as one upgrade commit")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "compute and print the upgrade without writing anything")
	cmd.Flags().BoolVar(&diff, "diff", false, "show unified diffs for files that would change")
	return cmd
}

func uninstallCommand() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Blueprint tool files; the repo keeps its specs and knowledge",
		Long: "Removes [tool]-tier files and strips managed regions from [mixed] files.\n" +
			"[user] files — specs, knowledge, steering — stay: the repo keeps its memory.\n" +
			"--purge deletes .blueprint/ entirely (asks for confirmation unless --json).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			if purge && !jsonFlag(cmd) {
				fmt.Fprint(cmd.OutOrStdout(), "--purge permanently deletes .blueprint/ (specs, knowledge, worklog history). Type \"yes\" to continue: ")
				sc := bufio.NewScanner(cmd.InOrStdin())
				if !sc.Scan() || strings.TrimSpace(strings.ToLower(sc.Text())) != "yes" {
					return fmt.Errorf("purge aborted — nothing was removed; re-run and answer \"yes\", or drop --purge to keep .blueprint/")
				}
			}
			res, err := install.Uninstall(repoRoot, purge)
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, res)
			}
			out := cmd.OutOrStdout()
			for _, p := range res.Removed {
				fmt.Fprintf(out, "  removed  %s\n", p)
			}
			for _, p := range res.Stripped {
				fmt.Fprintf(out, "  stripped %s (managed region removed; your content kept)\n", p)
			}
			if res.Purged {
				fmt.Fprintln(out, "  purged   .blueprint/")
			}
			if len(res.Remaining) > 0 {
				fmt.Fprintln(out, "remaining:")
				for _, r := range res.Remaining {
					fmt.Fprintf(out, "  %-28s %s\n", r.Path, r.Reason)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete .blueprint/ entirely (confirmation required unless --json)")
	return cmd
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

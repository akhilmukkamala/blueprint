// gardencmds.go — CLI surface for the gardenhooks feature (DESIGN §9):
// `blueprint garden`, the manual doc-gardening report. Owned by the
// gardenhooks feature; other features never edit this file (CONTRACTS
// package map).
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"blueprint/internal/garden"
)

func init() {
	Register(gardenCommand())
}

func gardenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "garden",
		Short: "Doc-gardening report: stale/orphaned knowledge, spec warnings, map staleness, unarchived changes, slop — with the exact fix for each",
		Long: "Folds knowledge-lint findings, spec-lint warnings, repo-map staleness,\n" +
			"archive candidates, and slop candidates (oversize + near-duplicate knowledge\n" +
			"files) into one prioritized fix list. Read-only: it opens no PRs and writes\n" +
			"nothing — gardening success is measured in net lines deleted (DESIGN §9).\n" +
			"Automation is deferred; this is the manual report.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			rep, err := garden.Run(repoRoot, garden.Options{})
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, rep)
			}
			w := cmd.OutOrStdout()
			if len(rep.Items) == 0 {
				fmt.Fprintln(w, rep.Summary)
				return nil
			}
			for i, it := range rep.Items {
				loc := it.File
				if it.Line > 0 {
					loc = fmt.Sprintf("%s:%d", it.File, it.Line)
				}
				if loc == "" {
					loc = "-"
				}
				fmt.Fprintf(w, "%2d. P%d %-17s %s\n      %s\n      fix: %s\n", i+1, it.Priority, it.Category, loc, it.Detail, it.Fix)
			}
			fmt.Fprintf(w, "\n%s\n", rep.Summary)
			return nil
		},
	}
	return cmd
}

// Tier-1 retrieval command (DESIGN §9): `blueprint map` renders the
// tree-sitter repo map inside a token budget. This file is owned by the
// repomap feature — other features register commands in their own files.
package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/repomap"
)

func init() {
	Register(mapCommand())
}

type mapResult struct {
	Generated time.Time `json:"generated"`
	Files     int       `json:"files"`
	Budget    int       `json:"budget"`
	Tokens    int       `json:"tokens"`
	Cache     string    `json:"cache"`
	Map       string    `json:"map"`
}

func mapCommand() *cobra.Command {
	var (
		refresh bool
		budget  int
		quiet   bool
	)
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Render the ranked repo map (tree-sitter symbols + import graph + churn)",
		Long: "Builds or loads the Tier-1 repo map: top-level symbols per file, ranked by\n" +
			"PageRank over the import graph mixed with git churn, rendered inside a token\n" +
			"budget. Cached at .blueprint/map.json and invalidated when sources change.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if budget <= 0 {
				return fmt.Errorf("--budget must be positive (tokens); the default %d yields the ~1-2k-token map", repomap.DefaultBudget)
			}
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			m, err := repomap.LoadOrBuild(repoRoot, refresh)
			if err != nil {
				return err
			}
			if quiet {
				// Hook mode (doctor --install-hooks shims): refresh the
				// cache, print nothing — hooks must never noise up commits.
				return nil
			}
			rendered := m.Render(budget)
			res := mapResult{
				Generated: m.Generated,
				Files:     len(m.Files),
				Budget:    budget,
				Tokens:    repomap.TokenEstimate(rendered),
				Cache:     repomap.CachePath(repoRoot),
				Map:       rendered,
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, res)
			}
			fmt.Fprint(cmd.OutOrStdout(), rendered)
			fmt.Fprintf(cmd.ErrOrStderr(), "%d files · ~%d tokens · cache %s\n", res.Files, res.Tokens, res.Cache)
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "rebuild even if the cache is fresh")
	cmd.Flags().IntVar(&budget, "budget", repomap.DefaultBudget, "token budget for the rendered map")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "build/refresh the cache and print nothing (hook mode)")
	return cmd
}

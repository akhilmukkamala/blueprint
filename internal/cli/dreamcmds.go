// dreamcmds.go — CLI surface for the dreaming loop (DESIGN §12, AC-10):
// `blueprint dream [--json] [--branch]`. Owned by the dream feature; other
// features never edit this file (CONTRACTS package map). The command is
// manual — weekly scheduling stays a documentation recipe, and any model
// call happens only inside the user-configured external [dream] command.
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"blueprint/internal/dream"
)

func init() {
	Register(newDreamCmd())
}

func newDreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dream",
		Short: "Fold journals into an evidence-cited self-improvement proposal branch (human merge only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			jsonOut, _ := cmd.Flags().GetBool("json")
			branch, _ := cmd.Flags().GetBool("branch")

			res, err := dream.Run(root, dream.Options{Branch: branch})
			if err != nil {
				return err
			}

			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			out := cmd.OutOrStdout()
			if res.NoSignal {
				// AC-10 hard no-op: nothing was written, nothing journaled.
				fmt.Fprintln(out, "no signal, nothing to dream about")
				return nil
			}
			for _, w := range res.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "note:", w)
			}
			if res.DryRun {
				fmt.Fprint(out, res.Proposal)
				fmt.Fprintf(out, "\n(dry run: nothing written; re-run without --branch=false to create %s)\n", res.Branch)
				return nil
			}
			fmt.Fprintf(out, "dream proposal committed on %s\n", res.Branch)
			fmt.Fprintf(out, "  %s (%d items", res.ProposalPath, len(res.Items))
			if len(res.PatchPaths) > 0 {
				fmt.Fprintf(out, ", %d patch files", len(res.PatchPaths))
			}
			fmt.Fprintln(out, ")")
			for _, it := range res.Items {
				tag := ""
				if it.Quarantined {
					tag = " [quarantined]"
				}
				fmt.Fprintf(out, "  %s%s — %s\n", it.ID, tag, it.Title)
			}
			if len(res.Scrubbed) > 0 {
				fmt.Fprintf(out, "  secret scrubber redacted content (rules: %v) — review before pushing\n", res.Scrubbed)
			}
			fmt.Fprintln(out, "next:", res.Instructions)
			return nil
		},
	}
	cmd.Flags().Bool("branch", true, "create the agent/dream/<date> branch with the committed proposal; --branch=false prints the proposal and writes nothing")
	return cmd
}

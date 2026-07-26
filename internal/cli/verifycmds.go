// verifycmds.go — CLI surface for the verify feature (DESIGN §7):
// `blueprint verify <id>` and `blueprint approve <id>`. Owned by the verify
// feature; other features never edit this file (CONTRACTS package map).
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/repomap"
	"blueprint/internal/route"
	"blueprint/internal/spec"
	"blueprint/internal/verify"
)

func init() {
	Register(newVerifyCmd())
	Register(newApproveCmd())
}

func verifyRepoRoot() (string, error) {
	return findRepoRoot()
}

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <change-id>",
		Short: "Run the verifier pipeline for a change: lint, tamper check, declared checks, domain verifiers, model checker",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := verifyRepoRoot()
			if err != nil {
				return err
			}
			jsonOut, _ := cmd.Flags().GetBool("json")

			// Mid-flight ceremony escalation (DESIGN §4, AC-7): before
			// verifying, re-run the router on the current diff. Draft changes
			// escalate in place (persisted + journaled by Reevaluate); an
			// approved change whose diff outgrew its tier must go back
			// through a human — verification at the stale tier would attest
			// ceremony that no longer matches the risk.
			if c, lerr := spec.LoadChange(root, args[0]); lerr == nil {
				if router, rerr := route.Load(root); rerr == nil {
					if m, mapErr := repomap.LoadOrBuild(root, false); mapErr == nil {
						router.Blast = m
					}
					if stats, serr := route.GitDiffStats(root); serr == nil {
						wasApproved := c.Status == core.StatusApproved || c.Status == core.StatusVerified
						if dec, escalated, eerr := router.Reevaluate(root, c, stats); eerr != nil {
							return eerr
						} else if escalated {
							if wasApproved {
								return fmt.Errorf("verify %s: the diff outgrew the approved tier (now %s: %s) — re-approve at the new tier: update change.md (tier + Design section if full), then `blueprint approve %s --amend`", args[0], dec.Tier, dec.Reasons[0].Detail, args[0])
							}
							if serr := spec.SaveChange(root, c); serr != nil {
								return serr
							}
							fmt.Fprintf(cmd.ErrOrStderr(), "note: mid-flight escalation to %s — %s\n", dec.Tier, dec.Reasons[0].Detail)
						}
					}
				}
			}

			v, err := verify.Run(root, args[0], verify.Options{})
			if err != nil {
				return err
			}

			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(v); err != nil {
					return err
				}
			} else {
				for _, c := range v.Checks {
					mark := "PASS"
					if !c.Pass {
						mark = "FAIL"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s", mark, c.Name)
					if c.ReqID != "" {
						fmt.Fprintf(cmd.OutOrStdout(), " (%s)", c.ReqID)
					}
					fmt.Fprintln(cmd.OutOrStdout())
					if c.Detail != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "       %s\n", c.Detail)
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "fingerprint: %s\n", v.Fingerprint)
			}

			// Exit nonzero on fail/tamper: verify is the CI gate (DESIGN §7).
			if v.Tamper {
				return fmt.Errorf("verify %s: TAMPER — approved inputs drifted; see the tamper check above and `blueprint approve %s --amend` if the evolution is legitimate", v.ChangeID, v.ChangeID)
			}
			if !v.Pass {
				return fmt.Errorf("verify %s: failing checks remain; fix the findings above and re-run `blueprint verify %s`", v.ChangeID, v.ChangeID)
			}
			return nil
		},
	}
}

func newApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <change-id>",
		Short: "Human approval gate: stamp approved.lock (tamper evidence) for a change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := verifyRepoRoot()
			if err != nil {
				return err
			}
			jsonOut, _ := cmd.Flags().GetBool("json")
			amend, _ := cmd.Flags().GetBool("amend")

			lk, err := verify.Approve(root, args[0], amend, verify.Options{})
			if err != nil {
				return err
			}

			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(lk)
			}
			verb := "approved"
			if amend {
				verb = "re-approved (amend, journaled)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "change %s %s: %d file(s) locked, %d spec-linked test(s), composite %.12s...\n",
				lk.ChangeID, verb, len(lk.Files), lk.SpecLinkedTests, lk.Composite)
			return nil
		},
	}
	cmd.Flags().Bool("amend", false, "re-stamp approved.lock after legitimate spec/test evolution (logged to the journal)")
	return cmd
}

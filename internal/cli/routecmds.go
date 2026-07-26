// Ceremony-router commands (DESIGN §4): `blueprint new` classifies an intent
// into exempt/light/full and scaffolds the change; `blueprint registry list`
// shows the pre-approved standard-change classes. This file is owned by the
// route feature — other features register commands in their own files.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/install"
	"blueprint/internal/repomap"
	"blueprint/internal/route"
	"blueprint/internal/spec"
	"blueprint/internal/worklog"
)

// Seams for the spec-package API (internal/spec, agent A). The defaults write
// the same .blueprint/changes/<id>/change.md contract; at merge time the
// integrator points these at spec.SaveChange / spec.ChangePath.
var (
	saveChange = spec.SaveChange
	changePath = spec.ChangePath
)

// routeNow is injected in tests; production uses the wall clock only for the
// journal timestamp and the Sev-1 SLA, both explicit (CONTRACTS rule 5).
var routeNow = time.Now

func init() {
	Register(newCommand())
	Register(registryCommand())
}

type newResult struct {
	ChangeID   string            `json:"change_id,omitempty"`
	Intent     string            `json:"intent"`
	Type       string            `json:"type"`
	Tier       core.CeremonyTier `json:"tier"`
	Status     core.ChangeStatus `json:"status,omitempty"`
	Reasons    []route.Reason    `json:"reasons"`
	Cost       route.Cost        `json:"cost"`
	ChangePath string            `json:"change_path,omitempty"`
	SkillPath  string            `json:"skill_path,omitempty"` // scenario playbook materialized on first use
	Worktree   string            `json:"worktree,omitempty"`
	SLA        string            `json:"sla,omitempty"`
}

func newCommand() *cobra.Command {
	var (
		changeType string
		paths      []string
		tierFlag   string
		reason     string
		sev1       bool
		worktree   bool
	)
	cmd := &cobra.Command{
		Use:   "new \"<intent>\"",
		Short: "Route an intent through the ceremony router and scaffold the change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			intent := strings.TrimSpace(args[0])
			if intent == "" {
				return fmt.Errorf("intent is empty — describe the change, e.g.: blueprint new \"fix login redirect loop\"")
			}
			if !route.ValidType(changeType) {
				return fmt.Errorf("unknown change type %q — use one of: %s", changeType, strings.Join(route.ChangeTypes, ", "))
			}
			if tierFlag != "" && strings.TrimSpace(reason) == "" {
				return fmt.Errorf("--tier is a human override and must be justified — re-run with --reason \"why\"; overrides are logged to the worklog for the ceremony-fit metric")
			}
			var override core.CeremonyTier
			if tierFlag != "" {
				switch core.CeremonyTier(tierFlag) {
				case core.TierExempt, core.TierLight, core.TierFull:
					override = core.CeremonyTier(tierFlag)
				default:
					return fmt.Errorf("unknown tier %q — valid tiers: exempt, light, full", tierFlag)
				}
			}

			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			router, err := route.Load(repoRoot)
			if err != nil {
				return err
			}
			// Tier-1 repo map upgrades blast radius from the path-count
			// fallback to transitive-importer counting; on any map failure
			// the router keeps the fallback (graceful degradation, G14).
			if m, mapErr := repomap.LoadOrBuild(repoRoot, false); mapErr == nil {
				router.Blast = m
			}

			in := route.Inputs{ChangeType: changeType, Paths: paths}
			if len(paths) == 0 {
				stats, err := route.GitDiffStats(repoRoot)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: %v; routing on declared inputs only\n", err)
				} else {
					in.Paths, in.ChangedLOC = stats.Paths, stats.ChangedLOC
				}
			}

			decision := router.Decide(in)
			now := routeNow().UTC()

			if override != "" {
				decision.Reasons = append(decision.Reasons, route.Reason{
					Axis:   "override",
					Detail: fmt.Sprintf("human override %s → %s: %s", decision.Tier, override, reason),
					Tier:   override,
				})
				decision.Tier = override
				decision.Cost = route.CostFor(override)
			}

			res := newResult{
				Intent:  intent,
				Type:    changeType,
				Tier:    decision.Tier,
				Reasons: decision.Reasons,
				Cost:    decision.Cost,
			}

			// Exempt = no spec, no contract, one worklog line (DESIGN §4) —
			// unless Sev-1, which always opens a backfill record.
			scaffold := decision.Tier != core.TierExempt || sev1
			var c *core.Change
			if scaffold {
				c = &core.Change{
					ID:       route.UniqueChangeID(repoRoot, intent, now),
					Title:    intent,
					Type:     changeType,
					Tier:     decision.Tier,
					Status:   core.StatusDraft,
					Scenario: scenarioFor(changeType, sev1),
				}
				if sev1 {
					c.Status = core.StatusBackfill
					sla := now.Add(72 * time.Hour)
					c.SLA = &sla
					decision.Reasons = append(decision.Reasons, route.Reason{
						Axis:   "sev1",
						Detail: fmt.Sprintf("Sev-1 ship-now path: backfill record opened, SLA %s (72h)", sla.Format(time.RFC3339)),
					})
					res.Reasons = decision.Reasons
					res.SLA = sla.Format(time.RFC3339)
				}
				c.Contract = route.DefaultContract(c.ID, decision.Tier)
				if err := saveChange(repoRoot, c); err != nil {
					return err
				}
				res.ChangeID = c.ID
				res.Status = c.Status
				res.ChangePath = changePath(repoRoot, c.ID)
				// First use of a scenario materializes its bundled SKILL.md
				// playbook ([user], optional — verifierpacks feature). Failure
				// is a note, never a routing failure.
				// First use also materializes the bundled verifier pack and the
				// standard-change registry defaults ([user]-tier, written once,
				// never overwritten) so the scenario's checks actually run.
				for _, rel := range []string{".blueprint/verifiers.toml", ".blueprint/registry.toml"} {
					if _, merr := install.Materialize(repoRoot, rel); merr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "note: could not materialize %s: %v\n", rel, merr)
					}
				}
				if created, err := install.MaterializeScenarioSkill(repoRoot, c.Scenario); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: could not materialize scenario playbook: %v\n", err)
				} else if created {
					res.SkillPath = install.SkillPath(c.Scenario)
				}
			}

			// Every routing decision is one worklog line; overrides get their
			// own event so the ceremony-fit metric can count them directly.
			ev := core.JournalEvent{
				Time: now,
				Kind: "route",
				Data: map[string]any{
					"intent":     intent,
					"type":       changeType,
					"tier":       string(decision.Tier),
					"est_tokens": decision.Cost.Tokens,
					"sev1":       sev1,
					"paths":      in.Paths,
				},
			}
			if c != nil {
				ev.ChangeID = c.ID
			}
			if err := worklog.Append(repoRoot, ev); err != nil {
				return err
			}
			if override != "" {
				ovEv := core.JournalEvent{
					Time: now,
					Kind: "override",
					Data: map[string]any{"tier": string(override), "reason": reason, "intent": intent},
				}
				if c != nil {
					ovEv.ChangeID = c.ID
				}
				if err := worklog.Append(repoRoot, ovEv); err != nil {
					return err
				}
			}

			if worktree {
				if c == nil {
					return fmt.Errorf("--worktree needs a change branch, but exempt changes have none — drop --worktree or override the tier (--tier light --reason ...)")
				}
				wt, err := addWorktree(repoRoot, c.ID)
				if err != nil {
					return err
				}
				res.Worktree = wt
			}

			if jsonFlag(cmd) {
				return printJSON(cmd, res)
			}
			printHumanNew(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&changeType, "type", "feat", "conventional-commits change type ("+strings.Join(route.ChangeTypes, "|")+")")
	cmd.Flags().StringSliceVar(&paths, "paths", nil, "declared touched paths (default: derived from git diff)")
	cmd.Flags().StringVar(&tierFlag, "tier", "", "human tier override (exempt|light|full); requires --reason")
	cmd.Flags().StringVar(&reason, "reason", "", "justification for --tier; logged to the worklog")
	cmd.Flags().BoolVar(&sev1, "sev1", false, "Sev-1 ship-now path: backfill-due status with a 72h SLA")
	cmd.Flags().BoolVar(&worktree, "worktree", false, "create ../<repo>-wt/<change-id> on branch agent/<change-id>")
	return cmd
}

func registryCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "registry",
		Short: "Inspect the standard-change registry (pre-approved exempt classes)",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List registry classes from .blueprint/registry.toml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			reg, err := route.LoadRegistry(repoRoot)
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, reg)
			}
			out := cmd.OutOrStdout()
			if len(reg.Classes) == 0 {
				fmt.Fprintln(out, "registry is empty — add [[class]] tables (name, type, globs, max_loc, checks) to .blueprint/registry.toml; expanding the registry is itself a light-tier reviewed change")
				return nil
			}
			for _, c := range reg.Classes {
				fmt.Fprintf(out, "%-20s type=%-8s max_loc=%-5d globs=%s checks=%s\n",
					c.Name, orDash(c.Type), c.MaxLOC, strings.Join(c.Globs, ","), orDash(strings.Join(c.Checks, ",")))
			}
			return nil
		},
	}
	root.AddCommand(list)
	return root
}

func printHumanNew(cmd *cobra.Command, res newResult) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "tier: %s  (type=%s)\n", res.Tier, res.Type)
	for _, r := range res.Reasons {
		if r.Tier != "" {
			fmt.Fprintf(out, "  [%s → %s] %s\n", r.Axis, r.Tier, r.Detail)
		} else {
			fmt.Fprintf(out, "  [%s] %s\n", r.Axis, r.Detail)
		}
	}
	fmt.Fprintln(out, res.Cost.Line())
	if res.ChangePath != "" {
		fmt.Fprintf(out, "change: %s (%s)\n", res.ChangePath, res.Status)
	} else {
		fmt.Fprintln(out, "exempt: no change folder; one worklog line appended (existing verifiers still run)")
	}
	if res.SkillPath != "" {
		fmt.Fprintf(out, "playbook: %s (materialized on first use of this scenario)\n", res.SkillPath)
	}
	if res.SLA != "" {
		fmt.Fprintf(out, "sev-1 backfill SLA: %s\n", res.SLA)
	}
	if res.Worktree != "" {
		fmt.Fprintf(out, "worktree: %s (branch agent/%s)\n", res.Worktree, res.ChangeID)
	}
}

// addWorktree runs `git worktree add ../<repo>-wt/<change-id> -b agent/<id>`
// from the repo root (DESIGN §6 parallelism model).
func addWorktree(repoRoot, changeID string) (string, error) {
	rel := filepath.Join("..", filepath.Base(repoRoot)+"-wt", changeID)
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", rel, "-b", "agent/"+changeID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %s — ensure the repo has at least one commit and branch agent/%s does not already exist", strings.TrimSpace(string(out)), changeID)
	}
	return filepath.Join(repoRoot, rel), nil
}

// scenarioFor maps the declared type onto the DESIGN §5 scenario taxonomy.
func scenarioFor(changeType string, sev1 bool) string {
	if sev1 {
		return "sev1-hotfix"
	}
	switch changeType {
	case "fix":
		return "bug-fix"
	case "refactor":
		return "refactor"
	case "perf":
		return "performance"
	case "docs", "style", "chore", "build", "ci":
		return "chore"
	default:
		return "feature"
	}
}

// jsonFlag reads the root --json persistent flag; tolerant so the command also
// works when registered under a root that does not define it.
func jsonFlag(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("json")
	return err == nil && v
}

func printJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// findRepoRoot walks up from the working directory to the first dir holding
// .blueprint or .git; falls back to the working directory itself.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %w", err)
	}
	for d := dir; ; {
		for _, marker := range []string{".blueprint", ".git"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d, nil
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir, nil
		}
		d = parent
	}
}

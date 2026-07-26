// Loop-harness commands (DESIGN §6): resume, status, log. This file is owned
// by the loop feature — other features register their commands in their own
// files, never here.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/lint"
	"blueprint/internal/loop"
	"blueprint/internal/spec"
)

func init() {
	Register(newResumeCmd())
	Register(newStatusCmd())
	Register(newLogCmd())
}

// jsonMode honors the root --json persistent flag; a local --json flag (used
// when a command runs without the root, e.g. in tests) takes precedence.
func jsonMode(cmd *cobra.Command) bool {
	for _, fs := range []interface{ GetBool(string) (bool, error) }{cmd.Flags(), cmd.InheritedFlags()} {
		if v, err := fs.GetBool("json"); err == nil && v {
			return true
		}
	}
	return false
}

func repoRoot() (string, error) {
	// Shared walk-up discovery (routecmds.findRepoRoot): loop commands must
	// work from subdirectories exactly like every other command.
	return findRepoRoot()
}

func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume <id>",
		Short: "Print the cold-start resume context pack for a change (predicate-first, re-dispatch guarded)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			// Park-on-trip before resuming (DESIGN §6: never silent retry):
			// a tripped breaker or blown cap parks the loop; resume reports
			// the parked state instead of rebuilding context.
			if c, lerr := spec.LoadChange(root, args[0]); lerr == nil {
				if bv, eerr := loop.Enforce(root, args[0], c.Contract); eerr == nil && bv.Trip {
					return fmt.Errorf("change %s is parked: %s tripped (%s) — review the journal (`blueprint log %s`), fix the blocker or raise the cap in change.md, then re-approve if caps changed", args[0], bv.Pattern, strings.Join(bv.Evidence, "; "), args[0])
				}
			}

			rc, err := loop.BuildResumeContext(root, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonMode(cmd) {
				return emitJSON(out, rc)
			}
			renderResume(out, rc)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "emit machine-readable JSON output")
	return cmd
}

func renderResume(w io.Writer, rc *loop.ResumeContext) {
	if rc.PredicateGreen {
		fmt.Fprintf(w, "change %s is DONE — predicate %q exited 0; state.json marked done. Nothing to resume.\n",
			rc.ChangeID, rc.PredicateCmd)
		if rc.PredicateOutput != "" {
			fmt.Fprintf(w, "\npredicate output:\n%s\n", rc.PredicateOutput)
		}
		return
	}
	fmt.Fprintf(w, "RESUME CONTEXT — change %s\n", rc.ChangeID)
	if rc.PredicateCmd != "" {
		fmt.Fprintf(w, "\n== predicate (ran first, still red) ==\n$ %s\n%s\n", rc.PredicateCmd, rc.PredicateOutput)
	} else {
		fmt.Fprintf(w, "\n== predicate ==\n(no [loop] predicate declared in change.md — add one; it is the only stop condition)\n")
	}
	fmt.Fprintf(w, "\n== 1. AGENTS.md ==\n%s\n", strings.TrimRight(rc.AgentsMD, "\n"))
	fmt.Fprintf(w, "\n== 2. change.md ==\n%s\n", strings.TrimRight(rc.ChangeMD, "\n"))
	fmt.Fprintf(w, "\n== 3. state.json ==\nphase=%s task=%s iteration=%d consumed: %d iters, %.1f min, $%.2f\n",
		rc.State.Phase, loopOrDash(rc.State.Task), rc.State.Iteration,
		rc.State.Consumed.Iterations, rc.State.Consumed.Minutes, rc.State.Consumed.USD)
	if len(rc.State.Blockers) > 0 {
		fmt.Fprintf(w, "blockers: %s\n", strings.Join(rc.State.Blockers, "; "))
	}
	if rc.State.Summary != "" {
		fmt.Fprintf(w, "summary: %s\n", rc.State.Summary)
	}
	fmt.Fprintf(w, "\n== 4. last %d journal events ==\n", len(rc.JournalTail))
	for _, ev := range rc.JournalTail {
		fmt.Fprintln(w, renderEvent(ev))
	}
	fmt.Fprintf(w, "\n== 5. git sanity ==\nstatus:\n%s\ndiff stat:\n%s\n",
		loopOrDash(rc.GitStatus), loopOrDash(rc.GitDiffStat))
	fmt.Fprintf(w, "\n== re-dispatch guard ==\n")
	refused := 0
	for _, g := range rc.Guards {
		if !g.Refuse {
			continue
		}
		refused++
		fmt.Fprintf(w, "REFUSE re-dispatch of %s (%s):\n", g.TaskID, g.Text)
		for _, e := range g.Evidence {
			fmt.Fprintf(w, "  - %s\n", e)
		}
	}
	if refused == 0 {
		fmt.Fprintln(w, "(no completed tasks on record — all tasks dispatchable)")
	}
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [<id>]",
		Short: "Show loop status for one change (phase, caps, breaker) or every change in the repo",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return changeStatus(cmd, root, args[0])
			}
			return repoStatus(cmd, root)
		},
	}
	cmd.Flags().Bool("json", false, "emit machine-readable JSON output")
	return cmd
}

// changeStatusView is the status payload for one change.
type changeStatusView struct {
	ChangeID string              `json:"change_id"`
	State    *loop.State         `json:"state"`
	Consumed loop.Consumption    `json:"consumed"`
	Caps     core.LoopContract   `json:"caps"`
	Breaker  loop.BreakerVerdict `json:"breaker"`
	Events   int                 `json:"journal_events"`
}

func buildChangeStatus(root, id string) (*changeStatusView, error) {
	state, err := loop.LoadState(root, id)
	if err != nil {
		return nil, err
	}
	events, err := loop.ReadJournal(root, id)
	if err != nil {
		return nil, err
	}
	v := &changeStatusView{ChangeID: id, State: state, Consumed: loop.Consumed(events), Events: len(events)}
	change, err := loop.Source().LoadChange(root, id)
	if err == nil {
		v.Caps = change.Contract
		v.Breaker = loop.Evaluate(events, change.Contract)
	}
	return v, nil
}

func changeStatus(cmd *cobra.Command, root, id string) error {
	v, err := buildChangeStatus(root, id)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if jsonMode(cmd) {
		return emitJSON(out, v)
	}
	fmt.Fprintf(out, "change %s: phase=%s task=%s iteration=%d\n", v.ChangeID, v.State.Phase, loopOrDash(v.State.Task), v.State.Iteration)
	fmt.Fprintf(out, "consumed: %d/%s iterations, %.1f/%s minutes, $%.2f/%s\n",
		v.Consumed.Iterations, capStr(float64(v.Caps.MaxIterations), "%.0f"),
		v.Consumed.Minutes, capStr(float64(v.Caps.MaxMinutes), "%.0f"),
		v.Consumed.USD, capStr(v.Caps.MaxUSD, "$%.2f"))
	if v.Breaker.Trip {
		fmt.Fprintf(out, "breaker: TRIPPED (%s)\n", v.Breaker.Pattern)
		for _, e := range v.Breaker.Evidence {
			fmt.Fprintf(out, "  - %s\n", e)
		}
		fmt.Fprintln(out, "next: read `blueprint log "+id+"`, fix the blocker, then `blueprint resume "+id+"`")
	} else {
		fmt.Fprintln(out, "breaker: clear")
	}
	if v.State.Phase == loop.PhaseParked {
		fmt.Fprintf(out, "parked: %s\n", v.State.Summary)
	}
	return nil
}

func repoStatus(cmd *cobra.Command, root string) error {
	ids, err := loop.Source().ListChanges(root)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	views := make([]*changeStatusView, 0, len(ids))
	for _, id := range ids {
		v, err := buildChangeStatus(root, id)
		if err != nil {
			return err
		}
		views = append(views, v)
	}
	// Sev-1 backfill banner (DESIGN §4): un-backfilled hotfixes are the
	// loudest thing on the board until their ceremony debt is paid.
	banners := backfillBanners(root, time.Now().UTC())
	if jsonMode(cmd) {
		return emitJSON(out, struct {
			Changes  []*changeStatusView `json:"changes"`
			Backfill []string            `json:"backfill_warnings,omitempty"`
		}{Changes: views, Backfill: banners})
	}
	for _, b := range banners {
		fmt.Fprintln(out, b)
	}
	if len(views) == 0 {
		fmt.Fprintln(out, "no changes found under .blueprint/changes — start one with `blueprint new`")
		return nil
	}
	for _, v := range views {
		mark := "clear"
		if v.Breaker.Trip {
			mark = "TRIPPED:" + v.Breaker.Pattern
		}
		fmt.Fprintf(out, "%-24s phase=%-8s iters=%-3d $%.2f breaker=%s\n",
			v.ChangeID, v.State.Phase, v.Consumed.Iterations, v.Consumed.USD, mark)
	}
	return nil
}

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log <id>",
		Short: "Render a change's journal for humans; --json streams the raw ndjson events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			id := args[0]
			events, err := loop.ReadJournal(root, id)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonMode(cmd) {
				enc := json.NewEncoder(out) // one raw event per line: stream, don't wrap
				for _, ev := range events {
					if err := enc.Encode(ev); err != nil {
						return err
					}
				}
				return nil
			}
			if len(events) == 0 {
				fmt.Fprintf(out, "no journal for change %s yet — events appear once the loop runs (path: %s)\n",
					id, loop.JournalPath(root, id))
				return nil
			}
			for _, ev := range events {
				fmt.Fprintln(out, renderEvent(ev))
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "stream raw ndjson events")
	return cmd
}

// renderEvent is the human line for one journal event: time, kind, and the
// interesting data fields per kind.
func renderEvent(ev core.JournalEvent) string {
	t := ev.Time.UTC().Format("2006-01-02 15:04:05")
	var detail string
	switch ev.Kind {
	case loop.KindIteration:
		detail = fmt.Sprintf("n=%v action=%v delta=%v", ev.Data["n"], ev.Data["action"], ev.Data["diff_delta"])
	case loop.KindCost:
		detail = fmt.Sprintf("$%v", ev.Data["usd"])
	case loop.KindTask:
		detail = fmt.Sprintf("%v -> %v", ev.Data["task_id"], ev.Data["status"])
	case loop.KindBreaker, loop.KindPark:
		detail = fmt.Sprintf("%v: %v", ev.Data["reason"], ev.Data["summary"])
	case loop.KindResume:
		detail = fmt.Sprintf("predicate_done=%v", ev.Data["predicate_done"])
	default:
		if len(ev.Data) > 0 {
			b, _ := json.Marshal(ev.Data)
			detail = string(b)
		}
	}
	return strings.TrimRight(fmt.Sprintf("%s  %-10s %s", t, ev.Kind, detail), " ")
}

func loopOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// capStr renders a cap value, with 0 meaning unlimited.
func capStr(v float64, format string) string {
	if v <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf(format, v)
}

// specChangeSource binds loop's change reading to the canonical spec package.
type specChangeSource struct{}

func (specChangeSource) LoadChange(repoRoot, id string) (*core.Change, error) {
	return spec.LoadChange(repoRoot, id)
}
func (specChangeSource) ListChanges(repoRoot string) ([]string, error) {
	return spec.ListChanges(repoRoot)
}
func (specChangeSource) ChangePath(repoRoot, id string) string {
	return spec.ChangePath(repoRoot, id)
}

func init() { loop.RegisterSource(specChangeSource{}) }

// backfillBanners renders Sev-1 backfill warnings from the backfill lint.
func backfillBanners(root string, now time.Time) []string {
	findings, err := lint.Backfill(root, now)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range findings {
		tag := "BACKFILL"
		if f.Severity == lint.SevError {
			tag = "BACKFILL-OVERDUE"
		}
		out = append(out, fmt.Sprintf("[%s] %s — %s", tag, f.Message, f.Remediation))
	}
	return out
}

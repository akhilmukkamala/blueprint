package verify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/lint"
	"blueprint/internal/route"
	"blueprint/internal/spec"
	"blueprint/internal/worklog"
)

// backfillGuard enforces Sev-1 time-shifted ceremony at verify time
// (DESIGN §4, ADR-0009): while a hotfix's 72h backfill SLA is lapsed, verify
// fails for changes whose paths overlap the hotfix (scoped); past the grace
// window it fails for every change. Verifying the backfill change itself is
// always allowed — that IS the remediation.
func backfillGuard(repoRoot, changeID string, now time.Time) core.CheckResult {
	res := core.CheckResult{Name: "backfill-guard", Pass: true}
	ids, err := spec.ListChanges(repoRoot)
	if err != nil {
		res.Detail = "skipped: " + err.Error()
		return res
	}

	var targetPaths []string
	if stats, err := route.GitDiffStats(repoRoot); err == nil {
		targetPaths = stats.Paths
	}
	targetPaths = append(targetPaths, worklogPaths(repoRoot, changeID)...)

	var failures []string
	for _, id := range ids {
		if id == changeID {
			continue
		}
		c, err := spec.LoadChange(repoRoot, id)
		if err != nil || c.Status != core.StatusBackfill || c.SLA == nil {
			continue
		}
		lapsed := now.Sub(*c.SLA)
		if lapsed <= 0 {
			continue // SLA still running; lint surfaces the countdown
		}
		if lapsed >= lint.BackfillGrace {
			failures = append(failures, fmt.Sprintf("%s lapsed %s ago (past the %dd grace window — repo-wide)", id, lapsed.Round(time.Hour), int(lint.BackfillGrace.Hours()/24)))
			continue
		}
		if overlap(targetPaths, worklogPaths(repoRoot, id)) {
			failures = append(failures, fmt.Sprintf("%s lapsed %s ago and overlaps this change's paths (scoped)", id, lapsed.Round(time.Hour)))
		}
	}
	if len(failures) > 0 {
		res.Pass = false
		res.ExitCode = 1
		res.Detail = "un-backfilled Sev-1 hotfix blocks verification:\n  - " + strings.Join(failures, "\n  - ") +
			"\nBackfill the hotfix first (EARS delta + regression test in its change.md, approve, verify) — ceremony is time-shifted, never skipped."
	}
	return res
}

// worklogPaths recovers a change's touched paths from its route events.
func worklogPaths(repoRoot, changeID string) []string {
	events, _, err := worklog.Read(repoRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, ev := range events {
		if ev.ChangeID != changeID || (ev.Kind != "route" && ev.Kind != "route-escalate") {
			continue
		}
		if raw, ok := ev.Data["paths"].([]any); ok {
			for _, p := range raw {
				if s, ok := p.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// overlap reports whether two path sets touch the same file or directory
// lineage (a/b/c.go overlaps a/b and a/b/c.go, not a/x).
func overlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, x := range a {
		for _, y := range b {
			if x == y || strings.HasPrefix(x, y+"/") || strings.HasPrefix(y, x+"/") {
				return true
			}
		}
	}
	return false
}

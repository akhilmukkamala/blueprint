package lint

import (
	"fmt"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/spec"
)

// BackfillGrace is how long after the 72h SLA lapse enforcement stays scoped
// (verify fails only for overlapping changes) before going repo-wide
// (ADR-0009: scoped first — repo-wide-red-on-one-lapse is over-ceremony).
const BackfillGrace = 7 * 24 * time.Hour

// Backfill lints Sev-1 time-shifted ceremony (DESIGN §4): every backfill-due
// change is surfaced; a lapse past the grace window is a repo-wide error.
func Backfill(repoRoot string, now time.Time) ([]core.LintFinding, error) {
	ids, err := spec.ListChanges(repoRoot)
	if err != nil {
		return nil, err
	}
	var out []core.LintFinding
	for _, id := range ids {
		c, err := spec.LoadChange(repoRoot, id)
		if err != nil || c.Status != core.StatusBackfill || c.SLA == nil {
			continue
		}
		f := core.LintFinding{
			File: spec.ChangePath(repoRoot, id),
			Rule: "sev1-backfill",
		}
		lapsed := now.Sub(*c.SLA)
		switch {
		case lapsed < 0:
			f.Severity = SevWarning
			f.Message = fmt.Sprintf("Sev-1 change %s owes its backfill in %s (SLA %s)", id, (-lapsed).Round(time.Hour), c.SLA.Format(time.RFC3339))
			f.Remediation = "add the EARS delta + red→green regression test to change.md, then `blueprint approve` and `blueprint verify` — ceremony is time-shifted, never skipped"
		case lapsed < BackfillGrace:
			f.Severity = SevWarning
			f.Message = fmt.Sprintf("Sev-1 change %s lapsed its 72h backfill SLA %s ago — verify now FAILS for changes overlapping this hotfix (scoped enforcement)", id, lapsed.Round(time.Hour))
			f.Remediation = "backfill immediately: EARS delta + regression test in change.md, then approve + verify; repo-wide enforcement starts " + c.SLA.Add(BackfillGrace).Format(time.RFC3339)
		default:
			f.Severity = SevError
			f.Message = fmt.Sprintf("Sev-1 change %s lapsed its backfill SLA more than %d days ago — repo-wide enforcement: every `blueprint verify` fails until it is backfilled", id, int(BackfillGrace.Hours()/24))
			f.Remediation = "backfill now (EARS delta + regression test, approve, verify) or, if the hotfix was reverted, close the record with a human-reviewed change"
		}
		out = append(out, f)
	}
	return out, nil
}

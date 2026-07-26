// recompute.go — the rate-based promotion/demotion function (DESIGN §8).
// Promotion is NEVER applied: eligible classes get a reviewable unified diff
// (.blueprint/autonomy-proposal.diff) a human applies and merges. Demotion is
// automatic, immediate, and journaled with evidence.
package autonomy

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// Thresholds (DESIGN §8): trailing window of W runs, minimum N to judge,
// clean rate >= 95%, rework rate <= 5%, zero tamper, rework horizon 30 days.
const (
	WindowRuns    = 30
	MinRuns       = 10
	CleanRateMin  = 0.95
	ReworkRateMax = 0.05
	ReworkDays    = 30
)

// Options carries the injectable impurities: the clock and the git revert
// probe. Zero value = wall clock + real git, per CONTRACTS rule 5 (explicit
// timestamps only).
type Options struct {
	Now     time.Time
	Reverts func(repoRoot string, since time.Time) (map[string]time.Time, error)
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now
}

// ClassStats is the per-class trailing-window evaluation.
type ClassStats struct {
	Scenario   string             `json:"scenario"`
	Level      core.AutonomyLevel `json:"level"`
	Runs       int                `json:"runs"`
	CleanRate  float64            `json:"clean_rate"`
	ReworkRate float64            `json:"rework_rate"`
	Tampers    int                `json:"tampers"`
	Eligible   bool               `json:"eligible"`
	Reason     string             `json:"reason"`
}

// LevelChange records one proposed promotion or applied demotion.
type LevelChange struct {
	Scenario string             `json:"scenario"`
	From     core.AutonomyLevel `json:"from"`
	To       core.AutonomyLevel `json:"to"`
	Reason   string             `json:"reason"`
}

// Result is what `blueprint autonomy recompute` reports.
type Result struct {
	Profile      core.EnforcementProfile `json:"profile"`
	Classes      []ClassStats            `json:"classes"`
	Demotions    []LevelChange           `json:"demotions"`
	Promotions   []LevelChange           `json:"promotions"`
	ProposalPath string                  `json:"proposal_path,omitempty"`
	Instructions string                  `json:"instructions,omitempty"`
}

// Recompute folds journals + worklog + git into per-class stats, auto-applies
// demotions (journaled), refreshes the stored windows, and emits a promotion
// proposal diff for eligible classes. It returns the full evaluation either
// way so `--json` consumers see the evidence.
func Recompute(repoRoot string, opts Options) (*Result, error) {
	now := opts.now()
	revertProbe := opts.Reverts
	if revertProbe == nil {
		revertProbe = gitReverts
	}
	reworkSince := now.Add(-ReworkDays * 24 * time.Hour)

	reverted, err := revertProbe(repoRoot, reworkSince)
	if err != nil {
		return nil, fmt.Errorf("autonomy: revert probe failed: %w", err)
	}
	runs, signals, err := collectRuns(repoRoot, reworkSince, reverted)
	if err != nil {
		return nil, err
	}
	f, err := Load(repoRoot)
	if err != nil {
		return nil, err
	}

	// A demotion is applied once per piece of evidence: signals at or before
	// the last journaled demotion for that class are spent.
	lastDemote, err := lastDemotions(repoRoot)
	if err != nil {
		return nil, err
	}

	// Evaluate every class that has state or runs.
	names := map[string]bool{}
	for s := range f.Classes {
		names[s] = true
	}
	for s := range runs {
		names[s] = true
	}
	scenarios := make([]string, 0, len(names))
	for s := range names {
		scenarios = append(scenarios, s)
	}
	sort.Strings(scenarios)

	res := &Result{Profile: f.Profile}
	demoted := map[string]bool{}

	for _, s := range scenarios {
		level := f.Level(s)
		window := trailing(runs[s], WindowRuns)

		// Demotion pass first: an escaped defect or tamper drops the class
		// one level regardless of how good the window looks.
		if reason, at, hit := freshestSignal(signals, s, lastDemote[s]); hit && level > core.L1Propose {
			from := level
			level--
			demoted[s] = true
			res.Demotions = append(res.Demotions, LevelChange{s, from, level, reason})
			if err := journalDemotion(repoRoot, now, s, from, level, reason, at); err != nil {
				return nil, err
			}
		}
		// Advisory profiles cannot hold L3: the host cannot enforce required
		// checks, so automerge is mechanically withdrawn (the RESEARCH §1.10
		// footgun), not merely refused for new promotions.
		if level == core.L3Automerge && !f.Profile.Enforced {
			reason := fmt.Sprintf("forge %q profile is advisory — L3 requires enforced required checks; verify branch protection or stay at L2", f.Profile.Forge)
			demoted[s] = true
			res.Demotions = append(res.Demotions, LevelChange{s, core.L3Automerge, core.L2Branch, reason})
			level = core.L2Branch
			if err := journalDemotion(repoRoot, now, s, core.L3Automerge, level, reason, now); err != nil {
				return nil, err
			}
		}

		f.Classes[s] = ClassState{Level: level, Window: window}
		res.Classes = append(res.Classes, evaluate(s, level, window, f.Profile, demoted[s]))
	}

	// Persist refreshed windows + applied demotions. Levels only ever move
	// DOWN in this write.
	if err := Save(repoRoot, f); err != nil {
		return nil, err
	}

	// Promotion proposals: build the proposed file and emit a unified diff.
	var proposed *File
	for _, cs := range res.Classes {
		if !cs.Eligible {
			continue
		}
		if proposed == nil {
			proposed = cloneFile(f)
		}
		st := proposed.Classes[cs.Scenario]
		st.Level = cs.Level + 1
		proposed.Classes[cs.Scenario] = st
		res.Promotions = append(res.Promotions, LevelChange{cs.Scenario, cs.Level, cs.Level + 1, cs.Reason})
	}

	if proposed == nil {
		// No eligible class: a stale proposal must not linger as an
		// apply-me artifact.
		if err := os.Remove(ProposalPath(repoRoot)); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("autonomy: cannot remove stale proposal: %w", err)
		}
		return res, nil
	}

	diff, err := proposalDiff(f, proposed, now, res.Promotions)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(ProposalPath(repoRoot), []byte(diff), 0o644); err != nil {
		return nil, fmt.Errorf("autonomy: cannot write proposal diff: %w", err)
	}
	res.ProposalPath = ProposalPath(repoRoot)
	res.Instructions = applyInstructions(res.Promotions)
	if err := worklog.Append(repoRoot, core.JournalEvent{
		Time: now, Kind: "autonomy",
		Data: map[string]any{"action": "propose", "promotions": len(res.Promotions), "proposal": ".blueprint/autonomy-proposal.diff"},
	}); err != nil {
		return nil, err
	}
	return res, nil
}

func trailing(rs []RunRecord, n int) []RunRecord {
	if len(rs) <= n {
		return rs
	}
	return rs[len(rs)-n:]
}

// freshestSignal returns the newest unspent demotion signal for a scenario.
func freshestSignal(signals []demoteSignal, scenario string, spentBefore time.Time) (string, time.Time, bool) {
	var best demoteSignal
	for _, sig := range signals {
		if sig.Scenario != scenario || !sig.Time.After(spentBefore) {
			continue
		}
		if sig.Time.After(best.Time) {
			best = sig
		}
	}
	return best.Reason, best.Time, !best.Time.IsZero()
}

// lastDemotions reads the worklog for the newest journaled demotion evidence
// time per scenario.
func lastDemotions(repoRoot string) (map[string]time.Time, error) {
	events, _, err := worklog.Read(repoRoot)
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, ev := range events {
		if ev.Kind != "autonomy" {
			continue
		}
		if action, _ := ev.Data["action"].(string); action != "demote" {
			continue
		}
		s, _ := ev.Data["scenario"].(string)
		at := ev.Time
		if raw, ok := ev.Data["evidence_time"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				at = t
			}
		}
		if s != "" && at.After(out[s]) {
			out[s] = at
		}
	}
	return out, nil
}

func journalDemotion(repoRoot string, now time.Time, scenario string, from, to core.AutonomyLevel, reason string, evidence time.Time) error {
	return worklog.Append(repoRoot, core.JournalEvent{
		Time: now, Kind: "autonomy",
		Data: map[string]any{
			"action": "demote", "scenario": scenario,
			"from": int(from), "to": int(to),
			"reason":        reason,
			"evidence_time": evidence.UTC().Format(time.RFC3339Nano),
		},
	})
}

// evaluate applies the DESIGN §8 gate to one class window.
func evaluate(scenario string, level core.AutonomyLevel, window []RunRecord, profile core.EnforcementProfile, justDemoted bool) ClassStats {
	cs := ClassStats{Scenario: scenario, Level: level, Runs: len(window)}
	clean, rework := 0, 0
	for _, r := range window {
		if r.Clean {
			clean++
		}
		if r.Rework {
			rework++
		}
		if r.Tamper {
			cs.Tampers++
		}
	}
	if cs.Runs > 0 {
		cs.CleanRate = float64(clean) / float64(cs.Runs)
		cs.ReworkRate = float64(rework) / float64(cs.Runs)
	}

	switch {
	case justDemoted:
		cs.Reason = "demoted this recompute — re-earn the window before promotion"
	case level >= core.L3Automerge:
		cs.Reason = "already at L3 (ceiling)"
	case cs.Runs < MinRuns:
		cs.Reason = fmt.Sprintf("only %d run(s) in window — need at least %d before rates mean anything", cs.Runs, MinRuns)
	case cs.Tampers > 0:
		cs.Reason = fmt.Sprintf("%d tamper event(s) in window — promotion requires zero", cs.Tampers)
	case cs.CleanRate < CleanRateMin:
		cs.Reason = fmt.Sprintf("clean rate %.1f%% below the %.0f%% bar", cs.CleanRate*100, CleanRateMin*100)
	case cs.ReworkRate > ReworkRateMax:
		cs.Reason = fmt.Sprintf("rework rate %.1f%% above the %.0f%% cap", cs.ReworkRate*100, ReworkRateMax*100)
	case level+1 == core.L3Automerge && !profile.Enforced:
		cs.Reason = fmt.Sprintf("window qualifies but forge %q profile is advisory — L3 refused; enable enforced branch protection + required checks first", profile.Forge)
	default:
		cs.Eligible = true
		cs.Reason = fmt.Sprintf("window of %d runs: clean %.1f%%, rework %.1f%%, zero tamper — qualifies for L%d",
			cs.Runs, cs.CleanRate*100, cs.ReworkRate*100, int(level+1))
	}
	return cs
}

func cloneFile(f *File) *File {
	out := &File{Profile: f.Profile, Classes: make(map[string]ClassState, len(f.Classes))}
	for k, v := range f.Classes {
		w := make([]RunRecord, len(v.Window))
		copy(w, v.Window)
		out.Classes[k] = ClassState{Level: v.Level, Window: w}
	}
	return out
}

// proposalDiff renders a PROMOTION PROPOSAL: human instructions as leading
// comment lines (ignored by `git apply`/`patch`) followed by a unified diff
// of autonomy.json. Never applied by the tool.
func proposalDiff(current, proposed *File, now time.Time, promotions []LevelChange) (string, error) {
	a, err := Marshal(current)
	if err != nil {
		return "", err
	}
	b, err := Marshal(proposed)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# PROMOTION PROPOSAL — generated by `blueprint autonomy recompute` at %s\n", now.UTC().Format(time.RFC3339))
	for _, p := range promotions {
		fmt.Fprintf(&sb, "# %s: L%d -> L%d — %s\n", p.Scenario, int(p.From), int(p.To), p.Reason)
	}
	sb.WriteString("# Review the evidence above, then apply on a branch and merge via PR:\n")
	sb.WriteString("#   git apply .blueprint/autonomy-proposal.diff\n")
	sb.WriteString("# Blueprint never applies promotions itself (DESIGN §8).\n")
	sb.WriteString(unifiedDiff(".blueprint/autonomy.json", string(a), string(b)))
	return sb.String(), nil
}

func applyInstructions(promotions []LevelChange) string {
	names := make([]string, len(promotions))
	for i, p := range promotions {
		names[i] = fmt.Sprintf("%s (L%d->L%d)", p.Scenario, int(p.From), int(p.To))
	}
	return fmt.Sprintf("Promotion proposed for %s. Review .blueprint/autonomy-proposal.diff, apply it with `git apply .blueprint/autonomy-proposal.diff` on a branch, and merge via PR. It is never applied automatically.",
		strings.Join(names, ", "))
}

// unifiedDiff emits a minimal-enough unified diff: common prefix and suffix
// lines become context, the changed middle is one hunk. Sufficient because
// both sides are canonical serializations differing only in level lines.
func unifiedDiff(name, a, b string) string {
	al := strings.SplitAfter(a, "\n")
	bl := strings.SplitAfter(b, "\n")
	// SplitAfter leaves a trailing "" element when the text ends in \n.
	if n := len(al); n > 0 && al[n-1] == "" {
		al = al[:n-1]
	}
	if n := len(bl); n > 0 && bl[n-1] == "" {
		bl = bl[:n-1]
	}
	pre := 0
	for pre < len(al) && pre < len(bl) && al[pre] == bl[pre] {
		pre++
	}
	suf := 0
	for suf < len(al)-pre && suf < len(bl)-pre && al[len(al)-1-suf] == bl[len(bl)-1-suf] {
		suf++
	}
	const ctx = 3
	cpre := pre - ctx
	if cpre < 0 {
		cpre = 0
	}
	csufA := len(al) - suf + ctx
	if csufA > len(al) {
		csufA = len(al)
	}
	csufB := len(bl) - suf + ctx
	if csufB > len(bl) {
		csufB = len(bl)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", name, name)
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", cpre+1, csufA-cpre, cpre+1, csufB-cpre)
	for _, l := range al[cpre:pre] {
		sb.WriteString(" " + l)
	}
	for _, l := range al[pre : len(al)-suf] {
		sb.WriteString("-" + l)
	}
	for _, l := range bl[pre : len(bl)-suf] {
		sb.WriteString("+" + l)
	}
	for _, l := range al[len(al)-suf : csufA] {
		sb.WriteString(" " + l)
	}
	return sb.String()
}

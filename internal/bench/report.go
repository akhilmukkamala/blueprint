// Joint reporting (RESEARCH §3.5): every row and every summary carries the
// verified-success column beside the token/tool-call/wall-clock numbers, and
// the cross-tier margin only exists once BOTH tiers have completed the same
// task — a token ratio without a success delta is unrepresentable here.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Row is one grid cell in the report. Pointer fields are nil while the run is
// pending, so a pending cell can never leak a partial number.
type Row struct {
	TaskID     string   `json:"task_id"`
	Tier       Tier     `json:"tier"`
	QueryClass string   `json:"query_class,omitempty"`
	Recorded   bool     `json:"recorded"`
	Tokens     *int     `json:"tokens,omitempty"`
	ToolCalls  *int     `json:"tool_calls,omitempty"`
	WallClockS *float64 `json:"wall_clock_seconds,omitempty"`
	Success    *bool    `json:"success,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

// TierSummary aggregates recorded runs of one tier. SuccessRate is mandatory
// context for every mean.
type TierSummary struct {
	Tier          Tier    `json:"tier"`
	Recorded      int     `json:"recorded"`
	SuccessRate   float64 `json:"success_rate"`
	MeanTokens    float64 `json:"mean_tokens"`
	MeanToolCalls float64 `json:"mean_tool_calls"`
	MeanWallClock float64 `json:"mean_wall_clock_seconds"`
}

// Margin is the A/B outcome over tasks recorded in BOTH tiers. Ratios are
// tier1/tier0 (<1 = map cheaper); SuccessDelta is tier1 − tier0 success rate
// and always accompanies the ratios.
type Margin struct {
	Tasks         int     `json:"tasks"`
	TokenRatio    float64 `json:"token_ratio"`
	ToolCallRatio float64 `json:"tool_call_ratio"`
	WallRatio     float64 `json:"wall_clock_ratio"`
	SuccessDelta  float64 `json:"success_delta"`
}

// ClassSummary is one (query class, tier) aggregate — the per-class joint
// numbers. SuccessRate is mandatory context for every mean, same as
// TierSummary.
type ClassSummary struct {
	QueryClass    string  `json:"query_class"`
	Tier          Tier    `json:"tier"`
	Recorded      int     `json:"recorded"`
	SuccessRate   float64 `json:"success_rate"`
	MeanTokens    float64 `json:"mean_tokens"`
	MeanToolCalls float64 `json:"mean_tool_calls"`
	MeanWallClock float64 `json:"mean_wall_clock_seconds"`
}

// ClassMargin is the tier1/tier0 margin restricted to one query class —
// retrieval wins are condition-bound (RESEARCH §3.5), so the report must say
// where the map pays off, not just whether.
type ClassMargin struct {
	QueryClass string `json:"query_class"`
	Margin
}

// Report is reports/bench/report.json.
type Report struct {
	Suite          string         `json:"suite"`
	GeneratedAt    time.Time      `json:"generated_at"`
	Rows           []Row          `json:"rows"`
	Summaries      []TierSummary  `json:"summaries"`
	ClassSummaries []ClassSummary `json:"class_summaries"`
	Margin         *Margin        `json:"margin"` // nil until ≥1 task completes in both tiers
	ClassMargins   []ClassMargin  `json:"class_margins"`
	Pending        []Run          `json:"pending"`
	Method         string         `json:"method"`
}

const methodNote = "Toggle-only-retrieval A/B on post-cutoff commits: same harness, same model, optimized grep baseline. " +
	"Tokens, tool calls, and wall clock are reported jointly with verified task success; no vendor numbers (RESEARCH 3.5)."

// acc accumulates joint numbers; success count travels with every sum.
type acc struct {
	n, ok, tokens, calls int
	wall                 float64
}

func (a *acc) add(r Result) {
	a.n++
	a.tokens += r.Tokens
	a.calls += r.ToolCalls
	a.wall += r.WallClockS
	if r.Success {
		a.ok++
	}
}

func (a *acc) summary() (successRate, meanTokens, meanCalls, meanWall float64) {
	n := float64(a.n)
	return float64(a.ok) / n, float64(a.tokens) / n, float64(a.calls) / n, a.wall / n
}

// pairedMargin computes the tier1/tier0 margin over tasks recorded in BOTH
// default tiers; nil when no task is paired — a token ratio without a success
// delta stays unrepresentable.
func pairedMargin(completed map[string]map[Tier]Result) *Margin {
	var pair, pairBase acc
	for _, byTier := range completed {
		base, hasBase := byTier[Tier0Grep]
		mapped, hasMap := byTier[Tier1Map]
		if !hasBase || !hasMap {
			continue
		}
		pairBase.add(base)
		pair.add(mapped)
	}
	if pair.n == 0 {
		return nil
	}
	return &Margin{
		Tasks:         pair.n,
		TokenRatio:    ratio(float64(pair.tokens), float64(pairBase.tokens)),
		ToolCallRatio: ratio(float64(pair.calls), float64(pairBase.calls)),
		WallRatio:     ratio(pair.wall, pairBase.wall),
		SuccessDelta:  float64(pair.ok)/float64(pair.n) - float64(pairBase.ok)/float64(pairBase.n),
	}
}

// BuildReport folds recorded results into the full grid. Unknown (task, tier)
// results are ignored so a stale log cannot invent rows. tiers is the active
// grid (ActiveTiers); every breakdown — per tier, per query class, margins —
// carries success beside the means (joint-reporting invariant).
func BuildReport(s *Suite, tiers []Tier, results []Result, now time.Time) *Report {
	latest := map[Run]Result{}
	for _, r := range results { // file order; later entries supersede
		latest[Run{TaskID: r.TaskID, Tier: r.Tier}] = r
	}

	classOf := map[string]string{} // task id -> query class (declared or auto)
	for _, t := range s.Tasks {
		classOf[t.ID] = TaskClass(t)
	}

	rep := &Report{Suite: s.Name, GeneratedAt: now.UTC(), Method: methodNote}
	perTier := map[Tier]*acc{}
	type classCell struct {
		class string
		tier  Tier
	}
	perClass := map[classCell]*acc{}
	completed := map[string]map[Tier]Result{}                 // task -> tier -> result
	classCompleted := map[string]map[string]map[Tier]Result{} // class -> task -> tier

	for _, run := range Plan(s, tiers) {
		class := classOf[run.TaskID]
		r, ok := latest[run]
		if !ok {
			rep.Rows = append(rep.Rows, Row{TaskID: run.TaskID, Tier: run.Tier, QueryClass: class})
			rep.Pending = append(rep.Pending, run)
			continue
		}
		tokens, calls, wall, success := r.Tokens, r.ToolCalls, r.WallClockS, r.Success
		rep.Rows = append(rep.Rows, Row{
			TaskID: run.TaskID, Tier: run.Tier, QueryClass: class, Recorded: true,
			Tokens: &tokens, ToolCalls: &calls, WallClockS: &wall, Success: &success,
			Notes: r.Notes,
		})
		a := perTier[run.Tier]
		if a == nil {
			a = &acc{}
			perTier[run.Tier] = a
		}
		a.add(r)
		cc := classCell{class, run.Tier}
		ca := perClass[cc]
		if ca == nil {
			ca = &acc{}
			perClass[cc] = ca
		}
		ca.add(r)
		if completed[run.TaskID] == nil {
			completed[run.TaskID] = map[Tier]Result{}
		}
		completed[run.TaskID][run.Tier] = r
		if classCompleted[class] == nil {
			classCompleted[class] = map[string]map[Tier]Result{}
		}
		classCompleted[class][run.TaskID] = completed[run.TaskID]
	}

	for _, tier := range tiers {
		a := perTier[tier]
		if a == nil || a.n == 0 {
			continue
		}
		sr, mt, mc, mw := a.summary()
		rep.Summaries = append(rep.Summaries, TierSummary{
			Tier: tier, Recorded: a.n,
			SuccessRate: sr, MeanTokens: mt, MeanToolCalls: mc, MeanWallClock: mw,
		})
	}

	// Per-class breakdown, class order × tier order for determinism.
	for _, class := range QueryClasses {
		for _, tier := range tiers {
			a := perClass[classCell{class, tier}]
			if a == nil || a.n == 0 {
				continue
			}
			sr, mt, mc, mw := a.summary()
			rep.ClassSummaries = append(rep.ClassSummaries, ClassSummary{
				QueryClass: class, Tier: tier, Recorded: a.n,
				SuccessRate: sr, MeanTokens: mt, MeanToolCalls: mc, MeanWallClock: mw,
			})
		}
	}

	rep.Margin = pairedMargin(completed)
	for _, class := range QueryClasses {
		if m := pairedMargin(classCompleted[class]); m != nil {
			rep.ClassMargins = append(rep.ClassMargins, ClassMargin{QueryClass: class, Margin: *m})
		}
	}
	return rep
}

func ratio(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}

// ReportPath is reports/bench/report.json under repoRoot.
func ReportPath(repoRoot string) string {
	return filepath.Join(repoRoot, "reports", "bench", "report.json")
}

// WriteReport writes the regenerated report snapshot.
func WriteReport(repoRoot string, r *Report) error {
	dir := filepath.Dir(ReportPath(repoRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s — check directory permissions: %w", dir, err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bench report: %w", err)
	}
	if err := os.WriteFile(ReportPath(repoRoot), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", ReportPath(repoRoot), err)
	}
	return nil
}

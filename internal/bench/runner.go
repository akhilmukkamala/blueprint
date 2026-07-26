// Run plan and the Runner seam. The A/B design holds everything constant —
// harness, model, repo commit — and toggles only the retrieval tier
// (RESEARCH §3.5 finding 8). Agent execution is stubbed: ManualRunner emits a
// human protocol and reads back human-entered results from reports/bench/.
package bench

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tier is a retrieval configuration under test.
type Tier string

const (
	// Tier0Grep is the optimized baseline: ripgrep + disciplined reads, no
	// index. A naive baseline would inflate the margin (RESEARCH §3.5).
	Tier0Grep Tier = "tier0-grep"
	// Tier1Map adds the tree-sitter repo map (blueprint map) to the same
	// harness; nothing else changes.
	Tier1Map Tier = "tier1-map"
)

// ValidTier reports whether s names a tier of the given grid.
func ValidTier(s string, tiers []Tier) bool {
	for _, t := range tiers {
		if Tier(s) == t {
			return true
		}
	}
	return false
}

// TierNames renders a grid for error messages.
func TierNames(tiers []Tier) string {
	names := make([]string, len(tiers))
	for i, t := range tiers {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}

// Run is one (task, tier) cell of the A/B grid.
type Run struct {
	TaskID string `json:"task_id"`
	Tier   Tier   `json:"tier"`
}

// Plan expands a suite into its full grid, task order × tier order, so runs
// and reports are deterministic. The tier list comes from ActiveTiers so
// opt-in tiers only create cells when they are actually configured.
func Plan(s *Suite, tiers []Tier) []Run {
	var runs []Run
	for _, t := range s.Tasks {
		for _, tier := range tiers {
			runs = append(runs, Run{TaskID: t.ID, Tier: tier})
		}
	}
	return runs
}

// Result is one recorded run. Success is the verified-task-success bit that
// must always travel with the token number (RESEARCH §3.5: never report
// tokens without the quality bar).
type Result struct {
	TaskID     string    `json:"task_id"`
	Tier       Tier      `json:"tier"`
	Tokens     int       `json:"tokens"`
	ToolCalls  int       `json:"tool_calls"`
	WallClockS float64   `json:"wall_clock_seconds"`
	Success    bool      `json:"success"`
	Notes      string    `json:"notes,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Runner executes one cell. Real agent-driving runners plug in here when
// harness automation ships; the skeleton ships ManualRunner only.
type Runner interface {
	RunTask(t Task, tier Tier) (Result, error)
}

// ErrNotRecorded marks a cell with no human-entered result yet.
var ErrNotRecorded = errors.New("no recorded result for this run")

// ResultsPath is the append-only human-entry log under reports/bench/.
func ResultsPath(repoRoot string) string {
	return filepath.Join(repoRoot, "reports", "bench", "results.ndjson")
}

// ManualRunner fulfills runs from the recorded-results log. RunTask never
// launches anything: a missing entry yields ErrNotRecorded and the caller
// shows Instructions for the human to execute the protocol.
type ManualRunner struct {
	RepoRoot string
}

func (m ManualRunner) RunTask(t Task, tier Tier) (Result, error) {
	results, _, err := ReadResults(m.RepoRoot)
	if err != nil {
		return Result{}, err
	}
	// Last entry wins: re-recording a run supersedes earlier entries without
	// rewriting the append-only log.
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].TaskID == t.ID && results[i].Tier == tier {
			return results[i], nil
		}
	}
	return Result{TaskID: t.ID, Tier: tier}, fmt.Errorf("%w: task %s under %s — follow the protocol below, then record it:\n%s", ErrNotRecorded, t.ID, tier, Instructions(t, tier))
}

// Record appends one human-entered result to results.ndjson (append-only,
// CONTRACTS rule 7).
func Record(repoRoot string, r Result) error {
	if r.TaskID == "" || r.Tier == "" {
		return fmt.Errorf("a result needs both task_id and tier — pass --record-task and --record-tier")
	}
	if r.RecordedAt.IsZero() {
		r.RecordedAt = time.Now().UTC()
	}
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	dir := filepath.Dir(ResultsPath(repoRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s — check directory permissions: %w", dir, err)
	}
	f, err := os.OpenFile(ResultsPath(repoRoot), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", ResultsPath(repoRoot), err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append result: %w", err)
	}
	return f.Close()
}

// ReadResults returns every parseable recorded result in file order plus one
// warning per corrupt line; a missing log is an empty history.
func ReadResults(repoRoot string) ([]Result, []string, error) {
	f, err := os.Open(ResultsPath(repoRoot))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var (
		out   []Result
		warns []string
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			warns = append(warns, fmt.Sprintf("%s:%d: skipping corrupt result line: %v", ResultsPath(repoRoot), line, err))
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, warns, fmt.Errorf("scan %s: %w", ResultsPath(repoRoot), err)
	}
	return out, warns, nil
}

// Instructions is the manual protocol for one cell. It encodes the fairness
// rules so a human run stays comparable to a future automated one.
func Instructions(t Task, tier Tier) string {
	tierSetup := "  Tier 0 (baseline): disable the repo map / MCP retrieval tools; the agent gets ripgrep + file reads only."
	switch tier {
	case Tier1Map:
		tierSetup = "  Tier 1 (map): enable the repo map (`blueprint map`, MCP tier 1); everything else identical to the Tier 0 run."
	case Tier2Packing:
		tierSetup = "  Tier 2 (packing): enable the Repomix packing MCP server configured under [retrieval.packing] in .blueprint/config.toml; everything else identical to the Tier 0 run."
	case Tier2LSP:
		tierSetup = "  Tier 2 (LSP): enable the Serena LSP-symbols MCP server configured under [retrieval.lsp] in .blueprint/config.toml; everything else identical to the Tier 0 run."
	case Tier3Graph:
		tierSetup = "  Tier 3 (graph): enable the graph retrieval backend configured under [retrieval.graph] in .blueprint/config.toml; everything else identical to the Tier 0 run."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "── bench run: task %s · %s ──\n", t.ID, tier)
	fmt.Fprintf(&b, "1. Checkout the pinned post-cutoff commit: git checkout %s\n", t.CommitRef)
	fmt.Fprintf(&b, "2. Configure retrieval:\n%s\n", tierSetup)
	fmt.Fprintf(&b, "3. Give the agent exactly this prompt (fresh session, same model/harness as every other run):\n   %q\n", t.Prompt)
	b.WriteString("4. Judge success: the answer must identify ")
	var wants []string
	if len(t.ExpectedFiles) > 0 {
		wants = append(wants, "file(s) "+strings.Join(t.ExpectedFiles, ", "))
	}
	if len(t.ExpectedSymbols) > 0 {
		wants = append(wants, "symbol(s) "+strings.Join(t.ExpectedSymbols, ", "))
	}
	b.WriteString(strings.Join(wants, " and "))
	b.WriteString(".\n5. Record tokens, tool calls, wall clock, and the success verdict together:\n")
	fmt.Fprintf(&b, "   blueprint bench retrieval --record-task %s --record-tier %s --tokens <n> --tool-calls <n> --seconds <s> --success=<true|false>\n", t.ID, tier)
	return b.String()
}

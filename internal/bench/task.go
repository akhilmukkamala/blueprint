// Package bench is the AC-8 retrieval-benchmark harness skeleton: a task
// suite format, a toggle-only-retrieval run plan (Tier 0 optimized grep vs
// Tier 1 repo map), and joint reporting per RESEARCH §3.5 — a token number is
// never emitted without the verified-success column beside it, and no vendor
// numbers ever appear. Executing agent runs is out of scope for the skeleton:
// the Runner seam is stubbed by ManualRunner, which emits a human protocol and
// folds human-entered results from reports/bench/.
package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Task is one benchmark item. CommitRef must point at a post-cutoff commit so
// the model cannot answer from memorized training data (SWE-Bench-Illusion
// guard, RESEARCH §3.5 finding 8).
type Task struct {
	ID              string   `toml:"id"`
	Prompt          string   `toml:"prompt"`
	ExpectedFiles   []string `toml:"expected_files"`
	ExpectedSymbols []string `toml:"expected_symbols,omitempty"`
	CommitRef       string   `toml:"commit_ref"`
	// QueryClass buckets the task for the per-class report breakdown
	// (RESEARCH §3.5: retrieval wins are condition-bound, so the report must
	// say WHERE a tier wins). Empty is allowed — Classify fills it at
	// report time.
	QueryClass string `toml:"query_class,omitempty"`
}

// Query classes (fixed vocabulary + report ordering).
const (
	ClassExactSymbol = "exact-symbol"
	ClassConceptual  = "conceptual"
	ClassCrossFile   = "cross-file"
	ClassRefactor    = "refactor"
)

// QueryClasses is the closed set, in deterministic report order.
var QueryClasses = []string{ClassExactSymbol, ClassConceptual, ClassCrossFile, ClassRefactor}

func validQueryClass(s string) bool {
	for _, c := range QueryClasses {
		if s == c {
			return true
		}
	}
	return false
}

// Suite is the TOML task file: a name plus [[tasks]] entries.
type Suite struct {
	Name  string `toml:"name"`
	Tasks []Task `toml:"tasks"`
}

// DefaultSuitePath is .blueprint/bench/retrieval.toml under repoRoot.
func DefaultSuitePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "bench", "retrieval.toml")
}

// LoadSuite parses and validates a task suite; every violation is reported as
// a remediation instruction.
func LoadSuite(path string) (*Suite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no task suite at %s — create one with `blueprint bench retrieval --init`, then edit the tasks to point at your repo's post-cutoff commits", path)
		}
		return nil, fmt.Errorf("read task suite %s: %w", path, err)
	}
	var s Suite
	if err := toml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("task suite %s is not valid TOML: %v — see the sample from `blueprint bench retrieval --init` for the expected shape", path, err)
	}
	if len(s.Tasks) == 0 {
		return nil, fmt.Errorf("task suite %s has no [[tasks]] entries — add at least one task with id, prompt, commit_ref, and expected_files or expected_symbols", path)
	}
	seen := map[string]bool{}
	for i, t := range s.Tasks {
		at := fmt.Sprintf("task suite %s, [[tasks]] entry %d", path, i+1)
		switch {
		case strings.TrimSpace(t.ID) == "":
			return nil, fmt.Errorf("%s: missing id — give every task a stable id like \"T1\"", at)
		case seen[t.ID]:
			return nil, fmt.Errorf("%s: duplicate id %q — task ids must be unique so recorded results attach to the right task", at, t.ID)
		case strings.TrimSpace(t.Prompt) == "":
			return nil, fmt.Errorf("%s (%s): missing prompt — write the retrieval question the agent must answer", at, t.ID)
		case len(t.ExpectedFiles) == 0 && len(t.ExpectedSymbols) == 0:
			return nil, fmt.Errorf("%s (%s): no expected_files or expected_symbols — success must be checkable, list what a correct answer touches", at, t.ID)
		case strings.TrimSpace(t.CommitRef) == "":
			return nil, fmt.Errorf("%s (%s): missing commit_ref — pin a post-cutoff commit so results measure retrieval, not training-data recall", at, t.ID)
		case t.QueryClass != "" && !validQueryClass(t.QueryClass):
			return nil, fmt.Errorf("%s (%s): unknown query_class %q — use one of %s, or leave it empty to auto-classify", at, t.ID, t.QueryClass, strings.Join(QueryClasses, ", "))
		}
		seen[t.ID] = true
	}
	return &s, nil
}

// sampleSuite is the --init scaffold: valid shape, obviously-placeholder
// content.
const sampleSuite = `# Retrieval benchmark task suite (AC-8).
# Every task pins a post-cutoff commit_ref: results must measure retrieval,
# not the model's memory of public training data.
name = "retrieval-suite"

[[tasks]]
id = "T1"
prompt = "Where is the ceremony tier decided from path sensitivity and blast radius?"
expected_files = ["internal/route/router.go"]
expected_symbols = ["Decide"]
commit_ref = "REPLACE-with-a-post-cutoff-commit-sha"
query_class = "exact-symbol" # exact-symbol | conceptual | cross-file | refactor (empty = auto)

[[tasks]]
id = "T2"
prompt = "Which function enforces the five no-progress breaker patterns?"
expected_files = ["internal/loop/breaker.go"]
expected_symbols = ["EvaluateBreaker"]
commit_ref = "REPLACE-with-a-post-cutoff-commit-sha"
`

// WriteSampleSuite scaffolds the sample at path; it refuses to overwrite.
func WriteSampleSuite(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("task suite %s already exists — edit it directly, or delete it before --init", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create %s — check directory permissions: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(sampleSuite), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

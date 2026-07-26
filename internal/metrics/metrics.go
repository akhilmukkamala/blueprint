// Package metrics folds the repo's existing instruments — the worklog, every
// change journal (active and archived), and git history — into
// reports/metrics.json, one row per DESIGN §15 metric. Metrics are a fold,
// not a pipeline: no collection daemon, no egress, no state of their own.
// Invariant: every §15 row is always present in the report; a row that cannot
// be measured yet carries value null plus a Reason naming the missing
// instrumentation — metrics are never silently omitted.
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Value is one metric row. Value == nil means "not measurable yet" and Reason
// is then mandatory (the honesty contract of DESIGN §15).
type Value struct {
	Value    any            `json:"value"`
	Unit     string         `json:"unit,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Baseline any            `json:"baseline,omitempty"` // same-repo denominator from .blueprint/baselines.json
	Detail   map[string]any `json:"detail,omitempty"`
}

// Report is the full metrics.json payload.
type Report struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Metrics     map[string]Value `json:"metrics"`
}

// Keys lists every §15 row in table order; the report always contains exactly
// this set, and human/prom output iterates it for deterministic ordering.
var Keys = []string{
	"time_to_first_verified_change",
	"time_to_install",
	"upgrade_success",
	"time_to_onboard",
	"context_retrieval_efficiency",
	"index_freshness",
	"rework_rate",
	"ceremony_fit",
	"cost_per_verified_change",
	"escaped_defect_rate",
	"self_improvement_velocity",
	"knowledge_health",
	"portability",
	"supervision_ratio",
}

// GitRunner executes git in repoRoot and returns stdout. Injected in tests so
// the fold stays a pure function of its inputs (CONTRACTS rule 5).
type GitRunner func(repoRoot string, args ...string) (string, error)

func execGit(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Options carries the two injectable effects: the clock (stamped into the
// report only) and git execution.
type Options struct {
	Now func() time.Time
	Git GitRunner
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now().UTC()
}

func (o Options) git() GitRunner {
	if o.Git != nil {
		return o.Git
	}
	return execGit
}

// Compute folds all sources into a Report. It never fails because a source is
// merely absent — absence becomes a null row with a reason.
func Compute(repoRoot string, opts Options) (*Report, error) {
	src, err := collect(repoRoot)
	if err != nil {
		return nil, err
	}
	r := &Report{GeneratedAt: opts.now().UTC(), Metrics: map[string]Value{}}

	r.Metrics["time_to_first_verified_change"] = timeToFirstVerified(src)
	r.Metrics["time_to_install"] = timeToInstall(src)
	r.Metrics["upgrade_success"] = Value{Reason: "measured in CI: upgrade events plus the [user]-tier hash audit over the AC-3 corpus run in the install matrix, not in-repo"}
	r.Metrics["time_to_onboard"] = timeToOnboard(src)
	r.Metrics["context_retrieval_efficiency"] = retrievalEfficiency(repoRoot)
	r.Metrics["index_freshness"] = indexFreshness(repoRoot, opts)
	r.Metrics["rework_rate"] = reworkRate(repoRoot, src, opts)
	r.Metrics["ceremony_fit"] = ceremonyFit(src)
	r.Metrics["cost_per_verified_change"] = costPerVerified(src)
	r.Metrics["escaped_defect_rate"] = escapedDefects(src)
	r.Metrics["self_improvement_velocity"] = Value{Reason: "dream cycles not yet recorded — failure-class recurrence tracking ships with the dreaming feature (`blueprint dream`)"}
	r.Metrics["knowledge_health"] = knowledgeHealth(repoRoot, opts)
	r.Metrics["portability"] = Value{Reason: "measured in CI: adapter drift-check and switch-tool round-trip test (AC-4) run in the install matrix, not in-repo"}
	r.Metrics["supervision_ratio"] = supervisionRatio(src)

	if err := mergeBaselines(repoRoot, r); err != nil {
		return nil, err
	}
	return r, nil
}

// ReportPath is reports/metrics.json under repoRoot.
func ReportPath(repoRoot string) string {
	return filepath.Join(repoRoot, "reports", "metrics.json")
}

// WriteReport writes metrics.json (creating reports/); the report is a
// regenerated snapshot, not a journal, so overwrite is correct here.
func WriteReport(repoRoot string, r *Report) error {
	dir := filepath.Dir(ReportPath(repoRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s — check directory permissions: %w", dir, err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode metrics report: %w", err)
	}
	if err := os.WriteFile(ReportPath(repoRoot), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", ReportPath(repoRoot), err)
	}
	return nil
}

// mergeBaselines attaches .blueprint/baselines.json values (adoption stage-0
// snapshot) as same-repo denominators. Keys must match Keys; unknown keys are
// ignored so other features can extend the file.
func mergeBaselines(repoRoot string, r *Report) error {
	path := filepath.Join(repoRoot, ".blueprint", "baselines.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var base map[string]any
	if err := json.Unmarshal(raw, &base); err != nil {
		return fmt.Errorf("%s is not valid JSON: %v — it must be an object of {\"<metric_key>\": <baseline value>}; fix or delete it and re-run", path, err)
	}
	for k, v := range base {
		m, ok := r.Metrics[k]
		if !ok {
			continue
		}
		m.Baseline = v
		r.Metrics[k] = m
	}
	return nil
}

package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"blueprint/internal/core"
)

const timeLayout = time.RFC3339

// Options tunes a verify/approve run. Zero value is production behavior.
type Options struct {
	Hooks *Hooks           // nil -> DefaultHooks (wired in integrated builds)
	Now   func() time.Time // nil -> time.Now; injected for deterministic tests
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Run is `blueprint verify <id>` (DESIGN §7): stages cheapest-first —
// (1) spec lint + trace lint, (2) TAMPER, (3) declared checks + scope-matched
// domain verifiers, (4) full-tier model checker. Every stage is a
// core.CheckResult; the Verdict is written to verdict/verdict.json and
// journaled. Deterministic apart from the explicit timestamp (CONTRACTS 5).
func Run(repoRoot, changeID string, opts Options) (*core.Verdict, error) {
	hk := opts.hooks()
	if hk.LoadChange == nil {
		return nil, fmt.Errorf("internal/spec is not wired into this build; enable the integrated wiring (internal/verify/wiring_integrated.go, build tag blueprint_integrated) or inject Hooks.LoadChange")
	}
	c, err := hk.LoadChange(repoRoot, changeID)
	if err != nil {
		return nil, err
	}

	v := &core.Verdict{ChangeID: changeID, Time: opts.now().UTC()}

	// Stage 1: deterministic linters — cheapest, so they run first.
	v.Checks = append(v.Checks, lintStage("spec-lint", hk.LintSpec, repoRoot, changeID))
	v.Checks = append(v.Checks, lintStage("trace-lint", hk.LintTrace, repoRoot, changeID))

	// Stage 2: tamper evidence. A tampered run stops here: later stages
	// (including the cost-capped checker) would attest inputs that no human
	// approved.
	tamper := checkTamper(repoRoot, c)
	v.Checks = append(v.Checks, tamper)
	v.Tamper = !tamper.Pass

	// Sev-1 time-shifted ceremony gate (DESIGN §4, ADR-0009): lapsed
	// backfills block overlapping changes (repo-wide past the grace window).
	v.Checks = append(v.Checks, backfillGuard(repoRoot, changeID, opts.now()))

	if !v.Tamper {
		// Stage 3: declared checks (requirement verify: blocks of kind
		// "check"), then scope-matched domain verifiers, deduped by name.
		verifiers, err := loadVerifiers(repoRoot)
		if err != nil {
			return nil, err
		}
		byName := map[string]Verifier{}
		for _, dv := range verifiers {
			byName[dv.Name] = dv
		}
		ran := map[string]bool{}
		for _, d := range c.Delta {
			for _, vm := range d.Requirement.Verify {
				if vm.Kind != "check" || ran[vm.Ref] {
					continue
				}
				ran[vm.Ref] = true
				dv, ok := byName[vm.Ref]
				if !ok {
					v.Checks = append(v.Checks, core.CheckResult{
						Name:     vm.Ref,
						ReqID:    d.Requirement.ID,
						ExitCode: -1,
						Detail:   fmt.Sprintf("requirement %s declares check:%s but no [[verifier]] named %q exists in .blueprint/verifiers.toml; add one (name, command, config_path, report_format, applies_to)", d.Requirement.ID, vm.Ref, vm.Ref),
					})
					continue
				}
				v.Checks = append(v.Checks, runVerifier(repoRoot, changeID, dv, d.Requirement.ID))
			}
		}
		for _, dv := range verifiers {
			if ran[dv.Name] || !dv.appliesTo(c.Scenario) {
				continue
			}
			ran[dv.Name] = true
			v.Checks = append(v.Checks, runVerifier(repoRoot, changeID, dv, ""))
		}

		// Stage 4: fresh-context model checker — full tier only; most
		// expensive, so it runs last and only when everything else is green.
		switch {
		case c.Tier != core.TierFull:
			v.Checks = append(v.Checks, core.CheckResult{
				Name:   "model-checker",
				Pass:   true,
				Detail: fmt.Sprintf("skipped: tier is %q; the model checker runs on full-tier changes only (deterministic checks are the gate here)", c.Tier),
			})
		case anyFailed(v.Checks):
			v.Checks = append(v.Checks, core.CheckResult{
				Name:   "model-checker",
				Detail: "skipped: earlier deterministic stages failed; fix those first — the cost-capped checker only runs on an otherwise-green change",
			})
		default:
			v.Checks = append(v.Checks, runChecker(repoRoot, c))
		}
	}

	v.Pass = !v.Tamper && !anyFailed(v.Checks)
	v.Fingerprint = fingerprint(v.Checks)

	if err := writeVerdict(repoRoot, changeID, v); err != nil {
		return nil, err
	}

	// A green verdict on an approved change flips it to verified — the
	// lifecycle gate `blueprint close` requires (DESIGN §3). Safe against the
	// tamper hash because change.md's status line is masked in the lock.
	if v.Pass && c.Status == core.StatusApproved && hk.SaveChange != nil {
		c.Status = core.StatusVerified
		if err := hk.SaveChange(repoRoot, c); err != nil {
			return nil, fmt.Errorf("verdict recorded but status update to verified failed: %w", err)
		}
		if err := appendJournal(repoRoot, changeID, core.JournalEvent{
			Time: v.Time, Kind: "status", ChangeID: changeID,
			Data: map[string]any{"from": string(core.StatusApproved), "to": string(core.StatusVerified)},
		}); err != nil {
			return nil, err
		}
	}
	if v.Tamper {
		if err := appendJournal(repoRoot, changeID, core.JournalEvent{
			Time: v.Time, Kind: "tamper", ChangeID: changeID,
			Data: map[string]any{"detail": tamper.Detail},
		}); err != nil {
			return nil, err
		}
	}
	if err := appendJournal(repoRoot, changeID, core.JournalEvent{
		Time: v.Time, Kind: "verdict", ChangeID: changeID,
		Data: map[string]any{
			"pass":        v.Pass,
			"tamper":      v.Tamper,
			"fingerprint": v.Fingerprint,
			"checks":      len(v.Checks),
		},
	}); err != nil {
		return nil, err
	}
	return v, nil
}

// lintStage adapts a lint hook into a CheckResult. An unwired hook is
// recorded as skipped — visibly, never silently — so pre-integration builds
// stay honest about what ran.
func lintStage(name string, fn func(string, string) ([]core.LintFinding, error), repoRoot, changeID string) core.CheckResult {
	res := core.CheckResult{Name: name}
	if fn == nil {
		res.Pass = true
		res.Detail = "skipped: internal/lint is not wired into this build"
		return res
	}
	findings, err := fn(repoRoot, changeID)
	if err != nil {
		res.ExitCode = -1
		res.Detail = err.Error()
		return res
	}
	var errs []string
	for _, f := range findings {
		if f.Severity == "error" {
			errs = append(errs, fmt.Sprintf("%s:%d %s: %s (%s)", f.File, f.Line, f.Rule, f.Message, f.Remediation))
		}
	}
	if len(errs) > 0 {
		res.ExitCode = 1
		res.Detail = strings.Join(errs, "\n")
		return res
	}
	res.Pass = true
	return res
}

func anyFailed(checks []core.CheckResult) bool {
	for _, c := range checks {
		if !c.Pass {
			return true
		}
	}
	return false
}

// fingerprint hashes the failure set (sorted "name|reqid" lines) so the loop
// breaker can detect identical consecutive failures (core.Verdict contract).
func fingerprint(checks []core.CheckResult) string {
	var failing []string
	for _, c := range checks {
		if !c.Pass {
			failing = append(failing, c.Name+"|"+c.ReqID)
		}
	}
	sort.Strings(failing)
	h := sha256.Sum256([]byte(strings.Join(failing, "\n")))
	return hex.EncodeToString(h[:])
}

func writeVerdict(repoRoot, changeID string, v *core.Verdict) error {
	if err := os.MkdirAll(verdictDir(repoRoot, changeID), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(verdictDir(repoRoot, changeID), "verdict.json"), append(b, '\n'), 0o644)
}

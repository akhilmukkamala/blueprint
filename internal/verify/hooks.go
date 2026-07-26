// Package verify implements DESIGN §7: the verifier orchestrator (blueprint
// verify) and the tamper-evidence stack (blueprint approve / approved.lock).
// Stages run cheapest-first; every stage is a core.CheckResult; the aggregate
// is a core.Verdict with a failure-set fingerprint.
package verify

import "blueprint/internal/core"

// Hooks are the seams to sibling feature packages (internal/spec,
// internal/lint). They exist because those packages are built in parallel:
// this package codes against the contract, and the integrated build wires the
// real implementations (see wiring_integrated.go, build-tagged
// `blueprint_integrated`). Tests wire tiny local stubs — never a fake
// internal/spec package.
type Hooks struct {
	// LoadChange mirrors spec.LoadChange(repoRoot, id).
	LoadChange func(repoRoot, id string) (*core.Change, error)
	// SaveChange mirrors spec.SaveChange; used by Approve to flip
	// draft -> approved. Optional: nil skips the status flip.
	SaveChange func(repoRoot string, c *core.Change) error
	// LintSpec runs the deterministic spec linter for the change
	// (blueprint lint spec). Nil = not wired; verify records the stage
	// as skipped rather than inventing a result.
	LintSpec func(repoRoot, changeID string) ([]core.LintFinding, error)
	// LintTrace runs the bidirectional trace check (blueprint lint trace).
	LintTrace func(repoRoot, changeID string) ([]core.LintFinding, error)
}

// DefaultHooks is populated by the integrated-build wiring file. When it is
// empty (parallel-development builds), commands fail with remediation text
// and tests inject their own stubs via Options.Hooks.
var DefaultHooks = &Hooks{}

func (o Options) hooks() *Hooks {
	if o.Hooks != nil {
		return o.Hooks
	}
	return DefaultHooks
}

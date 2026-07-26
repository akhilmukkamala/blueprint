package verify

// Integrated-build wiring: internal/spec (and internal/lint) are developed in
// parallel branches, so this worktree compiles without them. Once merged, the
// integrator drops the build tag above (delete the //go:build line) and the
// hooks bind to the real packages per the spec-package API contract.

import (
	"blueprint/internal/core"
	"blueprint/internal/lint"
	"blueprint/internal/spec"
)

func init() {
	DefaultHooks.LoadChange = spec.LoadChange
	DefaultHooks.SaveChange = spec.SaveChange

	DefaultHooks.LintSpec = func(repoRoot, changeID string) ([]core.LintFinding, error) {
		return lint.Spec(repoRoot, lint.Config{})
	}
	DefaultHooks.LintTrace = func(repoRoot, changeID string) ([]core.LintFinding, error) {
		return lint.Trace(repoRoot, lint.Config{})
	}
}

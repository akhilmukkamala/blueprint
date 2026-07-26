// Package autonomy owns .blueprint/autonomy.json — the per-scenario-class
// autonomy-ladder state (DESIGN §8) — and the rate-based promotion/demotion
// function. Invariants: levels are only ever RAISED by a human applying the
// emitted proposal diff (never by this package); demotions ARE applied
// automatically and journaled; L3 is refused mechanically whenever the forge
// profile is advisory (Enforced=false).
package autonomy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"blueprint/internal/core"
)

// Path returns the ladder-state file location under repoRoot.
func Path(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "autonomy.json")
}

// ProposalPath is where Recompute writes the promotion-proposal unified diff.
func ProposalPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "autonomy-proposal.diff")
}

// RunRecord is one verified run of a change, as folded from its journal.
// Clean means the FIRST verdict was green (green-on-first-review, DESIGN §8);
// Rework means an escaped-defect signal (incident event or revert commit
// touching the archived change) appeared within the rework window.
type RunRecord struct {
	ChangeID string    `json:"change_id"`
	Time     time.Time `json:"time"`
	Clean    bool      `json:"clean"`
	Tamper   bool      `json:"tamper"`
	Rework   bool      `json:"rework"`
}

// ClassState is the ladder state for one scenario class.
type ClassState struct {
	Level  core.AutonomyLevel `json:"level"`
	Window []RunRecord        `json:"window"`
}

// File is the autonomy.json shape: forge profile + per-class ladder state.
type File struct {
	Profile core.EnforcementProfile `json:"profile"`
	Classes map[string]ClassState   `json:"classes"`
}

// Load reads autonomy.json. A missing file is the starting state: unknown
// advisory forge, every class implicitly L1.
func Load(repoRoot string) (*File, error) {
	raw, err := os.ReadFile(Path(repoRoot))
	if os.IsNotExist(err) {
		return &File{
			Profile: core.EnforcementProfile{Forge: "unknown", Enforced: false,
				Notes: "no forge probe recorded — run `blueprint doctor` to detect the forge"},
			Classes: map[string]ClassState{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("autonomy: cannot read %s: %w", Path(repoRoot), err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("autonomy: %s is corrupt (%v) — restore it from git history; it is tool-owned and changed only via proposal PRs and journaled demotions", Path(repoRoot), err)
	}
	if f.Classes == nil {
		f.Classes = map[string]ClassState{}
	}
	return &f, nil
}

// Marshal renders the canonical on-disk serialization (stable key order via
// encoding/json map sorting, two-space indent) so proposal diffs are minimal.
func Marshal(f *File) ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("autonomy: cannot encode state: %w", err)
	}
	return append(b, '\n'), nil
}

// Save writes autonomy.json. Callers are Recompute (window refresh, demotions)
// and doctor (profile update) — never a promotion.
func Save(repoRoot string, f *File) error {
	b, err := Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path(repoRoot)), 0o755); err != nil {
		return fmt.Errorf("autonomy: cannot create .blueprint: %w", err)
	}
	if err := os.WriteFile(Path(repoRoot), b, 0o644); err != nil {
		return fmt.Errorf("autonomy: cannot write %s: %w", Path(repoRoot), err)
	}
	return nil
}

// SetProfile records the doctor's forge probe, preserving class state.
func SetProfile(repoRoot string, p core.EnforcementProfile) error {
	f, err := Load(repoRoot)
	if err != nil {
		return err
	}
	f.Profile = p
	return Save(repoRoot, f)
}

// Level returns the effective ladder level for a scenario class (L1 when the
// class has no recorded state — every class starts at L1, ADR-0007).
func (f *File) Level(scenario string) core.AutonomyLevel {
	if c, ok := f.Classes[scenario]; ok && c.Level >= core.L1Propose {
		return c.Level
	}
	return core.L1Propose
}

// Scenarios returns the recorded class names sorted for deterministic output.
func (f *File) Scenarios() []string {
	out := make([]string, 0, len(f.Classes))
	for s := range f.Classes {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

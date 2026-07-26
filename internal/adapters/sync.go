// Sync engine: apply, check, revert. Table stakes per DESIGN §10 — idempotent
// apply, .bak before the first overwrite of a file we did not generate,
// --check as the CI drift gate, --revert to restore the pre-sync originals.
package adapters

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Result reports what one sync/revert pass did, all paths repo-relative.
type Result struct {
	Written   []string `json:"written,omitempty"`
	BackedUp  []string `json:"backed_up,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"`
	Restored  []string `json:"restored,omitempty"`
	Removed   []string `json:"removed,omitempty"`
}

func abs(repoRoot, rel string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(rel))
}

// Sync writes the plan into the working tree. Unchanged files are untouched
// (idempotence); a changed file that exists but does not carry our provenance
// marker is copied to <path>.bak first — once, never clobbering an earlier
// backup, so .bak always holds the pre-blueprint original.
func Sync(repoRoot string, plan *Plan) (*Result, error) {
	res := &Result{}
	for _, f := range plan.Files {
		target := abs(repoRoot, f.Path)
		existing, err := os.ReadFile(target)
		exists := err == nil
		if err != nil && !os.IsNotExist(err) {
			return res, fmt.Errorf("adapters: cannot read %s: %w", f.Path, err)
		}
		if exists && bytes.Equal(existing, f.Content) {
			res.Unchanged = append(res.Unchanged, f.Path)
			continue
		}
		if exists && !IsGenerated(existing) {
			bak := target + ".bak"
			if _, err := os.Stat(bak); os.IsNotExist(err) {
				if err := os.WriteFile(bak, existing, 0o644); err != nil {
					return res, fmt.Errorf("adapters: cannot back up %s to %s.bak: %w", f.Path, f.Path, err)
				}
				res.BackedUp = append(res.BackedUp, f.Path+".bak")
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, fmt.Errorf("adapters: cannot create directory for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(target, f.Content, 0o644); err != nil {
			return res, fmt.Errorf("adapters: cannot write %s: %w", f.Path, err)
		}
		res.Written = append(res.Written, f.Path)
	}
	return res, nil
}

// Drift is one --check divergence between plan and working tree.
type Drift struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // missing | modified
}

// Check regenerates the plan into tmpDir and diffs it against the working
// tree. Any divergence is drift: CI fails, the fix is `blueprint sync` (or
// editing the canonical source, never the generated file).
func Check(repoRoot string, plan *Plan, tmpDir string) ([]Drift, error) {
	var drifts []Drift
	for _, f := range plan.Files {
		// Materialize into the temp dir so --check leaves an inspectable
		// regeneration behind and exercises the same write path as sync.
		tmp := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
			return nil, fmt.Errorf("adapters: cannot prepare check dir: %w", err)
		}
		if err := os.WriteFile(tmp, f.Content, 0o644); err != nil {
			return nil, fmt.Errorf("adapters: cannot write check copy of %s: %w", f.Path, err)
		}
		actual, err := os.ReadFile(abs(repoRoot, f.Path))
		if os.IsNotExist(err) {
			drifts = append(drifts, Drift{Path: f.Path, Reason: "missing"})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("adapters: cannot read %s: %w", f.Path, err)
		}
		if !bytes.Equal(actual, f.Content) {
			drifts = append(drifts, Drift{Path: f.Path, Reason: "modified"})
		}
	}
	return drifts, nil
}

// Revert restores the pre-sync originals: every planned path with a .bak gets
// the backup moved back over it; a planned file without a .bak that carries
// our provenance marker did not exist before sync, so it is removed. User
// files without backups are never touched.
func Revert(repoRoot string, plan *Plan) (*Result, error) {
	res := &Result{}
	for _, f := range plan.Files {
		target := abs(repoRoot, f.Path)
		bak := target + ".bak"
		if _, err := os.Stat(bak); err == nil {
			if err := os.Rename(bak, target); err != nil {
				return res, fmt.Errorf("adapters: cannot restore %s from %s.bak: %w", f.Path, f.Path, err)
			}
			res.Restored = append(res.Restored, f.Path)
			continue
		}
		existing, err := os.ReadFile(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return res, fmt.Errorf("adapters: cannot read %s: %w", f.Path, err)
		}
		if IsGenerated(existing) {
			if err := os.Remove(target); err != nil {
				return res, fmt.Errorf("adapters: cannot remove generated %s: %w", f.Path, err)
			}
			res.Removed = append(res.Removed, f.Path)
			removeEmptyParents(repoRoot, filepath.Dir(target))
		}
	}
	return res, nil
}

// removeEmptyParents prunes now-empty generated directories (.claude/commands
// etc.) up to but never including the repo root.
func removeEmptyParents(repoRoot, dir string) {
	root := filepath.Clean(repoRoot)
	for {
		dir = filepath.Clean(dir)
		if dir == root || len(dir) <= len(root) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

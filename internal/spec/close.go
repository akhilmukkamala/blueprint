package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"blueprint/internal/core"
)

// Close merges a verified change's delta into the living specs by REQ ID
// (mechanical, DESIGN §3 lifecycle), harvests its Design section into
// specs/<area>/design.md, and moves the change folder verbatim to
// .blueprint/archive/<id>/. The whole merge is validated before any file is
// written, so a failing Close leaves the repo untouched.
func Close(repoRoot, id string) error {
	c, err := LoadChange(repoRoot, id)
	if err != nil {
		return err
	}
	if c.Status != core.StatusVerified {
		return fmt.Errorf("change %q has status %q — only verified changes close; run `blueprint verify %s` until green first", id, c.Status, id)
	}

	// Phase 1: compute every merged spec in memory; fail before writing.
	specs := map[string]*fullSpec{} // area -> merged spec
	load := func(area string) (*fullSpec, error) {
		if s, ok := specs[area]; ok {
			return s, nil
		}
		s, err := loadFullSpec(repoRoot, area)
		if err != nil {
			if _, statErr := os.Stat(specPath(repoRoot, area)); os.IsNotExist(statErr) {
				// First change touching this area creates its spec.
				s = &fullSpec{Area: area, Status: string(core.StatusApproved)}
			} else {
				return nil, err
			}
		}
		specs[area] = s
		return s, nil
	}

	for _, d := range c.Delta {
		if d.Area == "" {
			return fmt.Errorf("change %q delta entry %q has a malformed REQ ID — use REQ-<area>-NNN", id, d.Requirement.ID)
		}
		s, err := load(d.Area)
		if err != nil {
			return err
		}
		idx := -1
		for i, r := range s.Requirements {
			if r.ID == d.Requirement.ID {
				idx = i
				break
			}
		}
		switch d.Op {
		case core.DeltaAdded:
			if idx >= 0 {
				return fmt.Errorf("cannot close %q: ADDED %s already exists in specs/%s/spec.md and REQ IDs are never reused — change the op to MODIFIED or pick a fresh ID", id, d.Requirement.ID, d.Area)
			}
			s.Requirements = append(s.Requirements, d.Requirement)
		case core.DeltaModified:
			if idx < 0 {
				return fmt.Errorf("cannot close %q: MODIFIED %s does not exist in specs/%s/spec.md — change the op to ADDED or fix the ID", id, d.Requirement.ID, d.Area)
			}
			s.Requirements[idx] = d.Requirement
		case core.DeltaRemoved:
			if idx < 0 {
				return fmt.Errorf("cannot close %q: REMOVED %s does not exist in specs/%s/spec.md — it may already be removed; drop the delta entry", id, d.Requirement.ID, d.Area)
			}
			s.Requirements = append(s.Requirements[:idx], s.Requirements[idx+1:]...)
		default:
			return fmt.Errorf("cannot close %q: unknown delta op %q for %s — use ADDED, MODIFIED, or REMOVED", id, d.Op, d.Requirement.ID)
		}
	}

	archiveDir := filepath.Join(repoRoot, ".blueprint", "archive")
	dest := filepath.Join(archiveDir, id)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("cannot close %q: archive folder %s already exists — a change ID is closed at most once", id, dest)
	}

	// Phase 2: write. Specs first (sorted for deterministic write order).
	areas := make([]string, 0, len(specs))
	for a := range specs {
		areas = append(areas, a)
	}
	sort.Strings(areas)
	for _, a := range areas {
		if err := saveFullSpec(repoRoot, specs[a]); err != nil {
			return err
		}
	}

	// Harvest the Design section into every touched area's design.md so
	// design knowledge survives archival (ADR-0005 call 8).
	if c.Design != "" {
		for _, a := range areas {
			if err := appendDesign(repoRoot, a, c); err != nil {
				return err
			}
		}
	}

	// Stamp the change closed before the folder moves.
	c.Status = core.StatusClosed
	if err := SaveChange(repoRoot, c); err != nil {
		return err
	}

	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return fmt.Errorf("cannot create archive directory: %v", err)
	}
	if err := os.Rename(filepath.Dir(ChangePath(repoRoot, id)), dest); err != nil {
		return fmt.Errorf("cannot archive change %q: %v — move the folder to %s manually and re-run", id, err, dest)
	}

	return appendWorklog(repoRoot, core.JournalEvent{
		Time:     time.Now().UTC(),
		Kind:     "close",
		ChangeID: id,
		Data:     map[string]any{"areas": areas, "delta_count": len(c.Delta)},
	})
}

// appendDesign appends the change's Design section to specs/<area>/design.md
// under a per-change heading, creating the file with a provenance line on
// first write.
func appendDesign(repoRoot, area string, c *core.Change) error {
	path := filepath.Join(specsDir(repoRoot), area, "design.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create spec directory for area %q: %v", area, err)
	}
	var b strings.Builder
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(&b, "# %s — design\n\nHarvested from closed changes by `blueprint close`; edit freely, harvests only append.\n", area)
	}
	fmt.Fprintf(&b, "\n## %s — %s\n\n%s\n", c.ID, c.Title, c.Design)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return fmt.Errorf("cannot append design to %s: %v", path, err)
	}
	return f.Close()
}

// appendWorklog appends one event line to .blueprint/log/worklog.ndjson
// (append-only, CONTRACTS rule 7).
func appendWorklog(repoRoot string, ev core.JournalEvent) error {
	dir := filepath.Join(repoRoot, ".blueprint", "log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create log directory: %v", err)
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("cannot encode worklog event: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "worklog.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open worklog: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("cannot append to worklog: %v", err)
	}
	return f.Close()
}

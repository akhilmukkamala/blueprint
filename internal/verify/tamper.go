package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"blueprint/internal/core"
)

// Lock is approved.lock (DESIGN §7): the human-approved fingerprint of
// everything a maker agent must not silently weaken — change.md, referenced
// living specs, spec-linked test files, verifier config, and loop caps —
// plus the executed-test-count floor.
type Lock struct {
	ChangeID        string            `json:"change_id"`
	ApprovedAt      string            `json:"approved_at"` // RFC3339; explicit timestamp, journaled
	Files           map[string]string `json:"files"`       // slash rel path -> sha256 ("" = absent at approve time)
	CapsHash        string            `json:"caps_hash"`   // sha256 of canonical LoopContract JSON
	SpecLinkedTests int               `json:"spec_linked_tests"`
	TestFiles       []string          `json:"test_files"`
	Composite       string            `json:"composite"`
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // recorded as absent; appearing later is drift
		}
		return "", err
	}
	return sha256hex(b), nil
}

var statusLineRe = regexp.MustCompile(`(?m)^(\s*)status\s*=\s*"(draft|approved|verified|closed|backfill-due)"`)

// hashChangeFile hashes change.md with the framework-owned status line
// normalized, so journaled status transitions never read as tamper.
func hashChangeFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	b = statusLineRe.ReplaceAll(b, []byte(`${1}status = "*"`))
	return sha256hex(b), nil
}

// computeLock builds the lock content for a change from current repo state.
func computeLock(repoRoot string, c *core.Change) (*Lock, error) {
	lk := &Lock{ChangeID: c.ID, Files: map[string]string{}}

	// change.md itself — with the status frontmatter line masked before
	// hashing: status transitions (draft->approved->verified) are performed by
	// the framework and journaled, so hashing the literal line would make
	// approve/verify invalidate their own lock. Everything else in the file
	// stays byte-protected.
	rel := filepath.ToSlash(filepath.Join(".blueprint", "changes", c.ID, "change.md"))
	h, err := hashChangeFile(changePath(repoRoot, c.ID))
	if err != nil {
		return nil, err
	}
	lk.Files[rel] = h

	// Referenced living-spec areas (delta targets), deduped.
	areas := map[string]bool{}
	reqIDs := map[string]bool{}
	for _, d := range c.Delta {
		areas[d.Area] = true
		reqIDs[d.Requirement.ID] = true
	}
	for area := range areas {
		p := specPath(repoRoot, area)
		h, err := hashFile(p)
		if err != nil {
			return nil, err
		}
		lk.Files[filepath.ToSlash(filepath.Join(".blueprint", "specs", area, "spec.md"))] = h
	}

	// Verifier config.
	h, err = hashFile(verifiersPath(repoRoot))
	if err != nil {
		return nil, err
	}
	lk.Files[".blueprint/verifiers.toml"] = h

	// Every test file matched by trace annotations for this change's REQ IDs.
	tests, err := scanTracedTests(repoRoot, reqIDs)
	if err != nil {
		return nil, err
	}
	for _, t := range tests {
		h, err := hashFile(filepath.Join(repoRoot, filepath.FromSlash(t.RelPath)))
		if err != nil {
			return nil, err
		}
		lk.Files[t.RelPath] = h
		lk.TestFiles = append(lk.TestFiles, t.RelPath)
		lk.SpecLinkedTests += t.Annotations
	}

	// Loop caps: canonical JSON of the contract (deterministic field order).
	caps, err := json.Marshal(c.Contract)
	if err != nil {
		return nil, err
	}
	lk.CapsHash = sha256hex(caps)

	lk.Composite = compositeHash(lk)
	return lk, nil
}

// compositeHash folds the lock's content into one digest: sorted path:hash
// lines + caps hash + test count. Deterministic by construction.
func compositeHash(lk *Lock) string {
	lines := make([]string, 0, len(lk.Files)+2)
	for p, h := range lk.Files {
		lines = append(lines, p+":"+h)
	}
	sort.Strings(lines)
	lines = append(lines, "caps:"+lk.CapsHash, fmt.Sprintf("tests:%d", lk.SpecLinkedTests))
	return sha256hex([]byte(strings.Join(lines, "\n")))
}

func readLock(repoRoot, id string) (*Lock, error) {
	b, err := os.ReadFile(lockPath(repoRoot, id))
	if err != nil {
		return nil, err
	}
	var lk Lock
	if err := json.Unmarshal(b, &lk); err != nil {
		return nil, fmt.Errorf("approved.lock for %s is corrupt (%v); re-approve with `blueprint approve %s --amend` after reviewing the change", id, err, id)
	}
	return &lk, nil
}

// Approve stamps approved.lock for a change (DESIGN §7 step 1) and journals
// the event. amend re-stamps an existing lock — the human-only, logged path
// for legitimate spec/test evolution; without amend an existing lock is an
// error so approval can never be silently overwritten.
func Approve(repoRoot, id string, amend bool, opts Options) (*Lock, error) {
	hk := opts.hooks()
	if hk.LoadChange == nil {
		return nil, fmt.Errorf("internal/spec is not wired into this build; enable the integrated wiring (internal/verify/wiring_integrated.go, build tag blueprint_integrated) or inject Hooks.LoadChange")
	}
	c, err := hk.LoadChange(repoRoot, id)
	if err != nil {
		return nil, err
	}

	if _, statErr := os.Stat(lockPath(repoRoot, id)); statErr == nil && !amend {
		return nil, fmt.Errorf("change %s is already approved (approved.lock exists); if the spec or tests legitimately evolved, run `blueprint approve %s --amend` — amendments are journaled and reviewable", id, id)
	}

	// The status flip must happen BEFORE hashing: the lock covers change.md,
	// so persisting draft->approved after computeLock would invalidate the
	// lock's own hash and every subsequent verify would report TAMPER against
	// a file approve itself mutated. Flip first, hash what will be on disk.
	flipped := false
	if hk.SaveChange != nil && c.Status == core.StatusDraft {
		c.Status = core.StatusApproved
		if err := hk.SaveChange(repoRoot, c); err != nil {
			return nil, fmt.Errorf("change status update failed; nothing approved: %w", err)
		}
		flipped = true
	}
	restore := func() {
		if flipped {
			c.Status = core.StatusDraft
			_ = hk.SaveChange(repoRoot, c)
		}
	}

	lk, err := computeLock(repoRoot, c)
	if err != nil {
		restore()
		return nil, err
	}
	lk.ApprovedAt = opts.now().UTC().Format(timeLayout)

	if err := os.MkdirAll(changeDir(repoRoot, id), 0o755); err != nil {
		restore()
		return nil, err
	}
	b, err := json.MarshalIndent(lk, "", "  ")
	if err != nil {
		restore()
		return nil, err
	}
	if err := os.WriteFile(lockPath(repoRoot, id), append(b, '\n'), 0o644); err != nil {
		restore()
		return nil, err
	}

	if err := appendJournal(repoRoot, id, core.JournalEvent{
		Time:     opts.now().UTC(),
		Kind:     "approve",
		ChangeID: id,
		Data: map[string]any{
			"amend":             amend,
			"composite":         lk.Composite,
			"spec_linked_tests": lk.SpecLinkedTests,
		},
	}); err != nil {
		return nil, err
	}
	return lk, nil
}

// checkTamper recomputes the lock inputs and reports drift (DESIGN §7 steps
// 2-3). The returned CheckResult carries a diff of what changed so failure is
// actionable, not just red.
func checkTamper(repoRoot string, c *core.Change) core.CheckResult {
	res := core.CheckResult{Name: "tamper", Pass: true}

	approved, err := readLock(repoRoot, c.ID)
	if err != nil {
		if os.IsNotExist(err) {
			res.Pass = false
			res.Detail = fmt.Sprintf("no approved.lock for change %s: the change was never approved; a human must run `blueprint approve %s` before verify can attest tamper-evidence", c.ID, c.ID)
			return res
		}
		res.Pass = false
		res.Detail = err.Error()
		return res
	}

	current, err := computeLock(repoRoot, c)
	if err != nil {
		res.Pass = false
		res.Detail = fmt.Sprintf("recomputing lock inputs failed: %v", err)
		return res
	}

	var drift []string
	// Files present at approval: changed or deleted?
	paths := make([]string, 0, len(approved.Files))
	for p := range approved.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		was := approved.Files[p]
		now, ok := current.Files[p]
		switch {
		case !ok && was != "":
			drift = append(drift, fmt.Sprintf("removed: %s (approved sha256 %.12s...)", p, was))
		case ok && now != was:
			drift = append(drift, fmt.Sprintf("modified: %s (approved %.12s... -> current %.12s...)", p, was, orAbsent(now)))
		}
	}
	// Files that joined the approved set (e.g. a new spec-linked test file is
	// fine — floor only checks decreases — but a new referenced spec area
	// means change.md drifted, which the change.md hash already catches).
	if approved.CapsHash != current.CapsHash {
		drift = append(drift, "modified: loop caps in change.md frontmatter (max_iterations/max_minutes/max_usd/breaker)")
	}

	// Executed-test-count floor.
	if current.SpecLinkedTests < approved.SpecLinkedTests {
		drift = append(drift, fmt.Sprintf("spec-linked test count dropped: approved %d -> current %d (trace annotations were removed or test files deleted)", approved.SpecLinkedTests, current.SpecLinkedTests))
	}

	// Skip/only markers in approved test files.
	for _, tf := range approved.TestFiles {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(tf)))
		if err != nil {
			continue // deletion already reported as drift above
		}
		if hits := findSkipMarkers(string(b)); len(hits) > 0 {
			drift = append(drift, fmt.Sprintf("skip/only marker in approved test file %s: %s", tf, strings.Join(hits, ", ")))
		}
	}

	if len(drift) > 0 {
		res.Pass = false
		res.Detail = "TAMPER: approved inputs drifted since `blueprint approve`:\n  - " +
			strings.Join(drift, "\n  - ") +
			fmt.Sprintf("\nIf this evolution is legitimate, a human must review and run `blueprint approve %s --amend`; the amendment is journaled.", c.ID)
	}
	return res
}

func orAbsent(h string) string {
	if h == "" {
		return "absent"
	}
	return h
}

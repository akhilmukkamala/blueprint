// Package garden is the MANUAL doc-gardening report (DESIGN §9; automation
// stays deferred per the MVP split). It folds knowledge-lint findings, spec-
// lint warnings, repo-map staleness, archive candidates (closed changes still
// under .blueprint/changes/), and slop candidates (oversize knowledge files +
// near-duplicate content) into one prioritized fix list. Every item carries
// the exact command or edit to make. Strictly read-only: no PRs, no writes —
// the success metric it frames is NET LINES DELETED, the structural anti-slop
// stance.
package garden

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/lint"
	"blueprint/internal/repomap"
	"blueprint/internal/spec"
)

// Priorities: lower number = do first.
const (
	PrioLintError = 1 // knowledge-lint errors: broken truth
	PrioMapStale  = 2 // stale retrieval index poisons every loop
	PrioArchive   = 3 // closed-but-unarchived changes clutter the working set
	PrioOversize  = 4 // oversize knowledge files: slop by volume
	PrioDuplicate = 5 // near-duplicate content: slop by repetition
	PrioLintWarn  = 6 // knowledge-lint warnings: aging truth
	PrioSpecWarn  = 7 // spec-lint warnings folded in last
)

// Item is one row of the fix list.
type Item struct {
	Priority int    `json:"priority"`
	Category string `json:"category"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"` // the exact command or edit to make
}

// Report is the full garden output. SuggestedDeletions is the net-lines-delta
// framing: how many lines of stale/duplicate content the report says to delete.
type Report struct {
	GeneratedAt        time.Time `json:"generated_at"`
	Items              []Item    `json:"items"`
	SuggestedDeletions int       `json:"suggested_deletions_lines"`
	Summary            string    `json:"summary"`
}

// oversizeLineCap is the slop threshold for one knowledge file.
const oversizeLineCap = 300

// dupOverlapThreshold is the normalized-line-hash overlap ratio above which
// two knowledge files count as near-duplicates.
const dupOverlapThreshold = 0.6

// dupMinShared is the minimum number of shared substantive lines before a
// pair is reported (guards against tiny files hitting the ratio trivially).
const dupMinShared = 3

// Options injects the clock and git (for the map-staleness comparison).
type Options struct {
	Now        func() time.Time
	Git        func(repoRoot string, args ...string) (string, error)
	LintConfig lint.Config
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now().UTC()
}

func (o Options) git(repoRoot string, args ...string) (string, error) {
	if o.Git != nil {
		return o.Git(repoRoot, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Run builds the report. It never writes anything under repoRoot.
func Run(repoRoot string, opts Options) (*Report, error) {
	r := &Report{GeneratedAt: opts.now().UTC()}

	// 1. Knowledge lint: errors first, warnings later.
	kf, err := lint.Knowledge(repoRoot, opts.now(), opts.LintConfig)
	if err != nil {
		return nil, err
	}
	for _, f := range kf {
		prio := PrioLintWarn
		if f.Severity == lint.SevError {
			prio = PrioLintError
		}
		r.Items = append(r.Items, Item{
			Priority: prio, Category: "knowledge-lint", File: f.File, Line: f.Line,
			Detail: fmt.Sprintf("[%s] %s", f.Rule, f.Message), Fix: f.Remediation,
		})
	}

	// 2. Spec lint warnings (errors belong to `blueprint lint`, not garden).
	sf, err := lint.Spec(repoRoot, opts.LintConfig)
	if err != nil {
		return nil, err
	}
	for _, f := range sf {
		if f.Severity != lint.SevWarning {
			continue
		}
		r.Items = append(r.Items, Item{
			Priority: PrioSpecWarn, Category: "spec-lint", File: f.File, Line: f.Line,
			Detail: fmt.Sprintf("[%s] %s", f.Rule, f.Message), Fix: f.Remediation,
		})
	}

	r.Items = append(r.Items, mapStaleness(repoRoot, opts)...)
	r.Items = append(r.Items, archiveCandidates(repoRoot)...)

	infos, err := lint.KnowledgeFiles(repoRoot)
	if err != nil {
		return nil, err
	}
	oversizeItems, oversizeLines := oversizeCandidates(infos)
	r.Items = append(r.Items, oversizeItems...)
	dupItems, dupLines := duplicateCandidates(repoRoot, infos)
	r.Items = append(r.Items, dupItems...)

	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].Priority < r.Items[j].Priority })

	r.SuggestedDeletions = oversizeLines + dupLines
	if r.SuggestedDeletions > 0 {
		r.Summary = fmt.Sprintf("net-lines suggestion: delete %d lines of stale/duplicate content — gardening success is measured in net lines deleted", r.SuggestedDeletions)
	} else if len(r.Items) > 0 {
		r.Summary = fmt.Sprintf("%d item(s) to fix, no bulk deletions suggested — net-lines target for this cycle is <= 0", len(r.Items))
	} else {
		r.Summary = "garden is clean: no stale docs, no orphans, no slop — nothing to delete"
	}
	return r, nil
}

// mapStaleness compares .blueprint/map.json's mtime against the last commit
// timestamp: a map older than the newest commit is feeding stale symbols to
// the router and every retrieval call.
func mapStaleness(repoRoot string, opts Options) []Item {
	const fix = "run `blueprint map --refresh --quiet` (and `blueprint doctor --install-hooks` so post-commit/post-merge keep it fresh automatically)"
	cache := repomap.CachePath(repoRoot)
	rel := ".blueprint/map.json"
	if r, err := filepath.Rel(repoRoot, cache); err == nil {
		rel = filepath.ToSlash(r)
	}
	st, err := os.Stat(cache)
	if err != nil {
		return []Item{{Priority: PrioMapStale, Category: "map-stale", File: rel,
			Detail: "no repo-map cache — Tier-1 retrieval and the router's blast-radius input are flying blind",
			Fix:    fix}}
	}
	out, err := opts.git(repoRoot, "log", "-1", "--format=%ct")
	if err != nil {
		return nil // no commits / no git: nothing to compare against
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return nil
	}
	last := time.Unix(sec, 0)
	if st.ModTime().Before(last) {
		return []Item{{Priority: PrioMapStale, Category: "map-stale", File: rel,
			Detail: fmt.Sprintf("map.json written %s but the last commit is %s — the map predates the code",
				st.ModTime().UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339)),
			Fix: fix}}
	}
	return nil
}

// archiveCandidates flags closed changes still sitting under
// .blueprint/changes/ — close is supposed to move them to archive/.
func archiveCandidates(repoRoot string) []Item {
	ids, err := spec.ListChanges(repoRoot)
	if err != nil {
		return nil
	}
	var items []Item
	for _, id := range ids {
		c, err := spec.LoadChange(repoRoot, id)
		if err != nil {
			continue // malformed changes are `blueprint lint`'s finding, not garden's
		}
		if c.Status != core.StatusClosed {
			continue
		}
		items = append(items, Item{
			Priority: PrioArchive, Category: "archive-candidate",
			File:   ".blueprint/changes/" + id + "/change.md",
			Detail: fmt.Sprintf("change %q is closed but still lives under .blueprint/changes/", id),
			Fix:    fmt.Sprintf("run `blueprint close %s` — close moves the folder to .blueprint/archive/%s/ (if close already ran and was interrupted, move the folder there yourself)", id, id),
		})
	}
	return items
}

// oversizeCandidates flags knowledge files past the slop line cap and counts
// the overage as suggested deletions.
func oversizeCandidates(infos []lint.KnowledgeFileInfo) ([]Item, int) {
	var items []Item
	lines := 0
	for _, info := range infos {
		if info.Lines <= oversizeLineCap {
			continue
		}
		over := info.Lines - oversizeLineCap
		lines += over
		items = append(items, Item{
			Priority: PrioOversize, Category: "slop-oversize", File: info.RelPath,
			Detail: fmt.Sprintf("%d lines (cap %d) — knowledge files past the cap stop being read", info.Lines, oversizeLineCap),
			Fix:    fmt.Sprintf("edit %s: delete stale sections or split detail into linked sub-pages until it is <=%d lines (~%d lines to remove)", info.RelPath, oversizeLineCap, over),
		})
	}
	return items, lines
}

// duplicateCandidates detects near-duplicate knowledge files: normalize each
// substantive line, hash it into a per-file set, and report pairs whose
// overlap exceeds dupOverlapThreshold of the smaller file.
func duplicateCandidates(repoRoot string, infos []lint.KnowledgeFileInfo) ([]Item, int) {
	sets := make([]map[string]bool, len(infos))
	for i, info := range infos {
		raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(info.RelPath)))
		if err != nil {
			continue
		}
		sets[i] = normalizedLineSet(string(raw))
	}
	var items []Item
	lines := 0
	for i := range infos {
		for j := i + 1; j < len(infos); j++ {
			a, b := sets[i], sets[j]
			if len(a) == 0 || len(b) == 0 {
				continue
			}
			shared := 0
			for l := range a {
				if b[l] {
					shared++
				}
			}
			min := len(a)
			if len(b) < min {
				min = len(b)
			}
			ratio := float64(shared) / float64(min)
			if shared < dupMinShared || ratio <= dupOverlapThreshold {
				continue
			}
			lines += shared
			items = append(items, Item{
				Priority: PrioDuplicate, Category: "slop-duplicate", File: infos[i].RelPath,
				Detail: fmt.Sprintf("%d%% of %s duplicates %s (%d shared lines)", int(ratio*100), smallerOf(infos[i], infos[j], len(a) <= len(b)), infos[j].RelPath, shared),
				Fix: fmt.Sprintf("pick one canonical home: keep the content in %s, delete the ~%d duplicated lines from %s, and link instead",
					infos[i].RelPath, shared, infos[j].RelPath),
			})
		}
	}
	return items, lines
}

func smallerOf(a, b lint.KnowledgeFileInfo, aSmaller bool) string {
	if aSmaller {
		return a.RelPath
	}
	return b.RelPath
}

// normalizedLineSet lowercases, whitespace-collapses, and de-punctuates each
// line, keeping only substantive ones (>=16 chars normalized) so headings and
// boilerplate don't count as duplication.
func normalizedLineSet(content string) map[string]bool {
	set := map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		n := strings.ToLower(strings.Join(strings.Fields(l), " "))
		n = strings.TrimLeft(n, "#>-*• \t")
		if len(n) < 16 {
			continue
		}
		set[n] = true
	}
	return set
}

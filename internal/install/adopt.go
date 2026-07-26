package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// Baselines is .blueprint/baselines.json: framework-off measurements captured
// at adoption stage 0 so later metrics have a before (DESIGN §14). Rates are
// derived from git history over the trailing window — no timing protocol here,
// only what the repo already proves.
type Baselines struct {
	CapturedAt time.Time `json:"captured_at"`
	WindowDays int       `json:"window_days"`
	Commits    int       `json:"commits"`
	Reverts    int       `json:"reverts"`
	FixCommits int       `json:"fix_commits"`
	RevertRate float64   `json:"revert_rate"`
	FixRate    float64   `json:"fix_rate"`
	Note       string    `json:"note,omitempty"`
}

// BaselinesPath returns the baselines location under repoRoot.
func BaselinesPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "baselines.json")
}

// AdoptResult reports the brownfield stage-0 outcome.
type AdoptResult struct {
	Init      *InitResult `json:"init"`
	Baselines Baselines   `json:"baselines"`
	Imported  []string    `json:"imported,omitempty"`  // steering drafts written
	Knowledge []string    `json:"knowledge,omitempty"` // knowledge skeletons written
	Shim      bool        `json:"claude_shim_written"`
}

// baselineWindowDays is the trailing git-history window (DESIGN §14 stage 0).
const baselineWindowDays = 90

// Adopt runs adoption stage 0 on an existing repo: import pre-existing agent
// files (CLAUDE.md, .cursorrules, .cursor/rules/*) into .blueprint/steering/
// as provenance-tagged drafts, install the floor (init), replace CLAUDE.md
// with the shim once its content is safely imported, capture git-derived
// baselines, and append the stage-0 worklog event. now stamps baselines and
// provenance comments — the caller passes the wall clock explicitly
// (CONTRACTS rule 5).
func Adopt(repoRoot string, now time.Time) (*AdoptResult, error) {
	start := time.Now()
	res := &AdoptResult{}

	imported, claudeImported, err := importAgentFiles(repoRoot, now)
	if err != nil {
		return nil, err
	}
	res.Imported = imported

	initRes, err := runInit(repoRoot, InitOptions{})
	if err != nil {
		return nil, err
	}
	res.Init = initRes

	// The pre-existing CLAUDE.md made init skip the shim; its content now
	// lives in steering/, so replacing it loses nothing user-owned.
	if claudeImported {
		if err := writeShim(repoRoot); err != nil {
			return nil, err
		}
		res.Shim = true
	}

	// Stage 0 writes the knowledge-store skeletons (DESIGN §2, §9): [user]
	// files whose curation is the later stages' work; existing files are kept.
	kn, err := MaterializeKnowledge(repoRoot)
	if err != nil {
		return nil, err
	}
	res.Knowledge = kn

	b, err := captureBaselines(repoRoot, now)
	if err != nil {
		return nil, err
	}
	res.Baselines = b
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("install: cannot encode baselines: %w", err)
	}
	if err := writeFile(repoRoot, ".blueprint/baselines.json", append(raw, '\n')); err != nil {
		return nil, err
	}
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return nil, err
	}
	m.record(".blueprint/baselines.json", core.OwnerTool, append(raw, '\n'))
	if err := m.Save(repoRoot); err != nil {
		return nil, err
	}

	ev := core.JournalEvent{
		Time: now.UTC(),
		Kind: "adopt-stage",
		Data: map[string]any{
			"stage":       0,
			"imported":    len(imported),
			"commits_90d": b.Commits,
			"revert_rate": b.RevertRate,
			"fix_rate":    b.FixRate,
		},
	}
	if err := worklog.Append(repoRoot, ev); err != nil {
		return nil, err
	}
	if err := appendSelfTiming(repoRoot, "adopt", time.Since(start)); err != nil {
		return nil, err
	}
	return res, nil
}

// importAgentFiles copies pre-existing agent-facing files into
// .blueprint/steering/ as [user]-tier drafts with a provenance header. The
// sources are left in place (adopt is read-only on user files); only CLAUDE.md
// is later replaced by the shim, and only because its content was imported.
func importAgentFiles(repoRoot string, now time.Time) (imported []string, claudeImported bool, err error) {
	type source struct {
		abs  string
		from string // provenance label
		dest string // steering file name
	}
	var sources []source

	claudePath := filepath.Join(repoRoot, "CLAUDE.md")
	if b, err := os.ReadFile(claudePath); err == nil && !isShim(b) {
		sources = append(sources, source{abs: claudePath, from: "CLAUDE.md", dest: "imported-claude.md"})
		claudeImported = true
	}
	cursorrules := filepath.Join(repoRoot, ".cursorrules")
	if _, err := os.Stat(cursorrules); err == nil {
		sources = append(sources, source{abs: cursorrules, from: ".cursorrules", dest: "imported-cursorrules.md"})
	}
	rulesDir := filepath.Join(repoRoot, ".cursor", "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ext := filepath.Ext(e.Name()); ext == ".md" || ext == ".mdc" {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			base := strings.TrimSuffix(n, filepath.Ext(n))
			sources = append(sources, source{
				abs:  filepath.Join(rulesDir, n),
				from: ".cursor/rules/" + n,
				dest: "imported-cursor-" + base + ".md",
			})
		}
	}

	for _, s := range sources {
		body, err := os.ReadFile(s.abs)
		if err != nil {
			return imported, claudeImported, fmt.Errorf("install: cannot read %s during adopt: %w", s.abs, err)
		}
		// Frontmatter first: the adapters steering loader requires it, and
		// adopt output must be sync-clean out of the box. activation:manual
		// marks an uncurated draft (never auto-applied by generated rules).
		id := strings.TrimSuffix(s.dest, ".md")
		header := fmt.Sprintf(
			"---\n# imported by `blueprint adopt` on %s from %s — curate me\nid: %s\ndescription: \"Imported from %s during adopt; curate into a scoped rule or fold into AGENTS.md\"\nglobs: []\nactivation: manual\n---\n\n",
			now.UTC().Format("2006-01-02"), s.from, id, s.from)
		rel := ".blueprint/steering/" + s.dest
		dst := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(dst); err == nil {
			continue // re-adopt must not clobber a curated draft
		}
		if err := writeFile(repoRoot, rel, append([]byte(header), body...)); err != nil {
			return imported, claudeImported, err
		}
		imported = append(imported, rel)
	}
	return imported, claudeImported, nil
}

// isShim reports whether a CLAUDE.md is already the Blueprint-generated shim
// (so re-adopting does not re-import our own output).
func isShim(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "generated by `blueprint init`") && strings.Contains(s, "@AGENTS.md")
}

// writeShim overwrites CLAUDE.md with the [tool] shim and records it.
func writeShim(repoRoot string) error {
	t, ok := templateByRelPath("CLAUDE.md")
	if !ok {
		return fmt.Errorf("install: CLAUDE.md template missing — this is a build defect")
	}
	content, err := t.Content()
	if err != nil {
		return err
	}
	if err := writeFile(repoRoot, "CLAUDE.md", content); err != nil {
		return err
	}
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("install: manifest missing while writing the CLAUDE.md shim — run `blueprint init` first")
	}
	m.record("CLAUDE.md", core.OwnerTool, content)
	return m.Save(repoRoot)
}

// captureBaselines derives trailing-90-day rework signals from git history:
// revert rate (subjects starting "Revert") and fix-commit rate (conventional
// fix: / fix( subjects). A repo without usable git history yields a zero
// baseline with an explanatory note rather than an error — adoption must not
// stall on a shallow clone.
func captureBaselines(repoRoot string, now time.Time) (Baselines, error) {
	b := Baselines{CapturedAt: now.UTC(), WindowDays: baselineWindowDays}
	since := now.UTC().AddDate(0, 0, -baselineWindowDays).Format("2006-01-02")
	cmd := exec.Command("git", "-C", repoRoot, "log", "--since="+since, "--no-merges", "--pretty=%s")
	out, err := cmd.Output()
	if err != nil {
		b.Note = "no usable git history (git log failed) — baseline rates default to zero; re-run `blueprint adopt` from a full clone to capture real rates"
		return b, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		subject := strings.TrimSpace(line)
		if subject == "" {
			continue
		}
		b.Commits++
		lower := strings.ToLower(subject)
		switch {
		case strings.HasPrefix(lower, "revert"):
			b.Reverts++
		case strings.HasPrefix(lower, "fix:"), strings.HasPrefix(lower, "fix("), strings.HasPrefix(lower, "fixup"), strings.HasPrefix(lower, "hotfix"):
			b.FixCommits++
		}
	}
	if b.Commits > 0 {
		b.RevertRate = float64(b.Reverts) / float64(b.Commits)
		b.FixRate = float64(b.FixCommits) / float64(b.Commits)
	} else {
		b.Note = "no commits in the trailing window — baseline rates are zero by absence of history"
	}
	return b, nil
}

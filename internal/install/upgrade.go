package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"blueprint/internal/core"
)

// UpgradeOptions configures Upgrade. DryRun computes everything and writes
// nothing (the dirty-tree refusal is skipped: a read-only pass is always
// safe). WithDiff attaches unified diffs to changed files.
type UpgradeOptions struct {
	DryRun   bool
	WithDiff bool
}

// FileUpgrade is one file's upgrade outcome.
type FileUpgrade struct {
	Path   string             `json:"path"`
	Tier   core.OwnershipTier `json:"tier"`
	Action string             `json:"action"` // unchanged | replace | merge | conflict | reinstall | skip-user | skip-untracked
	Note   string             `json:"note,omitempty"`
	Diff   string             `json:"diff,omitempty"`
}

// UpgradeResult is the exit summary. Writing files is all Upgrade does —
// making exactly one commit out of them is the caller's job.
type UpgradeResult struct {
	FromVersion string        `json:"from_version"`
	ToVersion   string        `json:"to_version"`
	DryRun      bool          `json:"dry_run"`
	Files       []FileUpgrade `json:"files"`
	Conflicts   int           `json:"conflicts"`
}

// Upgrade performs the Copier-grade three-way merge honoring ownership tiers
// (DESIGN §11): [tool] files are replaced from the current templates, [user]
// files are never touched, [mixed] files are merged only inside the managed
// region — the user body is preserved byte-for-byte, and a hand-edited managed
// region (stored hash vs current) gets inline conflict markers for the human
// to resolve. Refuses on a dirty git tree so the caller's single upgrade
// commit is clean and revertable.
func Upgrade(repoRoot string, opts UpgradeOptions) (*UpgradeResult, error) {
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("install: %s not found — nothing to upgrade; run `blueprint init` or `blueprint adopt` first", ManifestPath(repoRoot))
	}
	if !opts.DryRun {
		if err := refuseDirtyTree(repoRoot); err != nil {
			return nil, err
		}
	}

	res := &UpgradeResult{FromVersion: m.Version, ToVersion: TemplatesVersion, DryRun: opts.DryRun}

	for _, t := range Templates() {
		entry, tracked := m.Files[t.RelPath]
		if !tracked {
			// Never installed (e.g. optional template not yet materialized,
			// or a pre-existing user file init refused to touch): not ours.
			res.Files = append(res.Files, FileUpgrade{Path: t.RelPath, Tier: t.Tier, Action: "skip-untracked",
				Note: "not in manifest.json — Blueprint does not own this path"})
			continue
		}
		fu, newContent, err := planFileUpgrade(repoRoot, t, entry)
		if err != nil {
			return nil, err
		}
		if opts.WithDiff && newContent != nil {
			if cur, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(t.RelPath))); err == nil {
				fu.Diff = unifiedDiff(t.RelPath, string(cur), string(newContent))
			} else {
				fu.Diff = unifiedDiff(t.RelPath, "", string(newContent))
			}
		}
		if fu.Action == "conflict" {
			res.Conflicts++
		}
		if newContent != nil && !opts.DryRun {
			if err := writeFile(repoRoot, t.RelPath, newContent); err != nil {
				return nil, err
			}
			// The conflict path stores the incoming template's managed hash:
			// the region reads as hand-edited until the human resolves the
			// markers, so a re-run re-flags it instead of silently blessing it.
			tpl, err := t.Content()
			if err != nil {
				return nil, err
			}
			e := ManifestEntry{SHA256: hashBytes(newContent), Tier: t.Tier}
			if t.Tier == core.OwnerMixed {
				if h, ok := managedHash(string(tpl), t.RelPath); ok {
					e.ManagedSHA256 = h
				}
			}
			m.Files[t.RelPath] = e
		}
		res.Files = append(res.Files, fu)
	}

	if !opts.DryRun {
		m.Version = TemplatesVersion
		m.BinaryVersion = Version
		if err := m.Save(repoRoot); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// planFileUpgrade decides one file's action and (when it changes) its new
// content. nil content means nothing to write.
func planFileUpgrade(repoRoot string, t TemplateFile, entry ManifestEntry) (FileUpgrade, []byte, error) {
	fu := FileUpgrade{Path: t.RelPath, Tier: t.Tier}
	tpl, err := t.Content()
	if err != nil {
		return fu, nil, err
	}
	cur, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(t.RelPath)))
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return fu, nil, fmt.Errorf("install: cannot read %s during upgrade: %w", t.RelPath, err)
	}

	switch t.Tier {
	case core.OwnerUser:
		// Tier contract: user files are untouched even if the template moved on.
		fu.Action = "skip-user"
		fu.Note = "[user]-owned — upgrade never touches it"
		if missing {
			fu.Note = "[user]-owned and deleted by the user — upgrade honors the deletion"
		}
		return fu, nil, nil

	case core.OwnerTool:
		if missing {
			fu.Action = "reinstall"
			fu.Note = "tracked [tool] file was missing; restored from template"
			return fu, tpl, nil
		}
		if hashBytes(cur) == hashBytes(tpl) {
			fu.Action = "unchanged"
			return fu, nil, nil
		}
		fu.Action = "replace"
		fu.Note = "[tool]-owned — regenerated from the current template (hand edits to tool files do not survive upgrades)"
		return fu, tpl, nil

	default: // core.OwnerMixed
		if missing {
			fu.Action = "reinstall"
			fu.Note = "tracked [mixed] file was missing; restored from template (no user body to preserve)"
			return fu, tpl, nil
		}
		tplSplit := splitManaged(string(tpl), t.RelPath)
		if !tplSplit.found {
			return fu, nil, fmt.Errorf("install: template for %s has no managed region — this is a build defect", t.RelPath)
		}
		curSplit := splitManaged(string(cur), t.RelPath)
		if !curSplit.found {
			// The user removed the whole region; re-append a fresh one at the
			// end of their body. No user byte is lost.
			body := string(cur)
			if body != "" && !strings.HasSuffix(body, "\n") {
				body += "\n"
			}
			start, end := markersFor(t.RelPath)
			fu.Action = "merge"
			fu.Note = "managed region was missing; re-appended a fresh one after the user body"
			return fu, []byte(body + start + "\n" + tplSplit.inner + end + "\n"), nil
		}
		curHash := hashBytes([]byte(curSplit.inner))
		newHash := hashBytes([]byte(tplSplit.inner))
		switch {
		case curHash == newHash:
			fu.Action = "unchanged"
			fu.Note = "managed region already matches the current template; user body untouched"
			return fu, nil, nil
		case entry.ManagedSHA256 == "" || curHash == entry.ManagedSHA256:
			// Clean three-way case: region is as installed, replace it.
			fu.Action = "merge"
			fu.Note = "managed region replaced from template; user body preserved"
			return fu, []byte(renderManaged(curSplit, t.RelPath, tplSplit.inner)), nil
		default:
			// Hand-edited region: surface both sides inline.
			fu.Action = "conflict"
			fu.Note = "managed region was hand-edited — resolve the inline conflict markers in " + t.RelPath + ", keep what you need, delete the markers, then commit"
			inner := conflictInner(curSplit.inner, tplSplit.inner, TemplatesVersion)
			return fu, []byte(renderManaged(curSplit, t.RelPath, inner)), nil
		}
	}
}

// refuseDirtyTree blocks upgrade when git reports uncommitted changes, so the
// caller's one upgrade commit is isolated and `git revert` is a clean
// rollback. A directory that is not a git repo (or has no git binary) cannot
// be dirty; upgrade proceeds — there is nothing to protect.
func refuseDirtyTree(repoRoot string) error {
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		return nil
	}
	out, err := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("install: the git tree is dirty — commit or stash your changes first so the upgrade lands as exactly one clean, revertable commit (run `git status` to see what is pending, or preview with `blueprint upgrade --dry-run`)")
	}
	return nil
}

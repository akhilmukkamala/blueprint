package install

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"blueprint/internal/core"
)

// RemainItem is a path uninstall deliberately left behind, with the reason.
type RemainItem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// UninstallResult reports what was removed, what was stripped, and what stays.
type UninstallResult struct {
	Removed   []string     `json:"removed"`
	Stripped  []string     `json:"stripped"` // mixed files whose managed region was removed
	Remaining []RemainItem `json:"remaining"`
	Purged    bool         `json:"purged"`
}

// Uninstall removes Blueprint from a repo while keeping its memory: [tool]
// files are deleted, [mixed] files keep the user body with the managed region
// stripped, [user] files (and the whole knowledge/spec corpus) stay. purge
// additionally deletes .blueprint/ entirely — history, specs, knowledge and
// all; the interactive confirmation for that lives in the CLI, not here.
func Uninstall(repoRoot string, purge bool) (*UninstallResult, error) {
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return nil, err
	}
	if m == nil && !purge {
		return nil, fmt.Errorf("install: %s not found — nothing is installed here; if a stray .blueprint/ directory remains, `blueprint uninstall --purge` deletes it", ManifestPath(repoRoot))
	}
	res := &UninstallResult{}

	if m != nil {
		paths := make([]string, 0, len(m.Files))
		for p := range m.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, rel := range paths {
			entry := m.Files[rel]
			abs := filepath.Join(repoRoot, filepath.FromSlash(rel))
			switch entry.Tier {
			case core.OwnerTool:
				if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
					return nil, fmt.Errorf("install: cannot remove %s: %w", abs, err)
				}
				res.Removed = append(res.Removed, rel)
			case core.OwnerMixed:
				cur, err := os.ReadFile(abs)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("install: cannot read %s during uninstall: %w", abs, err)
				}
				stripped, found := stripManaged(string(cur), rel)
				if !found {
					res.Remaining = append(res.Remaining, RemainItem{Path: rel,
						Reason: "[mixed] file has no managed region left — treated as fully user-owned"})
					continue
				}
				if strings.TrimSpace(stripped) == "" {
					// Nothing user-authored survives the strip; an empty husk
					// helps nobody.
					if err := os.Remove(abs); err != nil {
						return nil, fmt.Errorf("install: cannot remove %s: %w", abs, err)
					}
					res.Removed = append(res.Removed, rel)
					continue
				}
				if err := os.WriteFile(abs, []byte(stripped), 0o644); err != nil {
					return nil, fmt.Errorf("install: cannot write %s during uninstall: %w", abs, err)
				}
				res.Stripped = append(res.Stripped, rel)
				res.Remaining = append(res.Remaining, RemainItem{Path: rel,
					Reason: "user body preserved; only the managed region was removed"})
			default: // user
				if _, err := os.Stat(abs); err == nil {
					res.Remaining = append(res.Remaining, RemainItem{Path: rel,
						Reason: "[user]-owned — the repo keeps its memory; delete manually if truly unwanted"})
				}
			}
		}
		// The manifest itself is [tool]: the last thing to go.
		if err := os.Remove(ManifestPath(repoRoot)); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("install: cannot remove %s: %w", ManifestPath(repoRoot), err)
		}
		res.Removed = append(res.Removed, ".blueprint/manifest.json")
	}

	nsDir := filepath.Join(repoRoot, ".blueprint")
	if purge {
		if err := os.RemoveAll(nsDir); err != nil {
			return nil, fmt.Errorf("install: cannot purge %s: %w", nsDir, err)
		}
		res.Purged = true
		// Purge invalidates any "remaining" claims under .blueprint/.
		kept := res.Remaining[:0]
		for _, r := range res.Remaining {
			if !strings.HasPrefix(r.Path, ".blueprint/") {
				kept = append(kept, r)
			}
		}
		res.Remaining = kept
		return res, nil
	}

	// Report the notable survivors under .blueprint/ so "what remains and
	// why" is complete even for paths the manifest never tracked.
	for _, sub := range []struct{ rel, reason string }{
		{".blueprint/specs", "living specs are [user] knowledge"},
		{".blueprint/changes", "change folders are the project record"},
		{".blueprint/knowledge", "ADRs, runbooks and glossary are [user] knowledge"},
		{".blueprint/steering", "steering rules are [user]-owned"},
		{".blueprint/log", "worklog history stays unless --purge"},
		{".blueprint/archive", "closed changes stay unless --purge"},
	} {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(sub.rel))); err == nil {
			res.Remaining = append(res.Remaining, RemainItem{Path: sub.rel + "/", Reason: sub.reason})
		}
	}
	if entries, err := os.ReadDir(nsDir); err == nil && len(entries) == 0 {
		if err := os.Remove(nsDir); err == nil {
			res.Removed = append(res.Removed, ".blueprint/")
		}
	}
	return res, nil
}

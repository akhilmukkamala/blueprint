package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueprint/internal/core"
)

// Action is one planned or executed install step.
type Action struct {
	Path string             `json:"path"`
	Tier core.OwnershipTier `json:"tier"`
	Op   string             `json:"op"` // write | skip-exists | manifest
	Note string             `json:"note,omitempty"`
}

// InitOptions configures Init. Offline is accepted for the USB-stick install
// path (DESIGN §11); every template is embedded, so it changes nothing except
// being recorded in the result — init never touches the network either way.
type InitOptions struct {
	Offline bool
}

// InitResult reports the plan and what actually happened.
type InitResult struct {
	Version string   `json:"version"`
	Plan    []Action `json:"plan"`
	Written []string `json:"written"`
	Skipped []string `json:"skipped"`
	Offline bool     `json:"offline,omitempty"`
}

// PlanInit computes the greenfield install plan without writing anything:
// the four-file floor (AGENTS.md, CLAUDE.md, config.toml, manifest.json).
// Optional templates (registry/safety/verifiers.toml) appear on first use via
// Materialize — the minimalist floor (DESIGN §2). Existing files are never
// overwritten, whatever their tier: init is idempotent and a pre-existing
// AGENTS.md or CLAUDE.md is user content until `blueprint adopt` imports it.
func PlanInit(repoRoot string) ([]Action, error) {
	var plan []Action
	for _, t := range Templates() {
		if !t.Floor {
			continue
		}
		a := Action{Path: t.RelPath, Tier: t.Tier, Op: "write"}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(t.RelPath))); err == nil {
			a.Op = "skip-exists"
			a.Note = "already present — init never overwrites; run `blueprint adopt` to import existing agent files"
		}
		plan = append(plan, a)
	}
	plan = append(plan, Action{Path: ".blueprint/manifest.json", Tier: core.OwnerTool, Op: "manifest"})
	return plan, nil
}

// Init executes the plan: writes missing floor files and the manifest.
// Re-running is safe — existing files are skipped and their manifest entries
// (if any) are preserved. Init times itself and appends an "install" worklog
// event (self-timing, DESIGN §15); Adopt calls runInit directly so an adopt
// run logs exactly one such event.
func Init(repoRoot string, opts InitOptions) (*InitResult, error) {
	start := time.Now()
	res, err := runInit(repoRoot, opts)
	if err != nil {
		return nil, err
	}
	if err := appendSelfTiming(repoRoot, "init", time.Since(start)); err != nil {
		return nil, err
	}
	return res, nil
}

func runInit(repoRoot string, opts InitOptions) (*InitResult, error) {
	plan, err := PlanInit(repoRoot)
	if err != nil {
		return nil, err
	}
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = &Manifest{Files: map[string]ManifestEntry{}}
	}
	m.Version = TemplatesVersion
	m.BinaryVersion = Version

	res := &InitResult{Version: TemplatesVersion, Plan: plan, Offline: opts.Offline}
	for _, a := range plan {
		switch a.Op {
		case "skip-exists":
			res.Skipped = append(res.Skipped, a.Path)
		case "write":
			t, ok := templateByRelPath(a.Path)
			if !ok {
				return nil, fmt.Errorf("install: plan names %s but no template exists for it — this is a defect, report it", a.Path)
			}
			content, err := t.Content()
			if err != nil {
				return nil, err
			}
			if err := writeFile(repoRoot, a.Path, content); err != nil {
				return nil, err
			}
			m.record(a.Path, t.Tier, content)
			res.Written = append(res.Written, a.Path)
		}
	}
	if err := m.Save(repoRoot); err != nil {
		return nil, err
	}
	res.Written = append(res.Written, ".blueprint/manifest.json")
	return res, nil
}

// Materialize writes one optional template (registry.toml, safety.toml,
// verifiers.toml) on first use and records it in the manifest. It refuses to
// overwrite: an existing file is returned as-is with created=false, because
// optional templates are [user]-tier the moment they land.
func Materialize(repoRoot, relPath string) (created bool, err error) {
	t, ok := templateByRelPath(relPath)
	if !ok {
		return false, fmt.Errorf("install: no template for %q — installable optional files are: .blueprint/registry.toml, .blueprint/safety.toml, .blueprint/verifiers.toml, the .blueprint/knowledge/ skeleton set (see MaterializeKnowledge), and .blueprint/skills/<scenario>/SKILL.md for scenarios %s", relPath, strings.Join(SkillScenarios, "/"))
	}
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(t.RelPath))); err == nil {
		return false, nil
	}
	content, err := t.Content()
	if err != nil {
		return false, err
	}
	if err := writeFile(repoRoot, t.RelPath, content); err != nil {
		return false, err
	}
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return true, err
	}
	if m == nil {
		m = &Manifest{Version: TemplatesVersion, BinaryVersion: Version, Files: map[string]ManifestEntry{}}
	}
	m.record(t.RelPath, t.Tier, content)
	return true, m.Save(repoRoot)
}

// writeFile writes a repo-relative file, creating parent directories. Plain
// files only — no symlinks anywhere (Windows-clean).
func writeFile(repoRoot, relPath string, content []byte) error {
	p := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("install: cannot create %s — check directory permissions: %w", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		return fmt.Errorf("install: cannot write %s: %w", p, err)
	}
	return nil
}

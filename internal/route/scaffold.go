// Change-folder scaffolding for `blueprint new`. The change.md layout follows
// DESIGN §3/§6: TOML frontmatter (identity + loop contract) between +++
// fences, then EARS delta, tasks, and (full tier) Design sections.
//
// WriteChangeFile / DefaultChangePath implement the same contract as
// spec.SaveChange / spec.ChangePath (the spec-package API). The CLI calls
// them through seam variables so the integrator can flip to internal/spec
// with a two-line change once that package lands.
package route

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"blueprint/internal/core"
)

//go:embed templates/change.md.tmpl
var changeTemplate string

// DefaultChangePath mirrors spec.ChangePath: .blueprint/changes/<id>/change.md.
func DefaultChangePath(repoRoot, id string) string {
	return filepath.Join(repoRoot, ".blueprint", "changes", id, "change.md")
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// ChangeID derives a stable folder name from the intent: <yyyy-mm-dd>-<slug>.
// now is injected so routing stays deterministic under test.
func ChangeID(intent string, now time.Time) string {
	slug := slugStrip.ReplaceAllString(strings.ToLower(intent), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = "change"
	}
	return now.UTC().Format("2006-01-02") + "-" + slug
}

// UniqueChangeID appends -2, -3, … until the change folder is free.
func UniqueChangeID(repoRoot, intent string, now time.Time) string {
	base := ChangeID(intent, now)
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Dir(DefaultChangePath(repoRoot, id))); os.IsNotExist(err) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// DefaultContract fills the DESIGN §6 loop-contract defaults, scaled down for
// light tier (a light change should never need a full-tier budget).
func DefaultContract(id string, tier core.CeremonyTier) core.LoopContract {
	c := core.LoopContract{
		Predicate:     "blueprint verify " + id,
		MaxIterations: 12,
		MaxMinutes:    90,
		MaxUSD:        15.0,
		Breaker: core.Breaker{
			RepeatActionN:   3,
			RepeatErrorN:    3,
			NoDiffDeltaN:    3,
			OscillationN:    2,
			MonologueTokens: 4000,
		},
		Writable: []string{"src/**", "tests/**"},
		ReadOnly: []string{".blueprint/specs/**", ".blueprint/safety.toml"},
	}
	if tier == core.TierLight {
		c.MaxIterations = 6
		c.MaxMinutes = 45
		c.MaxUSD = 5.0
	}
	return c
}

type changeView struct {
	*core.Change
	SLAString string
	Full      bool
}

// WriteChangeFile renders change.md from the embedded template and writes the
// change folder. Mirrors spec.SaveChange's contract; refuses to overwrite an
// existing change.md (changes are created once, then edited by humans).
func WriteChangeFile(repoRoot string, c *core.Change) error {
	p := DefaultChangePath(repoRoot, c.ID)
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("change %s already exists at %s — pick a new intent or edit the existing change.md", c.ID, p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("cannot create change folder %s: %w", filepath.Dir(p), err)
	}
	tmpl, err := template.New("change").Parse(changeTemplate)
	if err != nil {
		return fmt.Errorf("embedded change template is invalid: %w", err)
	}
	view := changeView{Change: c, Full: c.Tier == core.TierFull}
	if c.SLA != nil {
		view.SLAString = c.SLA.UTC().Format(time.RFC3339)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, view); err != nil {
		return fmt.Errorf("cannot render change.md for %s: %w", c.ID, err)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", p, err)
	}
	return nil
}

// Package install owns the canonical-home lifecycle (DESIGN §2, §11, §14):
// embedded file templates, the install manifest, and the init / adopt /
// upgrade / uninstall operations. The ownership-tier contract is absolute:
// [tool] files are regenerable and replaced on upgrade, [user] files are never
// touched by tooling, [mixed] files are edited only inside the
// "blueprint:managed" region.
package install

import (
	"embed"
	"fmt"
	"path"
	"strings"

	"blueprint/internal/core"
)

//go:embed templates
var templatesFS embed.FS

// TemplatesVersion identifies the embedded template set; it is recorded in
// manifest.json and bumped whenever a template changes shape.
const TemplatesVersion = "0.3.0" // 0.3.0: knowledge-store skeletons (architecture/glossary/debt/ADR/runbooks)

// TemplateFile describes one installable file: where it lands in the repo,
// who owns it, and whether it is part of the four-file install floor
// (AGENTS.md, CLAUDE.md, config.toml + the generated manifest.json —
// DESIGN §2: everything else appears on first use).
type TemplateFile struct {
	// RelPath is the repo-relative install path, always slash-separated;
	// callers join with filepath for the OS form.
	RelPath string
	Tier    core.OwnershipTier
	Floor   bool
	src     string // path inside templatesFS
}

// SkillScenarios are the scenario classes with a bundled SKILL.md playbook
// (DESIGN §5): the five core scenarios. Other scenarios (e.g. sev1-hotfix)
// have no playbook template yet and materialize nothing.
var SkillScenarios = []string{"bug-fix", "refactor", "performance", "chore", "feature"}

// SkillPath returns the repo-relative (slash-separated) SKILL.md path for a
// scenario class.
func SkillPath(scenario string) string {
	return ".blueprint/skills/" + scenario + "/SKILL.md"
}

// knowledgeRelPaths is the [user] knowledge-store skeleton set (DESIGN §2, §9),
// in deterministic materialization order. All optional: written by `blueprint
// adopt` (stage 0) or MaterializeKnowledge, never by init (minimalist floor).
var knowledgeRelPaths = []string{
	".blueprint/knowledge/architecture.md",
	".blueprint/knowledge/glossary.md",
	".blueprint/knowledge/debt.md",
	".blueprint/knowledge/decisions/ADR-0000-template.md",
	".blueprint/knowledge/runbooks/dev-env.md",
	".blueprint/knowledge/runbooks/no-egress.md",
}

// MaterializeKnowledge writes the knowledge-store skeleton set on first use.
// Existing files are never overwritten (knowledge is [user]-tier the moment it
// lands); created lists only the files this call actually wrote.
func MaterializeKnowledge(repoRoot string) (created []string, err error) {
	for _, rel := range knowledgeRelPaths {
		ok, err := Materialize(repoRoot, rel)
		if err != nil {
			return created, err
		}
		if ok {
			created = append(created, rel)
		}
	}
	return created, nil
}

// Templates returns the embedded template set in deterministic install order.
func Templates() []TemplateFile {
	files := []TemplateFile{
		{RelPath: "AGENTS.md", Tier: core.OwnerMixed, Floor: true, src: "templates/AGENTS.md"},
		{RelPath: "CLAUDE.md", Tier: core.OwnerTool, Floor: true, src: "templates/CLAUDE.md"},
		{RelPath: ".blueprint/config.toml", Tier: core.OwnerMixed, Floor: true, src: "templates/config.toml"},
		{RelPath: ".blueprint/registry.toml", Tier: core.OwnerUser, src: "templates/registry.toml"},
		{RelPath: ".blueprint/safety.toml", Tier: core.OwnerUser, Floor: true, src: "templates/safety.toml"},
		{RelPath: ".blueprint/verifiers.toml", Tier: core.OwnerUser, src: "templates/verifiers.toml"},
	}
	// Scenario playbooks ([user], optional): materialized by `blueprint new`
	// on first use of the scenario, never at init (minimalist floor, DESIGN §2).
	for _, s := range SkillScenarios {
		files = append(files, TemplateFile{
			RelPath: SkillPath(s),
			Tier:    core.OwnerUser,
			src:     "templates/skills/" + s + "/SKILL.md",
		})
	}
	// Knowledge skeletons ([user], optional): materialized by adopt stage 0 or
	// MaterializeKnowledge, never at init.
	for _, rel := range knowledgeRelPaths {
		files = append(files, TemplateFile{
			RelPath: rel,
			Tier:    core.OwnerUser,
			src:     "templates/knowledge/" + strings.TrimPrefix(rel, ".blueprint/knowledge/"),
		})
	}
	return files
}

// MaterializeScenarioSkill writes the bundled SKILL.md playbook for a
// scenario class on first use. Scenarios without a bundled playbook are a
// no-op, not an error — new scenarios are allowed to exist before their
// playbook does (DESIGN §5: new scenario = one SKILL.md + one registry row).
func MaterializeScenarioSkill(repoRoot, scenario string) (created bool, err error) {
	rel := SkillPath(scenario)
	if _, ok := templateByRelPath(rel); !ok {
		return false, nil
	}
	return Materialize(repoRoot, rel)
}

// templateByRelPath returns the template for a repo-relative path.
func templateByRelPath(relPath string) (TemplateFile, bool) {
	for _, t := range Templates() {
		if t.RelPath == path.Clean(relPath) {
			return t, true
		}
	}
	return TemplateFile{}, false
}

// Content returns the embedded template bytes.
func (t TemplateFile) Content() ([]byte, error) {
	b, err := templatesFS.ReadFile(t.src)
	if err != nil {
		return nil, fmt.Errorf("install: embedded template %s is missing — this is a build defect, rebuild the binary: %w", t.src, err)
	}
	return b, nil
}

// knowledge.go — the knowledge-store linter (DESIGN §9): per-class freshness
// from `reviewed:` frontmatter, dead relative links, orphan detection
// (unreachable knowledge does not exist), AGENTS.md index caps, relative-date
// ban, and the human-verify budget over living specs. Pure static except for
// the caller-supplied clock (CONTRACTS rule 5: the wall clock is an explicit
// input, never read here).
package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/spec"
)

// Per-class freshness max-ages (DESIGN §9). Warn fires at 80% of the max-age,
// error past it. ADRs are exempt once `status: accepted` — accepted decisions
// are history; a pending one ages like architecture.
const (
	maxAgeArchitecture = 90 * 24 * time.Hour
	maxAgeGlossary     = 180 * 24 * time.Hour
	maxAgeDebt         = 90 * 24 * time.Hour
	maxAgeRunbooks     = 180 * 24 * time.Hour
	maxAgeDefault      = 90 * 24 * time.Hour // pending ADRs + uncategorized knowledge files
)

// AGENTS.md index caps (DESIGN §2 index rules): hard line cap and Codex's
// 32 KiB AGENTS.md budget, both enforced by the same lint.
const (
	AgentsLineCap = 120
	AgentsByteCap = 32 * 1024
)

// DefaultHumanVerifyBudget caps `verify: human` escape hatches across all
// living specs; override via [lint] human_verify_budget in config.toml.
const DefaultHumanVerifyBudget = 5

func (c Config) humanVerifyBudget() int {
	if c.HumanVerifyBudget == nil {
		return DefaultHumanVerifyBudget
	}
	return *c.HumanVerifyBudget
}

const knowledgeRel = ".blueprint/knowledge"

// mdLink matches one inline markdown link and captures its target.
var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

// Knowledge lints the knowledge store plus the AGENTS.md index. now is the
// explicit clock for freshness; findings only — an error return means the
// repo itself could not be walked.
func Knowledge(repoRoot string, now time.Time, cfg Config) ([]core.LintFinding, error) {
	var findings []core.LintFinding
	relDates := compileWords(cfg.relativeDateWords())

	files, err := listKnowledgeFiles(repoRoot)
	if err != nil {
		return nil, err
	}
	docs := map[string]string{} // repo-relative slash path -> content
	for _, rel := range files {
		docs[rel] = strings.ReplaceAll(readFileOrEmpty(filepath.Join(repoRoot, filepath.FromSlash(rel))), "\r\n", "\n")
	}

	// AGENTS.md caps + relative dates. A missing index is stage-1/doctor
	// territory, not a knowledge finding.
	agents := strings.ReplaceAll(readFileOrEmpty(filepath.Join(repoRoot, "AGENTS.md")), "\r\n", "\n")
	if agents != "" {
		findings = append(findings, lintAgentsCaps(agents)...)
		findings = append(findings, scanWords("AGENTS.md", agents, relDates, "relative-date",
			"Replace the relative date with an absolute date (YYYY-MM-DD); the index must stay true when read later.")...)
	}

	for _, rel := range files {
		findings = append(findings, lintKnowledgeFreshness(rel, docs[rel], now)...)
		findings = append(findings, scanWords(rel, docs[rel], relDates, "relative-date",
			"Replace the relative date with an absolute date (YYYY-MM-DD); knowledge must stay true when read later.")...)
		findings = append(findings, lintDeadLinks(repoRoot, rel, docs[rel])...)
	}
	if agents != "" {
		findings = append(findings, lintDeadLinks(repoRoot, "AGENTS.md", agents)...)
	}

	findings = append(findings, lintOrphans(repoRoot, files, docs, agents)...)

	budgetFindings, err := lintHumanVerifyBudget(repoRoot, cfg)
	if err != nil {
		return nil, err
	}
	findings = append(findings, budgetFindings...)
	return findings, nil
}

// listKnowledgeFiles returns every .md under .blueprint/knowledge/ as sorted
// repo-relative slash paths; a missing store is empty, not an error.
func listKnowledgeFiles(repoRoot string) ([]string, error) {
	root := filepath.Join(repoRoot, filepath.FromSlash(knowledgeRel))
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			files = append(files, relPath(repoRoot, path))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot walk %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

func lintAgentsCaps(content string) []core.LintFinding {
	var out []core.LintFinding
	lines := strings.Count(strings.TrimSuffix(content, "\n"), "\n") + 1
	if lines > AgentsLineCap {
		out = append(out, finding("AGENTS.md", AgentsLineCap+1, "agents-line-cap", SevError,
			fmt.Sprintf("AGENTS.md is %d lines (hard cap %d).", lines, AgentsLineCap),
			fmt.Sprintf("Curate the index down to <=%d lines — it is a table of contents, not an encyclopedia; move detail into .blueprint/knowledge/ and point at it.", AgentsLineCap)))
	}
	if n := len(content); n > AgentsByteCap {
		out = append(out, finding("AGENTS.md", 1, "agents-size-cap", SevError,
			fmt.Sprintf("AGENTS.md is %d bytes (cap %d — the 32 KiB Codex AGENTS.md budget).", n, AgentsByteCap),
			"Shrink the index below 32 KiB: replace inlined detail with pointers into .blueprint/knowledge/."))
	}
	return out
}

// knowledgeFM is the parsed frontmatter of one knowledge file. Values are
// taken verbatim with trailing `# comments` stripped.
type knowledgeFM struct {
	found    bool
	reviewed string
	status   string
	line     int // 1-based line of `reviewed:`; 0 when absent
}

func parseKnowledgeFM(content string) knowledgeFM {
	if !strings.HasPrefix(content, "---\n") {
		return knowledgeFM{}
	}
	fm := knowledgeFM{}
	for i, l := range strings.Split(content, "\n")[1:] {
		if strings.TrimSpace(l) == "---" {
			fm.found = true
			break
		}
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		if idx := strings.Index(val, "#"); idx >= 0 {
			val = val[:idx]
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "reviewed":
			fm.reviewed = val
			fm.line = i + 2
		case "status":
			fm.status = val
		}
	}
	if !fm.found {
		return knowledgeFM{}
	}
	return fm
}

// knowledgeMaxAge maps a knowledge file to its freshness class.
func knowledgeMaxAge(rel string) (time.Duration, string) {
	inner := strings.TrimPrefix(rel, knowledgeRel+"/")
	switch {
	case inner == "architecture.md":
		return maxAgeArchitecture, "architecture (90d)"
	case inner == "glossary.md":
		return maxAgeGlossary, "glossary (180d)"
	case inner == "debt.md":
		return maxAgeDebt, "debt (90d)"
	case strings.HasPrefix(inner, "runbooks/"):
		return maxAgeRunbooks, "runbooks (180d)"
	default:
		return maxAgeDefault, "knowledge default (90d)"
	}
}

func lintKnowledgeFreshness(rel, content string, now time.Time) []core.LintFinding {
	fm := parseKnowledgeFM(content)
	if !fm.found {
		return []core.LintFinding{finding(rel, 1, "knowledge-frontmatter", SevError,
			"file has no `---` YAML frontmatter block.",
			"Start the file with a frontmatter block carrying `reviewed: YYYY-MM-DD`, `owner:`, and `status:` — freshness is lint-enforced per class.")}
	}
	// A blank ADR form is scaffolding, not knowledge; accepted ADRs are
	// history. Neither ages.
	isADR := strings.HasPrefix(strings.TrimPrefix(rel, knowledgeRel+"/"), "decisions/")
	if fm.status == "template" || (isADR && fm.status == "accepted") {
		return nil
	}
	if fm.reviewed == "" {
		return []core.LintFinding{finding(rel, max(fm.line, 1), "knowledge-frontmatter", SevError,
			"frontmatter has no `reviewed:` date.",
			"Curate the file, then set `reviewed: YYYY-MM-DD` (the curation date) in the frontmatter; stale-by-unknown is treated as stale.")}
	}
	reviewed, err := time.Parse("2006-01-02", fm.reviewed)
	if err != nil {
		return []core.LintFinding{finding(rel, fm.line, "knowledge-frontmatter", SevError,
			fmt.Sprintf("`reviewed: %s` is not an absolute YYYY-MM-DD date.", fm.reviewed),
			"Write the reviewed date as YYYY-MM-DD (e.g. 2026-07-22).")}
	}
	maxAge, class := knowledgeMaxAge(rel)
	age := now.Sub(reviewed)
	switch {
	case age > maxAge:
		return []core.LintFinding{finding(rel, fm.line, "knowledge-stale", SevError,
			fmt.Sprintf("reviewed %s — %d days ago, past the %s max-age.", fm.reviewed, int(age.Hours()/24), class),
			"Re-read the file, fix what drifted (or delete what no longer earns its place), and update `reviewed:` to the curation date.")}
	case age >= time.Duration(float64(maxAge)*0.8):
		return []core.LintFinding{finding(rel, fm.line, "knowledge-stale", SevWarning,
			fmt.Sprintf("reviewed %s — %d days ago, past 80%% of the %s max-age.", fm.reviewed, int(age.Hours()/24), class),
			"Schedule a curation pass before the max-age turns this into an error; update `reviewed:` when done.")}
	}
	return nil
}

// lintDeadLinks reports relative markdown links in rel that resolve neither
// against the file's directory nor against the repo root.
func lintDeadLinks(repoRoot, rel, content string) []core.LintFinding {
	var out []core.LintFinding
	dir := filepath.Dir(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	for i, l := range strings.Split(content, "\n") {
		for _, m := range mdLink.FindAllStringSubmatch(l, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			if target == "" {
				continue
			}
			if resolveLink(repoRoot, dir, target) == "" {
				out = append(out, finding(rel, i+1, "knowledge-dead-link", SevError,
					fmt.Sprintf("link target %q does not exist.", target),
					"Fix or delete the link — every pointer must resolve, or agents follow it into nothing."))
			}
		}
	}
	return out
}

// resolveLink resolves a relative link target against the source file's
// directory first, then the repo root; "" means dead.
func resolveLink(repoRoot, sourceDir, target string) string {
	t := filepath.FromSlash(target)
	for _, base := range []string{sourceDir, repoRoot} {
		p := filepath.Join(base, t)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// lintOrphans flags knowledge files reachable from neither AGENTS.md nor
// another knowledge file (DESIGN §9: unreachable knowledge does not exist).
// A file counts as reachable when its repo-relative path appears verbatim in
// a source, or a markdown link in a source resolves to it.
func lintOrphans(repoRoot string, files []string, docs map[string]string, agents string) []core.LintFinding {
	type source struct {
		rel     string
		dir     string
		content string
	}
	var sources []source
	if agents != "" {
		sources = append(sources, source{rel: "AGENTS.md", dir: repoRoot, content: agents})
	}
	for _, rel := range files {
		sources = append(sources, source{
			rel:     rel,
			dir:     filepath.Dir(filepath.Join(repoRoot, filepath.FromSlash(rel))),
			content: docs[rel],
		})
	}
	// abs path -> repo-relative slash path, for link resolution.
	absOf := map[string]string{}
	for _, rel := range files {
		absOf[filepath.Join(repoRoot, filepath.FromSlash(rel))] = rel
	}
	reachable := map[string]bool{}
	for _, s := range sources {
		for _, rel := range files {
			if rel != s.rel && strings.Contains(s.content, rel) {
				reachable[rel] = true
			}
		}
		for _, m := range mdLink.FindAllStringSubmatch(s.content, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if target = strings.SplitN(target, "#", 2)[0]; target == "" {
				continue
			}
			if rel, ok := absOf[resolveLink(repoRoot, s.dir, target)]; ok && rel != s.rel {
				reachable[rel] = true
			}
		}
	}
	var out []core.LintFinding
	for _, rel := range files {
		if !reachable[rel] {
			out = append(out, finding(rel, 1, "knowledge-orphan", SevError,
				"no path from AGENTS.md or any other knowledge file reaches this file — unreachable knowledge does not exist.",
				fmt.Sprintf("Link %s from AGENTS.md or a related knowledge doc, or delete it — knowledge nobody can find is dead weight.", rel)))
		}
	}
	return out
}

// lintHumanVerifyBudget counts `verify: human` escape hatches across all
// living specs against the configured budget. Unparseable specs are skipped —
// the spec linter owns reporting those.
func lintHumanVerifyBudget(repoRoot string, cfg Config) ([]core.LintFinding, error) {
	areas, err := spec.ListSpecs(repoRoot)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, area := range areas {
		s, err := spec.LoadSpec(repoRoot, area)
		if err != nil {
			continue
		}
		for _, r := range s.Requirements {
			for _, v := range r.Verify {
				if v.Kind == "human" {
					count++
				}
			}
		}
	}
	budget := cfg.humanVerifyBudget()
	if count > budget {
		return []core.LintFinding{finding(".blueprint/specs", 0, "human-verify-budget", SevError,
			fmt.Sprintf("living specs use %d `verify: human` methods (budget %d).", count, budget),
			"Convert human: entries to test/check/bench where a machine can settle the question, or raise `human_verify_budget` under [lint] in .blueprint/config.toml with a reviewed justification.")}, nil
	}
	return nil, nil
}

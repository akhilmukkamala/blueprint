package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"blueprint/internal/core"
	"blueprint/internal/spec"
)

// Spec runs the deterministic spec linter over every living spec and every
// open change file under repoRoot (DESIGN §3 + §6 task-quality lint).
// Findings only — an error return means the repo itself could not be walked.
func Spec(repoRoot string, cfg Config) ([]core.LintFinding, error) {
	var findings []core.LintFinding
	banned := compileWords(cfg.bannedWords())
	relDates := compileWords(cfg.relativeDateWords())

	// livingIDs: REQ ID -> relative file, for duplicate + dangling checks.
	livingIDs := map[string]string{}

	areas, err := spec.ListSpecs(repoRoot)
	if err != nil {
		return nil, err
	}
	for _, area := range areas {
		rel := relPath(repoRoot, filepath.Join(repoRoot, ".blueprint", "specs", area, "spec.md"))
		content := readFileOrEmpty(filepath.Join(repoRoot, ".blueprint", "specs", area, "spec.md"))
		s, err := spec.LoadSpec(repoRoot, area)
		if err != nil {
			findings = append(findings, finding(rel, 0, "spec-parse", SevError,
				err.Error(),
				"Fix the file so it parses; the message names the exact malformed construct."))
			continue
		}
		if !validSpecStatus[string(s.Status)] {
			findings = append(findings, finding(rel, lineOf(content, "status:"), "spec-status", SevError,
				fmt.Sprintf("living spec %q has status %q.", area, s.Status),
				"Set frontmatter status to one of draft|approved|verified|archived."))
		}
		for _, r := range s.Requirements {
			line := lineOf(content, r.ID)
			if prev, dup := livingIDs[r.ID]; dup {
				findings = append(findings, finding(rel, line, "req-id-duplicate", SevError,
					fmt.Sprintf("%s is already defined in %s; REQ IDs are stable and never reused.", r.ID, prev),
					"Give this requirement the next unused number for its area."))
			} else {
				livingIDs[r.ID] = rel
			}
			if a := spec.AreaOf(r.ID); a != area {
				findings = append(findings, finding(rel, line, "req-id-area", SevError,
					fmt.Sprintf("%s declares area %q but lives in specs/%s/spec.md.", r.ID, a, area),
					fmt.Sprintf("Move the requirement to specs/%s/spec.md or renumber it as REQ-%s-NNN.", a, area)))
			}
			// relDates nil: the whole-file scan below covers requirement
			// text too; passing the list twice would double-report.
			findings = append(findings, lintRequirement(rel, line, r, banned, nil)...)
		}
		// Relative dates anywhere in the file (frontmatter included).
		findings = append(findings, scanWords(rel, content, relDates, "relative-date",
			"Replace the relative date with an absolute date (YYYY-MM-DD); files must stay true when read later.")...)
	}

	ids, err := spec.ListChanges(repoRoot)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		path := spec.ChangePath(repoRoot, id)
		rel := relPath(repoRoot, path)
		content := readFileOrEmpty(path)
		c, err := spec.LoadChange(repoRoot, id)
		if err != nil {
			findings = append(findings, finding(rel, 0, "change-parse", SevError,
				err.Error(),
				"Fix the file so it parses; the message names the exact malformed construct."))
			continue
		}
		findings = append(findings, lintDelta(rel, content, c, livingIDs, banned, relDates)...)
		findings = append(findings, lintTasks(rel, content, c)...)
	}
	return findings, nil
}

// lintRequirement checks one requirement (living or delta ADDED/MODIFIED).
func lintRequirement(file string, line int, r core.Requirement, banned, relDates []wordMatcher) []core.LintFinding {
	var out []core.LintFinding
	if r.Pattern == "" {
		out = append(out, finding(file, line, "ears-pattern-missing", SevError,
			fmt.Sprintf("%s declares no EARS pattern.", r.ID),
			fmt.Sprintf("Write the heading as `### %s (<pattern>)` with pattern one of ubiquitous|event-driven|state-driven|optional|unwanted|complex.", r.ID)))
	} else if ok, want := earsConforms(r.Pattern, r.Text); !ok && want == "" {
		out = append(out, finding(file, line, "ears-pattern-unknown", SevError,
			fmt.Sprintf("%s declares unknown EARS pattern %q.", r.ID, r.Pattern),
			"Use one of ubiquitous|event-driven|state-driven|optional|unwanted|complex."))
	} else if !ok {
		out = append(out, finding(file, line, "ears-conformance", SevError,
			fmt.Sprintf("%s text does not follow its declared %q pattern.", r.ID, r.Pattern),
			fmt.Sprintf("Rewrite the text as %s, or declare the pattern that matches the text.", want)))
	}
	if len(r.Verify) == 0 {
		out = append(out, finding(file, line, "verify-missing", SevError,
			fmt.Sprintf("%s has no verify: block — a requirement without a machine-settleable verification method is incomplete (AC-5).", r.ID),
			"Add a `verify:` block with one or more of `- test: <id>`, `- check: <verifier>`, `- bench: <threshold-file>`, `- human: <question>`."))
	}
	for _, v := range r.Verify {
		if !validVerifyKinds[v.Kind] {
			out = append(out, finding(file, line, "verify-kind", SevError,
				fmt.Sprintf("%s verify entry uses unknown kind %q.", r.ID, v.Kind),
				"Use one of test|check|bench|human."))
		}
		if v.Kind == "human" {
			out = append(out, finding(file, line, "verify-human", SevWarning,
				fmt.Sprintf("%s relies on human verification (%q).", r.ID, v.Ref),
				"human: is an escape hatch — it always triggers a human gate and counts against the lint budget; prefer test/check/bench where possible."))
		}
	}
	for _, m := range banned {
		if m.re.MatchString(r.Text) {
			out = append(out, finding(file, line, "vague-word", SevError,
				fmt.Sprintf("%s uses the vague word %q (INCOSE-R7/NASA-ARM ban list).", r.ID, m.word),
				fmt.Sprintf("Replace %q with a measurable, testable criterion (a number, threshold, or enumerated behavior).", m.word)))
		}
	}
	for _, m := range relDates {
		if m.re.MatchString(r.Text) {
			out = append(out, finding(file, line, "relative-date", SevError,
				fmt.Sprintf("%s uses the relative date word %q.", r.ID, m.word),
				"Replace the relative date with an absolute date (YYYY-MM-DD)."))
		}
	}
	if n := sentenceCount(r.Text); n > maxSentences {
		out = append(out, finding(file, line, "sentence-budget", SevWarning,
			fmt.Sprintf("%s runs %d sentences (budget %d).", r.ID, n, maxSentences),
			"Split the requirement: one requirement per behavior, each independently verifiable."))
	}
	return out
}

// lintDelta checks a change's ADDED/MODIFIED/REMOVED entries against the
// living-spec ID set: no reuse on ADDED, no dangling on MODIFIED/REMOVED.
func lintDelta(file, content string, c *core.Change, livingIDs map[string]string, banned, relDates []wordMatcher) []core.LintFinding {
	var out []core.LintFinding
	seen := map[string]bool{}
	for _, d := range c.Delta {
		r := d.Requirement
		line := lineOf(content, r.ID)
		if seen[r.ID] {
			out = append(out, finding(file, line, "req-id-duplicate", SevError,
				fmt.Sprintf("%s appears in more than one delta entry.", r.ID),
				"Collapse the entries into one ADDED/MODIFIED/REMOVED operation per REQ ID."))
			continue
		}
		seen[r.ID] = true
		switch d.Op {
		case core.DeltaAdded:
			if prev, exists := livingIDs[r.ID]; exists {
				out = append(out, finding(file, line, "req-id-reused", SevError,
					fmt.Sprintf("ADDED %s already exists in %s; REQ IDs are never reused.", r.ID, prev),
					"Change the op to MODIFIED, or pick the next unused number for the area."))
			}
			out = append(out, lintRequirement(file, line, r, banned, relDates)...)
		case core.DeltaModified:
			if _, exists := livingIDs[r.ID]; !exists {
				out = append(out, finding(file, line, "req-id-dangling", SevError,
					fmt.Sprintf("MODIFIED %s does not exist in any living spec.", r.ID),
					"Change the op to ADDED, or fix the REQ ID to match the requirement being modified."))
			}
			out = append(out, lintRequirement(file, line, r, banned, relDates)...)
		case core.DeltaRemoved:
			if _, exists := livingIDs[r.ID]; !exists {
				out = append(out, finding(file, line, "req-id-dangling", SevError,
					fmt.Sprintf("REMOVED %s does not exist in any living spec.", r.ID),
					"Drop this delta entry, or fix the REQ ID."))
			}
		}
	}
	return out
}

// Placeholder-task patterns (superpowers' plan contract, DESIGN §6): tasks
// that defer the actual work are banned.
var placeholderTaskRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bTBD\b`),
	regexp.MustCompile(`(?i)\bTODO\b`),
	regexp.MustCompile(`(?i)^add error handling\b`),
	regexp.MustCompile(`(?i)\bsimilar to task\s+\w+`),
	regexp.MustCompile(`(?i)^(fix|handle|improve|clean ?up) (things|stuff|issues|errors)\b`),
	regexp.MustCompile(`(?i)^etc\.?$`),
}

// lintTasks enforces task quality: no placeholders; full-tier tasks declare
// Consumes/Produces so subagent dispatch is file-handoff (DESIGN §6).
func lintTasks(file, content string, c *core.Change) []core.LintFinding {
	var out []core.LintFinding
	for _, t := range c.Tasks {
		line := lineOf(content, t.Text)
		for _, re := range placeholderTaskRes {
			if re.MatchString(t.Text) {
				out = append(out, finding(file, line, "task-placeholder", SevError,
					fmt.Sprintf("task %s (%q) is a placeholder.", t.ID, t.Text),
					"Replace it with a concrete, independently completable step naming the files or behavior it changes."))
				break
			}
		}
		if c.Tier == core.TierFull {
			if len(t.Consumes) == 0 || len(t.Produces) == 0 {
				out = append(out, finding(file, line, "task-handoff", SevError,
					fmt.Sprintf("full-tier task %s declares no Consumes/Produces.", t.ID),
					"Add `  - Consumes: <input files>` and `  - Produces: <output files>` under the task so dispatch is file-handoff, never pasted history."))
			}
		}
	}
	return out
}

// scanWords emits one finding per banned word found anywhere in content,
// reported at the first matching line.
func scanWords(file, content string, words []wordMatcher, rule, remedy string) []core.LintFinding {
	var out []core.LintFinding
	for i, l := range strings.Split(content, "\n") {
		for _, m := range words {
			if m.re.MatchString(l) {
				out = append(out, finding(file, i+1, rule, SevError,
					fmt.Sprintf("line uses the banned word %q.", m.word), remedy))
			}
		}
	}
	return out
}

func readFileOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// relPath renders path relative to repoRoot with forward slashes so findings
// are stable across platforms (Windows-clean output).
func relPath(repoRoot, path string) string {
	r, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(r)
}

// adopt.go — per-stage adoption exit checks (DESIGN §14): each ADOPT.md stage
// ends at `blueprint doctor --adopt-stage <n>`. Doctor checks exactly the
// requested stage's exit criteria (stages are stop-anywhere; earlier stages
// were already gated when the adopter passed them).
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/lint"
)

func adoptStageChecks(repoRoot string, opts Options) ([]Check, error) {
	switch opts.AdoptStage {
	case 0:
		return stage0(repoRoot), nil
	case 1:
		return stage1(repoRoot), nil
	case 2:
		return stage2(opts), nil
	case 3:
		return stage3(repoRoot)
	case 4:
		return stage4(repoRoot), nil
	default:
		return nil, fmt.Errorf("adopt stage %d does not exist — stages are 0 through 4 (see ADOPT.md); run `blueprint doctor --adopt-stage <n>`", opts.AdoptStage)
	}
}

// Stage 0 exit: manifest + baselines exist (`blueprint adopt` wrote them).
func stage0(repoRoot string) []Check {
	return []Check{
		fileExists(repoRoot, "adopt-0-manifest", filepath.Join(".blueprint", "manifest.json"),
			"run `blueprint adopt` — it writes the install manifest (version + per-file hashes + ownership tiers)"),
		fileExists(repoRoot, "adopt-0-baselines", filepath.Join(".blueprint", "baselines.json"),
			"run `blueprint adopt` — it captures framework-off baselines (task timing + trailing-90-day rework rates); without them AC-1 has nothing to compare against"),
	}
}

// Stage 1 exit: AGENTS.md lint green (<=120 lines, pointers resolve) +
// dev-env runbook exists.
func stage1(repoRoot string) []Check {
	checks := []Check{agentsLint(repoRoot)}
	checks = append(checks, fileExists(repoRoot, "adopt-1-dev-env-runbook",
		filepath.Join(".blueprint", "knowledge", "runbooks", "dev-env.md"),
		"record the dev-environment command in .blueprint/knowledge/runbooks/dev-env.md (stage 1 deliverable)"))
	return checks
}

const agentsLineCap = 120

var mdLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

// agentsLint enforces the index rules doctor can settle mechanically:
// hard line cap and pointer resolution (DESIGN §2 index rules).
func agentsLint(repoRoot string) Check {
	path := filepath.Join(repoRoot, "AGENTS.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "adopt-1-agents-lint", Pass: false,
			Detail:      "AGENTS.md missing at the repo root",
			Remediation: "draft AGENTS.md (repo probe + git-churn mining gives a draft; a human curates it to <=120 lines) — it is THE canonical index"}
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
	if lines > agentsLineCap {
		return Check{Name: "adopt-1-agents-lint", Pass: false,
			Detail:      fmt.Sprintf("AGENTS.md is %d lines (cap %d)", lines, agentsLineCap),
			Remediation: fmt.Sprintf("curate AGENTS.md down to <=%d lines — it is a table of contents, not an encyclopedia; move detail into .blueprint/knowledge/ and point at it", agentsLineCap)}
	}
	var broken []string
	for _, m := range mdLinkRe.FindAllStringSubmatch(text, -1) {
		target := m[1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		target = strings.SplitN(target, "#", 2)[0]
		if target == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(target))); err != nil {
			broken = append(broken, target)
		}
	}
	if len(broken) > 0 {
		return Check{Name: "adopt-1-agents-lint", Pass: false,
			Detail:      fmt.Sprintf("%d pointer(s) do not resolve: %s", len(broken), strings.Join(broken, ", ")),
			Remediation: "fix or delete the broken links — every pointer in the index must resolve, or agents follow it into nothing"}
	}
	return Check{Name: "adopt-1-agents-lint", Pass: true,
		Detail: fmt.Sprintf("AGENTS.md: %d/%d lines, all pointers resolve", lines, agentsLineCap)}
}

// Stage 2 exit: verify runs as a command — the loop predicate `blueprint
// verify <id>` must be executable, which means the binary resolves on PATH.
func stage2(opts Options) []Check {
	if _, err := opts.lookPath("blueprint"); err != nil {
		return []Check{{Name: "adopt-2-verify-command", Pass: false,
			Detail:      "`blueprint` does not resolve on PATH, so `blueprint verify` cannot run as a predicate or CI check",
			Remediation: "install the blueprint binary on PATH (CI runners included) — stage 2's exit is verify running as a real command, not a docs promise"}}
	}
	return []Check{{Name: "adopt-2-verify-command", Pass: true,
		Detail: "`blueprint` resolves on PATH; `blueprint verify` is executable as a predicate/CI check"}}
}

// Stage 3 exit: at least one archived change closed with a green verdict —
// the first routed change made it through the whole lifecycle.
func stage3(repoRoot string) ([]Check, error) {
	archive := filepath.Join(repoRoot, ".blueprint", "archive")
	entries, err := os.ReadDir(archive)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot list %s: %w", archive, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(archive, e.Name(), "verdict", "verdict.json"))
		if err != nil {
			continue
		}
		var v core.Verdict
		if json.Unmarshal(raw, &v) == nil && v.Pass && !v.Tamper {
			return []Check{{Name: "adopt-3-verified-change", Pass: true,
				Detail: fmt.Sprintf("archived change %s closed with a green verdict", e.Name())}}, nil
		}
	}
	return []Check{{Name: "adopt-3-verified-change", Pass: false,
		Detail:      "no archived change with a green verdict",
		Remediation: "route one real bug or chore through `blueprint new`, get `blueprint verify` green, and `blueprint close` it — stage 3 exits on a merged verifier-green change, not on setup"}}, nil
}

// knowledgeLintNow is the clock the stage-4 knowledge lint sees; injected in
// tests (freshness is a function of an explicit clock, CONTRACTS rule 5).
var knowledgeLintNow = time.Now

// Stage 4 exit: tribal capture produced at least one glossary, one runbook,
// and one ADR — and the knowledge store passes the real knowledge lint
// (freshness, dead links, orphans, index caps), not an existence-only check.
func stage4(repoRoot string) []Check {
	kn := filepath.Join(repoRoot, ".blueprint", "knowledge")
	checks := []Check{
		fileExists(repoRoot, "adopt-4-glossary", filepath.Join(".blueprint", "knowledge", "glossary.md"),
			"capture domain terms from the tribal-knowledge interviews into .blueprint/knowledge/glossary.md"),
		dirHasMarkdown("adopt-4-runbook", filepath.Join(kn, "runbooks"),
			"write at least one runbook (run-it-first, deploy, debug) under .blueprint/knowledge/runbooks/"),
		dirHasMarkdown("adopt-4-adr", filepath.Join(kn, "decisions"),
			"backfill at least one one-page ADR under .blueprint/knowledge/decisions/"),
		knowledgeLintCheck(repoRoot),
	}
	return checks
}

// knowledgeLintCheck runs lint.Knowledge and fails on any error-severity
// finding, surfacing the first finding's remediation verbatim.
func knowledgeLintCheck(repoRoot string) Check {
	findings, err := lint.Knowledge(repoRoot, knowledgeLintNow().UTC(), lint.Config{})
	if err != nil {
		return Check{Name: "adopt-4-knowledge-lint", Pass: false,
			Detail:      err.Error(),
			Remediation: "fix the walk error above, then re-run `blueprint doctor --adopt-stage 4`"}
	}
	errs, warns := 0, 0
	var first *core.LintFinding
	for i, f := range findings {
		if f.Severity == lint.SevError {
			if first == nil {
				first = &findings[i]
			}
			errs++
		} else {
			warns++
		}
	}
	if errs > 0 {
		return Check{Name: "adopt-4-knowledge-lint", Pass: false,
			Detail:      fmt.Sprintf("`blueprint lint knowledge` has %d error(s), %d warning(s); first: %s: %s", errs, warns, first.File, first.Message),
			Remediation: first.Remediation + " — then re-run `blueprint lint knowledge` until zero errors"}
	}
	return Check{Name: "adopt-4-knowledge-lint", Pass: true,
		Detail: fmt.Sprintf("knowledge lint green (%d warning(s))", warns)}
}

func fileExists(repoRoot, name, rel, remedy string) Check {
	if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
		return Check{Name: name, Pass: false, Detail: rel + " missing", Remediation: remedy}
	}
	return Check{Name: name, Pass: true, Detail: rel + " exists"}
}

func dirHasMarkdown(name, dir, remedy string) Check {
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				return Check{Name: name, Pass: true,
					Detail: fmt.Sprintf("%s present (e.g. %s)", dir, e.Name())}
			}
		}
	}
	return Check{Name: name, Pass: false, Detail: dir + " has no .md files", Remediation: remedy}
}

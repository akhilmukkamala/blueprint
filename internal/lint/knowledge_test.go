package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
)

// knNow is the fixed clock every knowledge test lints against.
var knNow = time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)

func knWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// knFM builds frontmatter with the given reviewed date and status.
func knFM(reviewed, status string) string {
	return "---\nreviewed: " + reviewed + "\nowner: alice\nstatus: " + status + "\n---\n\n"
}

func rulesFor(findings []core.LintFinding, file string) map[string]string {
	out := map[string]string{}
	for _, f := range findings {
		if f.File == file {
			out[f.Rule] = f.Severity
		}
	}
	return out
}

func mustKnowledge(t *testing.T, root string, cfg Config) []core.LintFinding {
	t.Helper()
	findings, err := Knowledge(root, knNow, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return findings
}

// linkAll writes an AGENTS.md that reaches every listed knowledge file so
// orphan findings never leak into unrelated assertions.
func linkAll(t *testing.T, root string, rels ...string) {
	var b strings.Builder
	b.WriteString("# index\n\n")
	for _, r := range rels {
		b.WriteString("- `" + r + "`\n")
	}
	knWrite(t, root, "AGENTS.md", b.String())
}

func TestKnowledgeFreshnessPerClass(t *testing.T) {
	cases := []struct {
		name     string
		rel      string
		reviewed string // "" = omit frontmatter entirely
		status   string
		wantRule string
		wantSev  string
	}{
		{"architecture fresh", ".blueprint/knowledge/architecture.md", "2026-07-01", "draft", "", ""},
		// 90d class: warn window opens at 72d. 75 days before knNow = 2026-05-08.
		{"architecture warn at 80%", ".blueprint/knowledge/architecture.md", "2026-05-08", "draft", "knowledge-stale", SevWarning},
		// 100 days old = 2026-04-13.
		{"architecture stale", ".blueprint/knowledge/architecture.md", "2026-04-13", "draft", "knowledge-stale", SevError},
		// 100 days is fine for the 180d glossary class.
		{"glossary 180d class", ".blueprint/knowledge/glossary.md", "2026-04-13", "draft", "", ""},
		{"glossary stale past 180d", ".blueprint/knowledge/glossary.md", "2025-06-01", "draft", "knowledge-stale", SevError},
		{"runbook 180d class", ".blueprint/knowledge/runbooks/deploy.md", "2026-04-13", "draft", "", ""},
		{"debt 90d class stale", ".blueprint/knowledge/debt.md", "2026-04-13", "draft", "knowledge-stale", SevError},
		{"pending ADR ages", ".blueprint/knowledge/decisions/0001-x.md", "2026-04-13", "proposed", "knowledge-stale", SevError},
		{"accepted ADR exempt", ".blueprint/knowledge/decisions/0001-x.md", "2020-01-01", "accepted", "", ""},
		{"template form exempt", ".blueprint/knowledge/decisions/ADR-0000-template.md", "", "template", "", ""},
		{"missing reviewed", ".blueprint/knowledge/architecture.md", "", "draft", "knowledge-frontmatter", SevError},
		{"bad date", ".blueprint/knowledge/architecture.md", "last summer", "draft", "knowledge-frontmatter", SevError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			fm := "---\nowner: alice\nstatus: " + tc.status + "\n---\n\n"
			if tc.reviewed != "" {
				fm = knFM(tc.reviewed, tc.status)
			}
			knWrite(t, root, tc.rel, fm+"# doc\n")
			linkAll(t, root, tc.rel)
			rules := rulesFor(mustKnowledge(t, root, Config{}), tc.rel)
			if tc.wantRule == "" {
				if len(rules) != 0 {
					t.Fatalf("want no findings, got %v", rules)
				}
				return
			}
			if sev, ok := rules[tc.wantRule]; !ok || sev != tc.wantSev {
				t.Fatalf("want %s/%s, got %v", tc.wantRule, tc.wantSev, rules)
			}
		})
	}
}

func TestKnowledgeMissingFrontmatterBlock(t *testing.T) {
	root := t.TempDir()
	knWrite(t, root, ".blueprint/knowledge/glossary.md", "# terms, no frontmatter\n")
	linkAll(t, root, ".blueprint/knowledge/glossary.md")
	rules := rulesFor(mustKnowledge(t, root, Config{}), ".blueprint/knowledge/glossary.md")
	if rules["knowledge-frontmatter"] != SevError {
		t.Fatalf("missing frontmatter must be an error, got %v", rules)
	}
}

func TestKnowledgeDeadLinks(t *testing.T) {
	root := t.TempDir()
	knWrite(t, root, ".blueprint/knowledge/architecture.md", knFM("2026-07-01", "draft")+
		"# arch\n\n[gone](missing.md)\n[ok same dir](glossary.md)\n[ok repo-rel](.blueprint/knowledge/glossary.md)\n[ext](https://example.com/x)\n[anchor](#modules)\n")
	knWrite(t, root, ".blueprint/knowledge/glossary.md", knFM("2026-07-01", "draft")+"# terms\n\n[back](architecture.md)\n")
	linkAll(t, root, ".blueprint/knowledge/architecture.md", ".blueprint/knowledge/glossary.md")
	findings := mustKnowledge(t, root, Config{})
	var dead []core.LintFinding
	for _, f := range findings {
		if f.Rule == "knowledge-dead-link" {
			dead = append(dead, f)
		}
	}
	if len(dead) != 1 || !strings.Contains(dead[0].Message, "missing.md") {
		t.Fatalf("want exactly one dead link (missing.md), got %+v", dead)
	}
}

func TestKnowledgeDeadLinkInAgents(t *testing.T) {
	root := t.TempDir()
	knWrite(t, root, "AGENTS.md", "# index\n\n[gone](docs/nope.md)\n")
	findings := mustKnowledge(t, root, Config{})
	if rulesFor(findings, "AGENTS.md")["knowledge-dead-link"] != SevError {
		t.Fatalf("dead AGENTS.md link must be an error, got %+v", findings)
	}
}

func TestKnowledgeOrphans(t *testing.T) {
	root := t.TempDir()
	// linked-by-agents: plain-path mention in AGENTS.md.
	knWrite(t, root, "AGENTS.md", "# index\n\nKnowledge: `.blueprint/knowledge/glossary.md`\n")
	knWrite(t, root, ".blueprint/knowledge/glossary.md", knFM("2026-07-01", "draft")+
		"# terms\n\nSee [runbook](runbooks/dev-env.md).\n")
	// linked-by-knowledge: relative link from glossary.
	knWrite(t, root, ".blueprint/knowledge/runbooks/dev-env.md", knFM("2026-07-01", "draft")+"# dev\n")
	// orphan: nothing reaches it.
	knWrite(t, root, ".blueprint/knowledge/lore.md", knFM("2026-07-01", "draft")+"# lore\n")

	findings := mustKnowledge(t, root, Config{})
	orphans := map[string]bool{}
	for _, f := range findings {
		if f.Rule == "knowledge-orphan" {
			orphans[f.File] = true
		}
	}
	if !orphans[".blueprint/knowledge/lore.md"] || len(orphans) != 1 {
		t.Fatalf("want exactly lore.md orphaned, got %v", orphans)
	}
	// A self-reference must not count as reachability.
	knWrite(t, root, ".blueprint/knowledge/lore.md", knFM("2026-07-01", "draft")+
		"# lore\n\nI am `.blueprint/knowledge/lore.md`.\n")
	findings = mustKnowledge(t, root, Config{})
	if rulesFor(findings, ".blueprint/knowledge/lore.md")["knowledge-orphan"] != SevError {
		t.Fatal("self-reference must not clear the orphan check")
	}
}

func TestAgentsCaps(t *testing.T) {
	root := t.TempDir()
	knWrite(t, root, "AGENTS.md", strings.Repeat("line\n", AgentsLineCap+1))
	rules := rulesFor(mustKnowledge(t, root, Config{}), "AGENTS.md")
	if rules["agents-line-cap"] != SevError {
		t.Fatalf("121 lines must break the line cap, got %v", rules)
	}

	big := "# x\n" + strings.Repeat("a", AgentsByteCap) + "\n"
	knWrite(t, root, "AGENTS.md", big)
	rules = rulesFor(mustKnowledge(t, root, Config{}), "AGENTS.md")
	if rules["agents-size-cap"] != SevError {
		t.Fatalf("oversized AGENTS.md must break the 32 KiB cap, got %v", rules)
	}

	knWrite(t, root, "AGENTS.md", "# index\n\nAll pointers resolve.\n")
	if rules := rulesFor(mustKnowledge(t, root, Config{}), "AGENTS.md"); len(rules) != 0 {
		t.Fatalf("small clean index must pass, got %v", rules)
	}
}

func TestKnowledgeRelativeDates(t *testing.T) {
	root := t.TempDir()
	knWrite(t, root, "AGENTS.md", "# index\n\nUpdated recently.\n")
	knWrite(t, root, ".blueprint/knowledge/glossary.md", knFM("2026-07-01", "draft")+
		"# terms\n\nWe migrated last week.\n")
	findings := mustKnowledge(t, root, Config{})
	if rulesFor(findings, "AGENTS.md")["relative-date"] != SevError {
		t.Fatal("relative date in AGENTS.md must be an error")
	}
	if rulesFor(findings, ".blueprint/knowledge/glossary.md")["relative-date"] != SevError {
		t.Fatal("relative date in a knowledge file must be an error")
	}
}

const knSpecWithHumans = `---
id: pay
status: approved
owner: alice
reviewed: 2026-07-01
---

# pay

### REQ-pay-001 (ubiquitous)

The payment service shall retry a failed capture exactly once.

verify:
- human: q1
- human: q2
- human: q3

### REQ-pay-002 (ubiquitous)

The payment service shall record every capture attempt in the ledger.

verify:
- human: q4
- human: q5
- human: q6
`

func TestHumanVerifyBudget(t *testing.T) {
	root := t.TempDir()
	knWrite(t, root, ".blueprint/specs/pay/spec.md", knSpecWithHumans)

	// 6 human methods > default budget 5.
	findings := mustKnowledge(t, root, Config{})
	if rulesFor(findings, ".blueprint/specs")["human-verify-budget"] != SevError {
		t.Fatalf("6 human verifies must exceed the default budget of %d: %+v", DefaultHumanVerifyBudget, findings)
	}

	// Raising the budget in config clears it; lowering tightens it.
	ten, two := 10, 2
	if f := mustKnowledge(t, root, Config{HumanVerifyBudget: &ten}); len(rulesFor(f, ".blueprint/specs")) != 0 {
		t.Fatalf("budget 10 must clear 6 human verifies, got %+v", f)
	}
	if rulesFor(mustKnowledge(t, root, Config{HumanVerifyBudget: &two}), ".blueprint/specs")["human-verify-budget"] != SevError {
		t.Fatal("budget 2 must flag 6 human verifies")
	}
}

func TestKnowledgeEmptyStore(t *testing.T) {
	root := t.TempDir()
	if findings := mustKnowledge(t, root, Config{}); len(findings) != 0 {
		t.Fatalf("repo without knowledge store or AGENTS.md must lint clean, got %+v", findings)
	}
}

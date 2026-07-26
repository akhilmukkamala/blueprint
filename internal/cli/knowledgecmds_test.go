package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"blueprint/internal/core"
	"blueprint/internal/install"
)

// knowledgeLintJSON runs `lint knowledge --json` (or `lint all --json`) via the
// spec test root — the knowledge lint extends the spec feature's lint command.
func knowledgeLintJSON(t *testing.T, dir, mode string) (struct {
	Findings []core.LintFinding `json:"findings"`
	Errors   int                `json:"errors"`
	Warnings int                `json:"warnings"`
}, error) {
	t.Helper()
	out, err := specRun(t, dir, "lint", mode, "--json")
	var res struct {
		Findings []core.LintFinding `json:"findings"`
		Errors   int                `json:"errors"`
		Warnings int                `json:"warnings"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	return res, err
}

func TestLintKnowledgeMode(t *testing.T) {
	dir := t.TempDir()
	specWriteFile(t, dir, ".blueprint/config.toml", "")
	// The materialized skeletons carry empty reviewed: fields — the knowledge
	// lint must flag them as errors until a human curates.
	if _, err := install.MaterializeKnowledge(dir); err != nil {
		t.Fatal(err)
	}
	res, err := knowledgeLintJSON(t, dir, "knowledge")
	if err == nil {
		t.Fatal("uncurated skeletons must exit non-zero")
	}
	rules := map[string]int{}
	for _, f := range res.Findings {
		rules[f.Rule]++
	}
	if rules["knowledge-frontmatter"] == 0 {
		t.Fatalf("want knowledge-frontmatter errors for empty reviewed: fields, got %v", rules)
	}
	// The skeleton set must be self-consistent otherwise: cross-linked (no
	// orphans), no dead links, no relative-date words.
	for _, r := range []string{"knowledge-orphan", "knowledge-dead-link", "relative-date"} {
		if rules[r] != 0 {
			t.Fatalf("skeleton set must have zero %s findings, got %v: %+v", r, rules, res.Findings)
		}
	}
}

func TestLintKnowledgeCuratedGreenAndFoldedIntoAll(t *testing.T) {
	dir := t.TempDir()
	specWriteFile(t, dir, ".blueprint/config.toml", "")
	fm := "---\nreviewed: 2100-01-01\nowner: alice\nstatus: draft\n---\n\n"
	specWriteFile(t, dir, "AGENTS.md", "# index\n\n- `.blueprint/knowledge/glossary.md`\n")
	specWriteFile(t, dir, ".blueprint/knowledge/glossary.md", fm+"# terms\n")
	if _, err := knowledgeLintJSON(t, dir, "knowledge"); err != nil {
		t.Fatalf("curated store must lint green: %v", err)
	}

	// `lint all` folds knowledge findings in: orphan the file and all fails.
	specWriteFile(t, dir, "AGENTS.md", "# index\n")
	res, err := knowledgeLintJSON(t, dir, "all")
	if err == nil {
		t.Fatal("orphaned knowledge must fail `lint all`")
	}
	found := false
	for _, f := range res.Findings {
		if f.Rule == "knowledge-orphan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("`lint all` must include knowledge findings, got %+v", res.Findings)
	}
}

func TestLintKnowledgeBudgetFromConfig(t *testing.T) {
	dir := t.TempDir()
	spec := `---
id: pay
status: approved
owner: alice
reviewed: 2100-01-01
---

# pay

### REQ-pay-001 (ubiquitous)

The payment service shall retry a failed capture exactly once.

verify:
- human: q1
- human: q2
`
	specWriteFile(t, dir, ".blueprint/specs/pay/spec.md", spec)
	specWriteFile(t, dir, ".blueprint/config.toml", "[lint]\nhuman_verify_budget = 1\n")
	res, err := knowledgeLintJSON(t, dir, "knowledge")
	if err == nil {
		t.Fatal("2 human verifies over a configured budget of 1 must fail")
	}
	if len(res.Findings) != 1 || res.Findings[0].Rule != "human-verify-budget" {
		t.Fatalf("want exactly the budget finding, got %+v", res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, "budget 1") {
		t.Fatalf("finding must name the configured budget: %+v", res.Findings[0])
	}

	specWriteFile(t, dir, ".blueprint/config.toml", "[lint]\nhuman_verify_budget = 2\n")
	if _, err := knowledgeLintJSON(t, dir, "knowledge"); err != nil {
		t.Fatalf("budget 2 must clear 2 human verifies: %v", err)
	}
}

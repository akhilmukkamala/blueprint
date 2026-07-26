package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/autonomy"
	"blueprint/internal/core"
)

// stubTools returns Options wired so git exists, the repo is a work tree,
// origin points at url, and blueprint is on PATH — the all-green baseline
// each test then perturbs.
func stubTools(url string) Options {
	return Options{
		AdoptStage: -1,
		LookPath:   func(file string) (string, error) { return "/fake/bin/" + file, nil },
		Git: func(repoRoot string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true", nil
			case "remote get-url origin":
				if url == "" {
					return "", fmt.Errorf("no such remote")
				}
				return url, nil
			}
			return "", fmt.Errorf("unexpected git args %v", args)
		},
	}
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDevEnvRunbook(t *testing.T, root string) {
	t.Helper()
	writeFileT(t, filepath.Join(root, ".blueprint", "knowledge", "runbooks", "dev-env.md"),
		"# dev env\n\n```sh\ntrue\n```\n")
}

func checkByName(t *testing.T, rep *Report, name string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, rep.Checks)
	return Check{}
}

func TestDetectForge(t *testing.T) {
	cases := []struct {
		url      string
		forge    string
		enforced bool
	}{
		{"git@github.com:acme/app.git", "github", true},
		{"https://github.com/acme/app.git", "github", true},
		{"https://gitea.example.com/acme/app.git", "gitea", true},
		{"https://gitlab.com/acme/app.git", "gitlab", false},
		{"git@bitbucket.org:acme/app.git", "bitbucket", false},
		{"https://dev.azure.com/acme/app/_git/app", "azure", false},
		{"https://acme.visualstudio.com/app/_git/app", "azure", false},
		{"https://code.internal.acme/app.git", "unknown", false},
		{"", "unknown", false},
	}
	for _, tc := range cases {
		p := DetectForge(tc.url)
		if p.Forge != tc.forge || p.Enforced != tc.enforced {
			t.Errorf("DetectForge(%q) = %s/%v, want %s/%v", tc.url, p.Forge, p.Enforced, tc.forge, tc.enforced)
		}
		if p.Notes == "" {
			t.Errorf("DetectForge(%q) has empty notes — every profile must say what to verify manually", tc.url)
		}
	}
}

func TestRunAllGreenWritesProfile(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)

	rep, err := Run(root, stubTools("git@github.com:acme/app.git"))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pass {
		t.Fatalf("all-green fixture failed: %+v", rep.Checks)
	}
	if rep.Profile == nil || rep.Profile.Forge != "github" || !rep.Profile.Enforced {
		t.Fatalf("profile not reported: %+v", rep.Profile)
	}
	// The probe must land in autonomy.json.
	f, err := autonomy.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Profile.Forge != "github" || !f.Profile.Enforced {
		t.Fatalf("profile not persisted to autonomy.json: %+v", f.Profile)
	}
}

func TestRunProfilePreservesClasses(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	if err := autonomy.Save(root, &autonomy.File{
		Profile: core.EnforcementProfile{Forge: "unknown"},
		Classes: map[string]autonomy.ClassState{"bugfix": {Level: core.L2Branch}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, stubTools("https://gitlab.com/acme/app.git")); err != nil {
		t.Fatal(err)
	}
	f, err := autonomy.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Profile.Forge != "gitlab" {
		t.Fatalf("profile not updated: %+v", f.Profile)
	}
	if f.Classes["bugfix"].Level != core.L2Branch {
		t.Fatal("doctor must not clobber ladder state when recording the profile")
	}
}

func TestRunMissingGit(t *testing.T) {
	root := t.TempDir()
	opts := stubTools("")
	opts.LookPath = func(file string) (string, error) { return "", fmt.Errorf("not found") }

	rep, err := Run(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pass {
		t.Fatal("missing git must fail the report")
	}
	c := checkByName(t, rep, "git")
	if c.Pass || c.Remediation == "" {
		t.Fatalf("git check must fail with remediation: %+v", c)
	}
	if f := checkByName(t, rep, "forge"); f.Pass {
		t.Fatal("forge check cannot pass when git failed")
	}
}

func TestRunDevEnvRunbookChecks(t *testing.T) {
	root := t.TempDir()
	opts := stubTools("git@github.com:acme/app.git")

	rep, _ := Run(root, opts)
	if c := checkByName(t, rep, "dev-env-runbook"); c.Pass {
		t.Fatal("missing runbook must fail")
	}

	writeFileT(t, devEnvRunbookPath(root), "# dev env\n\nno code here\n")
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "dev-env-runbook"); c.Pass {
		t.Fatal("runbook without a fenced block must fail")
	}

	writeDevEnvRunbook(t, root)
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "dev-env-runbook"); !c.Pass {
		t.Fatalf("runbook with fenced block must pass without executing: %+v", c)
	}

	// Opt-in execution: `true` exits 0, `exit 3` does not.
	opts.RunDevEnv = true
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "dev-env-runbook"); !c.Pass {
		t.Fatalf("exit-0 block under --run-dev-env must pass: %+v", c)
	}
	writeFileT(t, devEnvRunbookPath(root), "```sh\nexit 3\n```\n")
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "dev-env-runbook"); c.Pass {
		t.Fatal("non-zero block under --run-dev-env must fail")
	}
}

func TestHooksLiveness(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	opts := stubTools("git@github.com:acme/app.git")
	settings := filepath.Join(root, ".claude", "settings.json")

	// No settings file: vacuous pass.
	rep, _ := Run(root, opts)
	if c := checkByName(t, rep, "hooks-liveness"); !c.Pass {
		t.Fatalf("absent settings must pass: %+v", c)
	}

	// Declared hook whose script file is missing: the superpowers scar.
	writeFileT(t, settings, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"$CLAUDE_PROJECT_DIR/.claude/hooks/guard.sh --strict"}]}]}}`)
	rep, _ = Run(root, opts)
	c := checkByName(t, rep, "hooks-liveness")
	if c.Pass {
		t.Fatal("hook referencing a missing script must fail")
	}
	if !strings.Contains(c.Detail, "guard.sh") {
		t.Errorf("detail should name the dead file, got %q", c.Detail)
	}

	// Restore the script: check goes green.
	writeFileT(t, filepath.Join(root, ".claude", "hooks", "guard.sh"), "#!/bin/sh\nexit 0\n")
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "hooks-liveness"); !c.Pass {
		t.Fatalf("existing hook script must pass: %+v", c)
	}

	// Bare command names resolve through PATH lookup.
	writeFileT(t, settings, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"definitely-not-a-real-binary"}]}]}}`)
	opts.LookPath = func(file string) (string, error) {
		if file == "definitely-not-a-real-binary" {
			return "", fmt.Errorf("not found")
		}
		return "/fake/bin/" + file, nil
	}
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "hooks-liveness"); c.Pass {
		t.Fatal("bare hook command missing from PATH must fail")
	}
}

func TestAdoptStage0(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	opts := stubTools("git@github.com:acme/app.git")
	opts.AdoptStage = 0

	rep, _ := Run(root, opts)
	if checkByName(t, rep, "adopt-0-manifest").Pass || checkByName(t, rep, "adopt-0-baselines").Pass {
		t.Fatal("stage 0 must fail without manifest+baselines")
	}

	writeFileT(t, filepath.Join(root, ".blueprint", "manifest.json"), "{}\n")
	writeFileT(t, filepath.Join(root, ".blueprint", "baselines.json"), "{}\n")
	rep, _ = Run(root, opts)
	if !rep.Pass {
		t.Fatalf("stage 0 with both files must pass: %+v", rep.Checks)
	}
}

func TestAdoptStage1AgentsLint(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	opts := stubTools("git@github.com:acme/app.git")
	opts.AdoptStage = 1

	// Missing AGENTS.md.
	rep, _ := Run(root, opts)
	if checkByName(t, rep, "adopt-1-agents-lint").Pass {
		t.Fatal("missing AGENTS.md must fail")
	}

	// Over the 120-line cap.
	writeFileT(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("line\n", 121))
	rep, _ = Run(root, opts)
	c := checkByName(t, rep, "adopt-1-agents-lint")
	if c.Pass || !strings.Contains(c.Detail, "121") {
		t.Fatalf("cap violation must fail naming the count: %+v", c)
	}

	// Broken pointer.
	writeFileT(t, filepath.Join(root, "AGENTS.md"), "# Index\n\nSee [specs](.blueprint/specs/missing.md).\n")
	rep, _ = Run(root, opts)
	c = checkByName(t, rep, "adopt-1-agents-lint")
	if c.Pass || !strings.Contains(c.Detail, ".blueprint/specs/missing.md") {
		t.Fatalf("broken pointer must fail naming the target: %+v", c)
	}

	// Green: short file, resolving pointer (web links and anchors ignored).
	writeFileT(t, filepath.Join(root, "docs", "map.md"), "# map\n")
	writeFileT(t, filepath.Join(root, "AGENTS.md"),
		"# Index\n\nSee [map](docs/map.md), [site](https://example.com), [top](#index).\n")
	rep, _ = Run(root, opts)
	if !rep.Pass {
		t.Fatalf("clean AGENTS.md + runbook must pass stage 1: %+v", rep.Checks)
	}
}

func TestAdoptStage3(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	opts := stubTools("git@github.com:acme/app.git")
	opts.AdoptStage = 3

	rep, _ := Run(root, opts)
	if checkByName(t, rep, "adopt-3-verified-change").Pass {
		t.Fatal("empty archive must fail stage 3")
	}

	// A red verdict does not count.
	red, _ := json.Marshal(core.Verdict{ChangeID: "c1", Pass: false})
	writeFileT(t, filepath.Join(root, ".blueprint", "archive", "c1", "verdict", "verdict.json"), string(red))
	rep, _ = Run(root, opts)
	if checkByName(t, rep, "adopt-3-verified-change").Pass {
		t.Fatal("red verdict must not satisfy stage 3")
	}

	green, _ := json.Marshal(core.Verdict{ChangeID: "c2", Pass: true, Time: time.Now()})
	writeFileT(t, filepath.Join(root, ".blueprint", "archive", "c2", "verdict", "verdict.json"), string(green))
	rep, _ = Run(root, opts)
	if !checkByName(t, rep, "adopt-3-verified-change").Pass {
		t.Fatal("archived green verdict must satisfy stage 3")
	}
}

func TestAdoptStage4(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	opts := stubTools("git@github.com:acme/app.git")
	opts.AdoptStage = 4

	// Freshness is a function of an explicit clock.
	old := knowledgeLintNow
	knowledgeLintNow = func() time.Time { return time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC) }
	defer func() { knowledgeLintNow = old }()

	rep, _ := Run(root, opts)
	for _, name := range []string{"adopt-4-glossary", "adopt-4-runbook", "adopt-4-adr"} {
		if checkByName(t, rep, name).Pass && name != "adopt-4-runbook" {
			t.Fatalf("%s must fail on empty knowledge dir", name)
		}
	}
	// The dev-env fixture has no frontmatter: the real knowledge lint fails.
	if checkByName(t, rep, "adopt-4-knowledge-lint").Pass {
		t.Fatal("knowledge lint must fail while knowledge files lack reviewed: frontmatter")
	}

	fm := "---\nreviewed: 2026-07-01\nowner: alice\nstatus: draft\n---\n\n"
	writeFileT(t, filepath.Join(root, ".blueprint", "knowledge", "runbooks", "dev-env.md"),
		fm+"# dev env\n\nSee [glossary](../glossary.md).\n\n```sh\ntrue\n```\n")
	writeFileT(t, filepath.Join(root, ".blueprint", "knowledge", "glossary.md"),
		fm+"# terms\n\nDecisions: [ADR-0001](decisions/0001-adr.md); setup: [dev-env](runbooks/dev-env.md).\n")
	writeFileT(t, filepath.Join(root, ".blueprint", "knowledge", "decisions", "0001-adr.md"),
		"---\nreviewed: 2026-07-01\nowner: alice\nstatus: accepted\n---\n\n# adr\n\nIndex: [glossary](../glossary.md).\n")
	rep, _ = Run(root, opts)
	if !rep.Pass {
		t.Fatalf("curated glossary + runbook + ADR must pass stage 4: %+v", rep.Checks)
	}

	// A stale reviewed: date turns the real check red — existence is not enough.
	writeFileT(t, filepath.Join(root, ".blueprint", "knowledge", "glossary.md"),
		"---\nreviewed: 2025-01-01\nowner: alice\nstatus: draft\n---\n\n# terms\n\nDecisions: [ADR-0001](decisions/0001-adr.md); setup: [dev-env](runbooks/dev-env.md).\n")
	rep, _ = Run(root, opts)
	c := checkByName(t, rep, "adopt-4-knowledge-lint")
	if c.Pass || !strings.Contains(c.Detail, "error") {
		t.Fatalf("stale knowledge must fail the stage-4 lint check: %+v", c)
	}
}

func TestAdoptStageOutOfRange(t *testing.T) {
	root := t.TempDir()
	opts := stubTools("git@github.com:acme/app.git")
	opts.AdoptStage = 7
	if _, err := Run(root, opts); err == nil || !strings.Contains(err.Error(), "0 through 4") {
		t.Fatalf("stage 7 must error with the valid range, got %v", err)
	}
}

func TestSafetyDenyRules(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	opts := stubTools("git@github.com:acme/app.git")
	safety := filepath.Join(root, ".blueprint", "safety.toml")
	settings := filepath.Join(root, ".claude", "settings.json")

	// No safety.toml: vacuous pass.
	rep, _ := Run(root, opts)
	if c := checkByName(t, rep, "safety-deny-rules"); !c.Pass {
		t.Fatalf("absent safety.toml must pass: %+v", c)
	}

	// Empty deny list: still vacuous.
	writeFileT(t, safety, "[deny]\nwrite = []\n")
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "safety-deny-rules"); !c.Pass {
		t.Fatalf("empty deny list must pass: %+v", c)
	}

	// Non-empty deny list but no settings.json: the inert-guardrail scar —
	// fail, remediation says run `blueprint sync`.
	writeFileT(t, safety, "[deny]\nwrite = [\".env*\", \"**/secrets/**\"]\n")
	rep, _ = Run(root, opts)
	c := checkByName(t, rep, "safety-deny-rules")
	if c.Pass {
		t.Fatal("deny globs without settings.json must fail")
	}
	if !strings.Contains(c.Remediation, "blueprint sync") {
		t.Errorf("remediation must say to run `blueprint sync`, got %q", c.Remediation)
	}

	// Settings present but missing one compiled rule: fail and name it.
	writeFileT(t, settings, `{"permissions":{"deny":["Write(.env*)","Edit(.env*)","Write(**/secrets/**)"]}}`)
	rep, _ = Run(root, opts)
	c = checkByName(t, rep, "safety-deny-rules")
	if c.Pass {
		t.Fatal("missing Edit(**/secrets/**) rule must fail")
	}
	if !strings.Contains(c.Detail, "Edit(**/secrets/**)") {
		t.Errorf("detail should name the missing rule, got %q", c.Detail)
	}
	if !strings.Contains(c.Remediation, "blueprint sync") {
		t.Errorf("remediation must say to run `blueprint sync`, got %q", c.Remediation)
	}

	// All compiled rules present (user extras welcome): pass.
	writeFileT(t, settings, `{"permissions":{"deny":["Write(.env*)","Edit(.env*)","Write(**/secrets/**)","Edit(**/secrets/**)","Bash(rm -rf *)"]}}`)
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "safety-deny-rules"); !c.Pass {
		t.Fatalf("all rules present must pass: %+v", c)
	}

	// Malformed settings.json: fail (host enforces nothing it cannot parse).
	writeFileT(t, settings, "{not json")
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "safety-deny-rules"); c.Pass {
		t.Fatal("malformed settings.json must fail")
	}

	// Malformed safety.toml: fail with a fix-the-TOML remediation.
	writeFileT(t, safety, "[deny\nwrite = [")
	rep, _ = Run(root, opts)
	if c := checkByName(t, rep, "safety-deny-rules"); c.Pass {
		t.Fatal("malformed safety.toml must fail")
	}
}

func TestRetrievalTiers(t *testing.T) {
	// pathWith simulates a PATH holding only the named binaries.
	pathWith := func(bins ...string) func(string) (string, error) {
		set := map[string]bool{}
		for _, b := range bins {
			set[b] = true
		}
		return func(file string) (string, error) {
			if set[file] {
				return "/fake/bin/" + file, nil
			}
			return "", fmt.Errorf("%s not on PATH", file)
		}
	}
	baseBins := []string{"git", "blueprint"} // keep the other checks green

	tests := []struct {
		name       string
		config     string // "" = no config.toml
		bins       []string
		wantPass   bool
		wantDetail string
		wantRemedy string
	}{
		{
			name:       "nothing configured passes vacuously",
			wantPass:   true,
			wantDetail: "Tier 0/1",
		},
		{
			name:     "configured and present passes",
			config:   "[retrieval]\ntier2_packing = \"repomix\"\ntier2_lsp = \"serena\"\n[retrieval.graph]\ncommand = \"graph-backend --stdio\"\n",
			bins:     []string{"npx", "uvx", "graph-backend"},
			wantPass: true,
		},
		{
			name:       "repomix without npx fails with install remediation",
			config:     "[retrieval]\ntier2_packing = \"repomix\"\n",
			wantPass:   false,
			wantDetail: "npx",
			wantRemedy: "Node.js",
		},
		{
			name:       "serena without uvx fails with install remediation",
			config:     "[retrieval]\ntier2_lsp = \"serena\"\n",
			wantPass:   false,
			wantDetail: "uvx",
			wantRemedy: "uv",
		},
		{
			name:       "graph command off PATH fails",
			config:     "[retrieval.graph]\ncommand = \"graph-backend --stdio\"\n",
			wantPass:   false,
			wantDetail: "graph-backend",
			wantRemedy: "[retrieval.graph]",
		},
		{
			name:       "unknown tier2 value fails naming the accepted set",
			config:     "[retrieval]\ntier2_packing = \"sourcegraph\"\n",
			bins:       []string{"npx", "uvx"},
			wantPass:   false,
			wantDetail: "sourcegraph",
			wantRemedy: "repomix, serena",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDevEnvRunbook(t, root)
			if tc.config != "" {
				writeFileT(t, filepath.Join(root, ".blueprint", "config.toml"), tc.config)
			}
			opts := stubTools("git@github.com:acme/app.git")
			opts.LookPath = pathWith(append(tc.bins, baseBins...)...)
			rep, err := Run(root, opts)
			if err != nil {
				t.Fatal(err)
			}
			c := checkByName(t, rep, "retrieval-tiers")
			if c.Pass != tc.wantPass {
				t.Fatalf("pass = %v, want %v (%+v)", c.Pass, tc.wantPass, c)
			}
			if tc.wantDetail != "" && !strings.Contains(c.Detail, tc.wantDetail) {
				t.Errorf("detail must mention %q, got %q", tc.wantDetail, c.Detail)
			}
			if tc.wantRemedy != "" && !strings.Contains(c.Remediation, tc.wantRemedy) {
				t.Errorf("remediation must mention %q, got %q", tc.wantRemedy, c.Remediation)
			}
			if !c.Pass && c.Remediation == "" {
				t.Error("failing check must carry a remediation")
			}
		})
	}
}

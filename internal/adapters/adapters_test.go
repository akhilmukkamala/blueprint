package adapters

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

var update = flag.Bool("update", false, "rewrite golden files from current output")

// seedFixtureRepo builds the canonical test repo: two steering rules, one
// declared MCP server, plus pre-existing user files (.gitignore, .mcp.json
// with a foreign server, codex config.toml with foreign keys, a user
// CLAUDE.md) so merge and .bak behavior are exercised.
func seedFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("AGENTS.md", "# Acme repo index\n\n<!-- blueprint:managed -->\nengine-owned index content\n<!-- /blueprint:managed -->\n\nHand-written user notes outside the managed block.\n")
	write(".blueprint/safety.toml", `[deny]
write = [".env*", "**/secrets/**", ".github/workflows/**"]
`)
	write(".blueprint/steering/go-style.md", `---
id: go-style
description: Go style rules for this repo
globs: ["**/*.go"]
activation: glob
---
Use table-driven tests.

Wrap errors with %w and remediation text.
`)
	write(".blueprint/steering/security.md", `---
id: security
description: Security rules
globs: []
activation: always
---
Never write secrets to the journal.
`)
	write(".blueprint/config.toml", `[router]
max_files_exempt = 2

[mcp.servers.blueprint]
command = "blueprint"
args = ["mcp", "serve"]
`)
	write(".gitignore", "node_modules/\ndist/\n")
	write(".mcp.json", `{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    }
  }
}
`)
	write(".codex/config.toml", `model = "o3"

[mcp_servers.docs]
command = "docs-server"
`)
	write("CLAUDE.md", "My hand-written claude notes.\n")
	write(".gemini/settings.json", `{
  "theme": "dark",
  "context": {
    "fileName": "GEMINI.md"
  },
  "mcpServers": {
    "weather": {
      "command": "weather-server"
    }
  }
}
`)
	return root
}

// goldenName flattens a repo path to a testdata/golden filename. The suffix
// keeps a literal .gitignore golden from acting as a real ignore file inside
// testdata.
func goldenName(path string) string {
	return strings.ReplaceAll(path, "/", "__") + ".golden"
}

func TestBuildGolden(t *testing.T) {
	root := seedFixtureRepo(t)
	plan, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	wantPaths := []string{
		"CLAUDE.md",
		".claude/settings.json",
		".codex/sandbox-policy.md",
		".cursor/blueprint-safety.md",
		".claude/commands/blueprint-new.md",
		".claude/commands/blueprint-resume.md",
		".claude/commands/blueprint-verify.md",
		".claude/commands/blueprint-status.md",
		".claude/rules/go-style.md",
		".claude/rules/security.md",
		".mcp.json",
		".cursor/commands/blueprint-new.md",
		".cursor/commands/blueprint-resume.md",
		".cursor/commands/blueprint-verify.md",
		".cursor/commands/blueprint-status.md",
		".cursor/rules/go-style.mdc",
		".cursor/rules/security.mdc",
		".cursor/mcp.json",
		".codex/prompts/blueprint-new.md",
		".codex/prompts/blueprint-resume.md",
		".codex/prompts/blueprint-verify.md",
		".codex/prompts/blueprint-status.md",
		".codex/config.toml",
		".windsurf/rules/go-style.md",
		".windsurf/rules/security.md",
		".devin/rules/go-style.md",
		".devin/rules/security.md",
		".windsurf/workflows/blueprint-new.md",
		".windsurf/workflows/blueprint-resume.md",
		".windsurf/workflows/blueprint-verify.md",
		".windsurf/workflows/blueprint-status.md",
		".windsurf/blueprint-mcp-note.md",
		".github/instructions/go-style.instructions.md",
		".github/instructions/security.instructions.md",
		".github/prompts/blueprint-new.prompt.md",
		".github/prompts/blueprint-resume.prompt.md",
		".github/prompts/blueprint-verify.prompt.md",
		".github/prompts/blueprint-status.prompt.md",
		".github/blueprint-mcp-note.md",
		".gemini/commands/blueprint-new.toml",
		".gemini/commands/blueprint-resume.toml",
		".gemini/commands/blueprint-verify.toml",
		".gemini/commands/blueprint-status.toml",
		".gemini/settings.json",
		".gitignore",
	}
	got := plan.Paths()
	sortedGot, sortedWant := append([]string(nil), got...), append([]string(nil), wantPaths...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)
	if strings.Join(sortedGot, "\n") != strings.Join(sortedWant, "\n") {
		t.Fatalf("plan paths mismatch:\ngot:\n%s\nwant:\n%s", strings.Join(sortedGot, "\n"), strings.Join(sortedWant, "\n"))
	}

	for _, f := range plan.Files {
		gp := filepath.Join("testdata", "golden", goldenName(f.Path))
		if *update {
			if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(gp, f.Content, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(gp)
		if err != nil {
			t.Fatalf("missing golden for %s (run go test -run TestBuildGolden -update): %v", f.Path, err)
		}
		if !bytes.Equal(want, f.Content) {
			t.Errorf("%s differs from golden %s:\n--- got ---\n%s\n--- want ---\n%s", f.Path, gp, f.Content, want)
		}
	}

	// Every generated file must carry the provenance marker.
	for _, f := range plan.Files {
		if !IsGenerated(f.Content) {
			t.Errorf("%s lacks the provenance marker %q", f.Path, provenanceMarker)
		}
	}
}

func TestSyncIdempotentAndBak(t *testing.T) {
	root := seedFixtureRepo(t)
	plan, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Sync(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != len(plan.Files) {
		t.Fatalf("first sync wrote %d files, want %d", len(res.Written), len(plan.Files))
	}
	// Pre-existing user files got backed up exactly once; generated-only
	// files did not.
	wantBaks := map[string]bool{"CLAUDE.md.bak": true, ".mcp.json.bak": true, ".codex/config.toml.bak": true, ".gitignore.bak": true, ".gemini/settings.json.bak": true}
	if len(res.BackedUp) != len(wantBaks) {
		t.Fatalf("backed up %v, want the five pre-existing user files", res.BackedUp)
	}
	for _, b := range res.BackedUp {
		if !wantBaks[b] {
			t.Errorf("unexpected backup %s", b)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md.bak")); string(got) != "My hand-written claude notes.\n" {
		t.Errorf("CLAUDE.md.bak does not hold the pre-sync original: %q", got)
	}

	// Second sync: no writes, no new backups (idempotence).
	plan2, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := Sync(root, plan2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Written) != 0 || len(res2.BackedUp) != 0 {
		t.Fatalf("second sync not idempotent: written=%v backed_up=%v", res2.Written, res2.BackedUp)
	}
	if len(res2.Unchanged) != len(plan2.Files) {
		t.Fatalf("second sync unchanged=%d, want %d", len(res2.Unchanged), len(plan2.Files))
	}
}

func TestCheckReportsDrift(t *testing.T) {
	root := seedFixtureRepo(t)
	plan, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(root, plan); err != nil {
		t.Fatal(err)
	}

	clean, err := Check(root, mustBuild(t, root), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(clean) != 0 {
		t.Fatalf("freshly synced tree reports drift: %v", clean)
	}

	// Hand-edit one generated file, delete another.
	ruleP := filepath.Join(root, ".cursor", "rules", "go-style.mdc")
	if err := os.WriteFile(ruleP, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".claude", "commands", "blueprint-new.md")); err != nil {
		t.Fatal(err)
	}
	drifts, err := Check(root, mustBuild(t, root), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, d := range drifts {
		byPath[d.Path] = d.Reason
	}
	if byPath[".cursor/rules/go-style.mdc"] != "modified" {
		t.Errorf("tampered rule not reported as modified: %v", drifts)
	}
	if byPath[".claude/commands/blueprint-new.md"] != "missing" {
		t.Errorf("deleted stub not reported as missing: %v", drifts)
	}
	if len(drifts) != 2 {
		t.Errorf("want exactly 2 drifts, got %v", drifts)
	}
}

func TestMCPMergePreservesForeignEntries(t *testing.T) {
	root := seedFixtureRepo(t)
	if _, err := Sync(root, mustBuild(t, root)); err != nil {
		t.Fatal(err)
	}

	// JSON side: foreign "filesystem" server and blueprint server coexist.
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf(".mcp.json is not valid JSON after merge: %v", err)
	}
	if doc.Servers["filesystem"].Command != "npx" {
		t.Errorf("foreign filesystem entry lost: %+v", doc.Servers)
	}
	if doc.Servers["blueprint"].Command != "blueprint" {
		t.Errorf("blueprint entry missing: %+v", doc.Servers)
	}

	// TOML side: foreign top-level key and foreign server survive.
	rawT, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var codex struct {
		Model   string `toml:"model"`
		Servers map[string]struct {
			Command string `toml:"command"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(rawT, &codex); err != nil {
		t.Fatalf("codex config.toml is not valid TOML after merge: %v", err)
	}
	if codex.Model != "o3" {
		t.Errorf("foreign top-level key model lost: %q", codex.Model)
	}
	if codex.Servers["docs"].Command != "docs-server" {
		t.Errorf("foreign docs server lost: %+v", codex.Servers)
	}
	if codex.Servers["blueprint"].Command != "blueprint" {
		t.Errorf("blueprint server missing: %+v", codex.Servers)
	}
}

// planContent returns the planned bytes for path, failing if absent.
func planContent(t *testing.T, plan *Plan, path string) []byte {
	t.Helper()
	for _, f := range plan.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("plan has no %s (paths: %v)", path, plan.Paths())
	return nil
}

func TestBuildTier2RetrievalServers(t *testing.T) {
	root := seedFixtureRepo(t)

	// Tiers off (fixture default): no retrieval-derived servers appear.
	off := mustBuild(t, root)
	for _, name := range []string{"repomix", "serena"} {
		if bytes.Contains(planContent(t, off, ".mcp.json"), []byte(name)) {
			t.Errorf("tiers off but %s appears in .mcp.json", name)
		}
	}

	// Tiers on: both tools land in every MCP surface with the table's
	// launch lines, alongside the user's blueprint server.
	appendConfig := "\n[retrieval]\ntier2_packing = \"repomix\"\ntier2_lsp = \"serena\"\n"
	cfgPath := filepath.Join(root, ".blueprint", "config.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, append(cfg, appendConfig...), 0o644); err != nil {
		t.Fatal(err)
	}
	on := mustBuild(t, root)
	var doc struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(planContent(t, on, ".mcp.json"), &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Servers["repomix"]; got.Command != "npx" || strings.Join(got.Args, " ") != "-y repomix --mcp" {
		t.Errorf("repomix entry = %+v", got)
	}
	if got := doc.Servers["serena"]; got.Command != "uvx" || len(got.Args) != 4 {
		t.Errorf("serena entry = %+v", got)
	}
	if doc.Servers["blueprint"].Command != "blueprint" {
		t.Errorf("user-declared blueprint server lost: %+v", doc.Servers)
	}
	var codex struct {
		Servers map[string]struct {
			Command string `toml:"command"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(planContent(t, on, ".codex/config.toml"), &codex); err != nil {
		t.Fatal(err)
	}
	if codex.Servers["repomix"].Command != "npx" || codex.Servers["serena"].Command != "uvx" {
		t.Errorf("tier-2 servers missing from codex config: %+v", codex.Servers)
	}

	// An explicit [mcp.servers.repomix] outranks the built-in table.
	override := "\n[mcp.servers.repomix]\ncommand = \"my-repomix\"\n"
	cfg, err = os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, append(cfg, override...), 0o644); err != nil {
		t.Fatal(err)
	}
	over := mustBuild(t, root)
	if err := json.Unmarshal(planContent(t, over, ".mcp.json"), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Servers["repomix"].Command != "my-repomix" {
		t.Errorf("user declaration must win over the table: %+v", doc.Servers["repomix"])
	}

	// An unknown tier2 value is a build error naming the accepted set.
	if err := os.WriteFile(cfgPath, []byte("[retrieval]\ntier2_lsp = \"nope\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(root); err == nil || !strings.Contains(err.Error(), "repomix, serena") {
		t.Errorf("unknown tier2 tool must fail the build with remediation, got %v", err)
	}
}

// TestRevertRoundtrip is the AC-4b zero-byte-loss check across all six
// targets: sync generates every surface (including pre-existing user files
// merged in place), revert restores the tree byte-identically.
func TestRevertRoundtrip(t *testing.T) {
	root := seedFixtureRepo(t)
	before := snapshotTree(t, root)

	if _, err := Sync(root, mustBuild(t, root)); err != nil {
		t.Fatal(err)
	}
	res, err := Revert(root, mustBuild(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) == 0 || len(res.Removed) == 0 {
		t.Fatalf("revert did nothing meaningful: %+v", res)
	}

	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("tree size changed after revert: before=%d after=%d\nbefore=%v\nafter=%v", len(before), len(after), keys(before), keys(after))
	}
	for p, want := range before {
		if after[p] != want {
			t.Errorf("%s content changed after revert roundtrip", p)
		}
	}
}

func TestGitignoreManagedBlockMerge(t *testing.T) {
	got := string(mergeGitignore([]byte("user-stuff/\n"), []string{"CLAUDE.md", ".mcp.json"}))
	if !strings.HasPrefix(got, "user-stuff/\n") {
		t.Errorf("user lines must stay first:\n%s", got)
	}
	if !strings.Contains(got, gitignoreBegin) || !strings.Contains(got, gitignoreEnd) {
		t.Errorf("managed markers missing:\n%s", got)
	}
	// Re-merging with a different path set replaces the block, not appends.
	second := string(mergeGitignore([]byte(got), []string{"CLAUDE.md"}))
	if strings.Count(second, gitignoreBegin) != 1 {
		t.Errorf("managed block duplicated:\n%s", second)
	}
	if strings.Contains(second, ".mcp.json") {
		t.Errorf("stale path survived block replacement:\n%s", second)
	}
	if !strings.HasPrefix(second, "user-stuff/\n") {
		t.Errorf("user lines lost on re-merge:\n%s", second)
	}
}

func TestSteeringParsing(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		want    SteeringRule
	}{
		{
			name: "globs as single string",
			raw:  "---\nid: x\ndescription: d\nglobs: \"src/**\"\nactivation: glob\n---\nbody\n",
			want: SteeringRule{ID: "x", Description: "d", Globs: []string{"src/**"}, Activation: "glob", Body: "body\n"},
		},
		{
			name:    "missing frontmatter",
			raw:     "just a body\n",
			wantErr: true,
		},
		{
			name:    "unterminated frontmatter",
			raw:     "---\nid: x\n",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSteering([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.want.ID || got.Description != tc.want.Description ||
				got.Activation != tc.want.Activation || got.Body != tc.want.Body ||
				strings.Join(got.Globs, ",") != strings.Join(tc.want.Globs, ",") {
				t.Errorf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

// TestRuleFrontmatterTransformsNewTargets pins the windsurf (trigger/globs)
// and copilot (applyTo) frontmatter vocabularies for every activation mode.
func TestRuleFrontmatterTransformsNewTargets(t *testing.T) {
	rule := func(activation string, globs ...string) SteeringRule {
		return SteeringRule{ID: "r", Description: "desc", Globs: globs, Activation: activation, Body: "body\n", Source: ".blueprint/steering/r.md"}
	}
	windsurf := Target{Name: "windsurf", RulesDir: ".windsurf/rules", RulesTransform: TransformWindsurf, RulesExt: ".md"}
	copilot := Target{Name: "copilot", RulesDir: ".github/instructions", RulesTransform: TransformApplyTo, RulesExt: ".instructions.md"}

	tests := []struct {
		name    string
		target  Target
		rule    SteeringRule
		want    []string
		notWant []string
	}{
		{"windsurf glob", windsurf, rule("glob", "**/*.go"),
			[]string{"trigger: glob\n", "globs: ['**/*.go']\n", "description: desc\n"}, nil},
		{"windsurf always", windsurf, rule("always"),
			[]string{"trigger: always_on\n"}, []string{"globs:"}},
		{"windsurf manual", windsurf, rule("manual"),
			[]string{"trigger: manual\n"}, []string{"globs:"}},
		{"copilot glob joins comma-separated", copilot, rule("glob", "**/*.ts", "**/*.tsx"),
			[]string{"applyTo: '**/*.ts,**/*.tsx'\n"}, nil},
		{"copilot always", copilot, rule("always"),
			[]string{"applyTo: '**'\n"}, nil},
		{"copilot manual has no applyTo", copilot, rule("manual"),
			[]string{"description: desc\n"}, []string{"applyTo:"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(RenderRule(tc.target, tc.rule))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("unwanted %q in:\n%s", nw, got)
				}
			}
			if !strings.HasSuffix(got, "---\nbody\n") {
				t.Errorf("body not verbatim after frontmatter:\n%s", got)
			}
		})
	}
}

// TestGeminiSettingsMerge: foreign keys, a user context.fileName, and foreign
// mcpServers entries all survive; AGENTS.md is appended, never substituted.
func TestGeminiSettingsMerge(t *testing.T) {
	existing := []byte(`{
  "theme": "dark",
  "context": {"fileName": "GEMINI.md", "importFormat": "flat"},
  "mcpServers": {"weather": {"command": "weather-server"}}
}`)
	merged, err := MergeGeminiSettings(existing, map[string]MCPServer{
		"blueprint": {Command: "blueprint", Args: []string{"mcp", "serve"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Theme   string `json:"theme"`
		Context struct {
			FileName     []string `json:"fileName"`
			ImportFormat string   `json:"importFormat"`
		} `json:"context"`
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
		Prov string `json:"//blueprint"`
	}
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatalf("merged settings not valid JSON: %v\n%s", err, merged)
	}
	if doc.Theme != "dark" || doc.Context.ImportFormat != "flat" {
		t.Errorf("foreign keys lost: %+v", doc)
	}
	if strings.Join(doc.Context.FileName, ",") != "GEMINI.md,AGENTS.md" {
		t.Errorf("fileName must keep the user's entry and append AGENTS.md: %v", doc.Context.FileName)
	}
	if doc.Servers["weather"].Command != "weather-server" {
		t.Errorf("foreign mcp server lost: %+v", doc.Servers)
	}
	if doc.Servers["blueprint"].Command != "blueprint" {
		t.Errorf("blueprint server missing: %+v", doc.Servers)
	}
	if doc.Prov == "" {
		t.Error("provenance key //blueprint missing")
	}

	// Idempotence: merging the merge changes nothing.
	again, err := MergeGeminiSettings(merged, map[string]MCPServer{
		"blueprint": {Command: "blueprint", Args: []string{"mcp", "serve"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(merged, again) {
		t.Errorf("gemini settings merge not idempotent:\n--- first ---\n%s\n--- second ---\n%s", merged, again)
	}

	// Fresh file (no servers): context wiring alone, list-normalized.
	fresh, err := MergeGeminiSettings(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var freshDoc struct {
		Context struct {
			FileName []string `json:"fileName"`
		} `json:"context"`
		Servers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(fresh, &freshDoc); err != nil {
		t.Fatal(err)
	}
	if strings.Join(freshDoc.Context.FileName, ",") != "AGENTS.md" {
		t.Errorf("fresh settings fileName = %v, want [AGENTS.md]", freshDoc.Context.FileName)
	}
	if freshDoc.Servers != nil {
		t.Errorf("no servers declared but mcpServers key emitted: %v", freshDoc.Servers)
	}

	// Broken user JSON: remediation error, never overwrite.
	if _, err := MergeGeminiSettings([]byte("{nope"), nil); err == nil || !strings.Contains(err.Error(), "blueprint sync") {
		t.Errorf("invalid JSON must fail with remediation, got %v", err)
	}
}

// TestOversizeRuleSplit: a body over Windsurf's 12,000-byte cap splits into
// numbered parts, each under the cap, each carrying the split provenance
// note, with the part bodies concatenating byte-exactly to the canonical
// body — and the .devin mirror stays byte-identical per part.
func TestOversizeRuleSplit(t *testing.T) {
	root := seedFixtureRepo(t)
	var big strings.Builder
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&big, "line %04d: the quick brown fox jumps over the lazy dog\n", i)
	}
	body := big.String() // ~38 KB, needs >3 parts at 12k
	src := "---\nid: huge\ndescription: giant rule\nglobs: []\nactivation: always\n---\n" + body
	if err := os.WriteFile(filepath.Join(root, ".blueprint", "steering", "huge.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := mustBuild(t, root)

	var parts []File
	for _, f := range plan.Files {
		if strings.HasPrefix(f.Path, ".windsurf/rules/huge-part") {
			parts = append(parts, f)
		}
		if f.Path == ".windsurf/rules/huge.md" {
			t.Errorf("oversize rule emitted unsplit at %s", f.Path)
		}
	}
	if len(parts) < 2 {
		t.Fatalf("oversize rule did not split: %v", plan.Paths())
	}
	var rejoined strings.Builder
	for i, p := range parts {
		if len(p.Content) > 12000 {
			t.Errorf("%s is %d bytes, over the 12000 cap", p.Path, len(p.Content))
		}
		content := string(p.Content)
		note := fmt.Sprintf("part %d of %d", i+1, len(parts))
		if !strings.Contains(content, note) {
			t.Errorf("%s missing split note %q", p.Path, note)
		}
		if !strings.Contains(content, ".blueprint/steering/huge.md") {
			t.Errorf("%s split note does not cite the canonical source", p.Path)
		}
		idx := strings.Index(content, "---\n")
		end := strings.Index(content[idx+4:], "---\n")
		rejoined.WriteString(content[idx+4+end+4:])
		// Mirror dir carries the identical bytes.
		mirror := planContent(t, plan, strings.Replace(p.Path, ".windsurf/rules", ".devin/rules", 1))
		if !bytes.Equal(mirror, p.Content) {
			t.Errorf("mirror of %s not byte-identical", p.Path)
		}
	}
	if rejoined.String() != body {
		t.Errorf("part bodies do not concatenate to the canonical body (got %d bytes, want %d)", rejoined.Len(), len(body))
	}

	// Targets without a byte budget keep the rule whole.
	whole := planContent(t, plan, ".claude/rules/huge.md")
	if !bytes.HasSuffix(whole, []byte(body)) {
		t.Error(".claude rule must stay unsplit and carry the full body")
	}
}

func mustBuild(t *testing.T, root string) *Plan {
	t.Helper()
	plan, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// snapshotTree maps repo-relative slash paths to contents for the whole tree.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

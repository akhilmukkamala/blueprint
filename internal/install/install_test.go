package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func mustInit(t *testing.T, root string) *InitResult {
	t.Helper()
	res, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return res
}

func TestManagedSplitReplaceStrip(t *testing.T) {
	tests := []struct {
		name, rel, content string
		wantFound          bool
		wantInner          string
	}{
		{
			name:      "markdown markers",
			rel:       "AGENTS.md",
			content:   "body\n<!-- blueprint:managed -->\ninner line\n<!-- /blueprint:managed -->\ntail\n",
			wantFound: true,
			wantInner: "inner line\n",
		},
		{
			name:      "hash markers",
			rel:       ".blueprint/config.toml",
			content:   "# head\n# blueprint:managed\n[router]\n# /blueprint:managed\n# tail\n",
			wantFound: true,
			wantInner: "[router]\n",
		},
		{
			name:      "no markers",
			rel:       "AGENTS.md",
			content:   "just a body\n",
			wantFound: false,
		},
		{
			name:      "start without end",
			rel:       "AGENTS.md",
			content:   "body\n<!-- blueprint:managed -->\ninner\n",
			wantFound: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sp := splitManaged(tc.content, tc.rel)
			if sp.found != tc.wantFound {
				t.Fatalf("found = %v, want %v", sp.found, tc.wantFound)
			}
			if !tc.wantFound {
				return
			}
			if sp.inner != tc.wantInner {
				t.Fatalf("inner = %q, want %q", sp.inner, tc.wantInner)
			}
			// Reassembly with the same inner is the identity.
			if got := renderManaged(sp, tc.rel, sp.inner); got != tc.content {
				t.Fatalf("identity reassembly changed content:\n%q\n->\n%q", tc.content, got)
			}
			replaced, ok := replaceManaged(tc.content, tc.rel, "NEW\n")
			if !ok || !strings.Contains(replaced, "NEW\n") {
				t.Fatalf("replaceManaged failed: %q", replaced)
			}
			if !strings.HasPrefix(replaced, sp.before) || !strings.HasSuffix(replaced, sp.after) {
				t.Fatal("replaceManaged disturbed the user body")
			}
			stripped, ok := stripManaged(tc.content, tc.rel)
			if !ok || strings.Contains(stripped, "blueprint:managed") {
				t.Fatalf("stripManaged left markers: %q", stripped)
			}
			if stripped != sp.before+sp.after {
				t.Fatalf("strip = %q, want %q", stripped, sp.before+sp.after)
			}
		})
	}
}

func TestTemplatesHaveManagedRegionsWhereMixed(t *testing.T) {
	for _, tpl := range Templates() {
		b, err := tpl.Content()
		if err != nil {
			t.Fatalf("%s: %v", tpl.RelPath, err)
		}
		sp := splitManaged(string(b), tpl.RelPath)
		if tpl.Tier == core.OwnerMixed && !sp.found {
			t.Errorf("%s is [mixed] but its template has no managed region", tpl.RelPath)
		}
		if tpl.Tier != core.OwnerMixed && sp.found {
			t.Errorf("%s is [%s] but its template carries a managed region", tpl.RelPath, tpl.Tier)
		}
	}
}

func TestInitWritesFloorAndManifest(t *testing.T) {
	root := t.TempDir()
	res := mustInit(t, root)
	// safety.toml is floor since the C4 safety-compile fix: a fresh repo gets
	// a default safety envelope (DESIGN §13), not an empty one.
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".blueprint/config.toml", ".blueprint/manifest.json", ".blueprint/safety.toml"} {
		if !exists(root, rel) {
			t.Errorf("floor file %s missing after init", rel)
		}
	}
	// Remaining optional user configs appear on first use only.
	for _, rel := range []string{".blueprint/registry.toml", ".blueprint/verifiers.toml"} {
		if exists(root, rel) {
			t.Errorf("%s written by init; it must appear on first use", rel)
		}
	}
	if len(res.Skipped) != 0 {
		t.Errorf("fresh init skipped %v", res.Skipped)
	}
	m, err := LoadManifest(root)
	if err != nil || m == nil {
		t.Fatalf("manifest: %v %v", m, err)
	}
	if m.Version != TemplatesVersion {
		t.Errorf("manifest version = %q", m.Version)
	}
	if e := m.Files["AGENTS.md"]; e.Tier != core.OwnerMixed || e.ManagedSHA256 == "" {
		t.Errorf("AGENTS.md entry incomplete: %+v", e)
	}
	if e := m.Files["CLAUDE.md"]; e.Tier != core.OwnerTool || e.ManagedSHA256 != "" {
		t.Errorf("CLAUDE.md entry wrong: %+v", e)
	}
}

func TestInitIdempotentAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", "my own index\n")
	res := mustInit(t, root)
	if got := read(t, root, "AGENTS.md"); got != "my own index\n" {
		t.Fatalf("init overwrote a pre-existing file: %q", got)
	}
	found := false
	for _, s := range res.Skipped {
		if s == "AGENTS.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("AGENTS.md not reported skipped: %+v", res)
	}
	if _, ok := (mustLoad(t, root)).Files["AGENTS.md"]; ok {
		t.Error("manifest claims ownership of a file init refused to write")
	}
	// Second run changes nothing.
	before := read(t, root, ".blueprint/config.toml")
	res2 := mustInit(t, root)
	if len(res2.Written) != 1 || res2.Written[0] != ".blueprint/manifest.json" {
		t.Errorf("re-init wrote files: %+v", res2.Written)
	}
	if read(t, root, ".blueprint/config.toml") != before {
		t.Error("re-init altered config.toml")
	}
}

func mustLoad(t *testing.T, root string) *Manifest {
	t.Helper()
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("manifest missing")
	}
	return m
}

func TestMaterializeOptionalOnce(t *testing.T) {
	root := t.TempDir()
	mustInit(t, root)
	created, err := Materialize(root, ".blueprint/registry.toml")
	if err != nil || !created {
		t.Fatalf("Materialize: created=%v err=%v", created, err)
	}
	write(t, root, ".blueprint/registry.toml", "user content\n")
	created, err = Materialize(root, ".blueprint/registry.toml")
	if err != nil || created {
		t.Fatalf("second Materialize must be a no-op: created=%v err=%v", created, err)
	}
	if read(t, root, ".blueprint/registry.toml") != "user content\n" {
		t.Fatal("Materialize overwrote a user file")
	}
	if _, err := Materialize(root, "nope.toml"); err == nil {
		t.Fatal("unknown template must error")
	}
}

func TestManifestVerifyDrift(t *testing.T) {
	root := t.TempDir()
	if _, err := Verify(root); err == nil {
		t.Fatal("Verify without a manifest must error with remediation")
	}
	mustInit(t, root)
	drift, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drift {
		if d.Status != "ok" {
			t.Errorf("fresh install drifted: %+v", d)
		}
	}
	write(t, root, "CLAUDE.md", "edited\n")
	if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	drift, err = Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range drift {
		got[d.Path] = d.Status
	}
	if got["CLAUDE.md"] != "modified" || got["AGENTS.md"] != "missing" || got[".blueprint/config.toml"] != "ok" {
		t.Errorf("drift wrong: %v", got)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write(t, root, "README.md", "hi\n")
	run("add", ".")
	run("commit", "-q", "-m", "feat: first")
	write(t, root, "a.txt", "a\n")
	run("add", ".")
	run("commit", "-q", "-m", "fix: broken thing")
	write(t, root, "b.txt", "b\n")
	run("add", ".")
	run("commit", "-q", "-m", `Revert "feat: first"`)
	return root
}

func gitCommitAll(t *testing.T, root, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestAdoptImportsBaselinesAndWorklog(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "CLAUDE.md", "old claude guidance\n")
	write(t, root, ".cursorrules", "cursor legacy rules\n")
	write(t, root, ".cursor/rules/style.mdc", "style rule body\n")
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	res, err := Adopt(root, now)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	// Imports carry provenance and preserve the source body.
	claude := read(t, root, ".blueprint/steering/imported-claude.md")
	if !strings.Contains(claude, "imported by `blueprint adopt` on 2026-07-21 from CLAUDE.md") ||
		!strings.Contains(claude, "old claude guidance\n") {
		t.Errorf("claude import wrong:\n%s", claude)
	}
	if !exists(root, ".blueprint/steering/imported-cursorrules.md") ||
		!exists(root, ".blueprint/steering/imported-cursor-style.md") {
		t.Errorf("cursor imports missing: %+v", res.Imported)
	}
	// CLAUDE.md becomes the shim only after its content is imported.
	if !res.Shim || !strings.Contains(read(t, root, "CLAUDE.md"), "@AGENTS.md") {
		t.Error("CLAUDE.md was not replaced with the shim")
	}
	// Baselines from git history: 3 commits, 1 revert, 1 fix.
	b := res.Baselines
	if b.Commits != 3 || b.Reverts != 1 || b.FixCommits != 1 {
		t.Errorf("baselines = %+v", b)
	}
	if !exists(root, ".blueprint/baselines.json") {
		t.Error("baselines.json not written")
	}
	// Stage-0 worklog event.
	events, _, err := worklog.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, ev := range events {
		if ev.Kind == "adopt-stage" && ev.Data["stage"] == float64(0) {
			seen = true
		}
	}
	if !seen {
		t.Errorf("no adopt-stage 0 worklog event: %+v", events)
	}

	// Re-adopt is idempotent: curated drafts are not clobbered, shim not re-imported.
	write(t, root, ".blueprint/steering/imported-claude.md", "curated\n")
	res2, err := Adopt(root, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if read(t, root, ".blueprint/steering/imported-claude.md") != "curated\n" {
		t.Error("re-adopt clobbered a curated steering draft")
	}
	if res2.Shim {
		t.Error("re-adopt re-imported our own shim")
	}
}

func TestAdoptWithoutGitHistoryStillSucceeds(t *testing.T) {
	root := t.TempDir()
	res, err := Adopt(root, time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Adopt on a non-git dir: %v", err)
	}
	if res.Baselines.Note == "" || res.Baselines.Commits != 0 {
		t.Errorf("expected zero baselines with a note, got %+v", res.Baselines)
	}
}

func TestUninstallKeepsMemory(t *testing.T) {
	root := t.TempDir()
	mustInit(t, root)
	if _, err := Materialize(root, ".blueprint/safety.toml"); err != nil {
		t.Fatal(err)
	}
	// User body in AGENTS.md and a spec that must survive.
	write(t, root, "AGENTS.md", "MY BODY\n<!-- blueprint:managed -->\nengine\n<!-- /blueprint:managed -->\n")
	write(t, root, ".blueprint/specs/auth/spec.md", "living spec\n")

	res, err := Uninstall(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if exists(root, "CLAUDE.md") || exists(root, ".blueprint/manifest.json") {
		t.Error("tool files not removed")
	}
	if got := read(t, root, "AGENTS.md"); got != "MY BODY\n" {
		t.Errorf("managed strip wrong: %q", got)
	}
	if !exists(root, ".blueprint/safety.toml") || !exists(root, ".blueprint/specs/auth/spec.md") {
		t.Error("user files did not survive uninstall")
	}
	var reasons []string
	for _, r := range res.Remaining {
		reasons = append(reasons, r.Path)
	}
	if len(reasons) == 0 {
		t.Error("uninstall reported nothing remaining")
	}
}

func TestUninstallPurge(t *testing.T) {
	root := t.TempDir()
	mustInit(t, root)
	write(t, root, ".blueprint/specs/auth/spec.md", "spec\n")
	res, err := Uninstall(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Purged || exists(root, ".blueprint") {
		t.Error(".blueprint not purged")
	}
	if exists(root, "CLAUDE.md") {
		t.Error("tool file survived purge")
	}
	for _, r := range res.Remaining {
		if strings.HasPrefix(r.Path, ".blueprint/") {
			t.Errorf("purge reported a .blueprint survivor: %+v", r)
		}
	}
}

func TestUninstallNothingInstalled(t *testing.T) {
	if _, err := Uninstall(t.TempDir(), false); err == nil {
		t.Fatal("uninstall with no manifest must error with remediation")
	}
}

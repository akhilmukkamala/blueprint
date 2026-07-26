package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/core"
)

// seedOldInstall simulates a repo installed by an older template set: the
// managed regions hold old content and the manifest pins their hashes, so the
// current templates constitute a real upgrade.
func seedOldInstall(t *testing.T, root string) {
	t.Helper()
	mustInit(t, root)
	m := mustLoad(t, root)
	for _, rel := range []string{"AGENTS.md", ".blueprint/config.toml"} {
		cur := read(t, root, rel)
		var oldInner string
		if strings.HasSuffix(rel, ".md") {
			oldInner = "old managed guidance from v0.0.1\n"
		} else {
			oldInner = "[router]\nescalate_loc = 200\n"
		}
		updated, ok := replaceManaged(cur, rel, oldInner)
		if !ok {
			t.Fatalf("template %s lost its managed region", rel)
		}
		write(t, root, rel, updated)
		e := m.Files[rel]
		e.SHA256 = hashBytes([]byte(updated))
		e.ManagedSHA256 = hashBytes([]byte(oldInner))
		m.Files[rel] = e
	}
	m.Version = "0.0.1"
	if err := m.Save(root); err != nil {
		t.Fatal(err)
	}
}

func actionsByPath(res *UpgradeResult) map[string]FileUpgrade {
	out := map[string]FileUpgrade{}
	for _, f := range res.Files {
		out[f.Path] = f
	}
	return out
}

// TestUpgradeSeededUserEditCorpus is the AC-3 proof: a brownfield repo with
// user edits in every tier loses zero user-tier bytes across an upgrade.
func TestUpgradeSeededUserEditCorpus(t *testing.T) {
	root := t.TempDir()
	seedOldInstall(t, root)
	for _, rel := range []string{".blueprint/registry.toml", ".blueprint/safety.toml", ".blueprint/verifiers.toml"} {
		if _, err := Materialize(root, rel); err != nil {
			t.Fatal(err)
		}
	}

	// Seed user edits: [user] files rewritten wholesale, [mixed] bodies edited
	// around (not inside) the managed region.
	userEdits := map[string]string{
		".blueprint/registry.toml":  "# my registry\n[[class]]\nname = \"docs\"\nglobs = [\"docs/**\"]\n",
		".blueprint/safety.toml":    "[safety]\nsensitive = [\"src/pay/**\"]\n",
		".blueprint/verifiers.toml": "[[verifier]]\nname = \"t\"\ncommand = \"true\"\napplies_to = [\"*\"]\n",
	}
	for rel, content := range userEdits {
		write(t, root, rel, content)
	}
	agents := read(t, root, "AGENTS.md")
	agentsSplit := splitManaged(agents, "AGENTS.md")
	userTop := "# ACME index — hand-curated, precious bytes: éü🎯\n\n" + agentsSplit.before
	userTail := agentsSplit.after + "\n## Appendix\nkeep me too\n"
	write(t, root, "AGENTS.md", userTop+"<!-- blueprint:managed -->\n"+agentsSplit.inner+"<!-- /blueprint:managed -->\n"+userTail)

	cfg := read(t, root, ".blueprint/config.toml")
	cfgSplit := splitManaged(cfg, ".blueprint/config.toml")
	cfgTail := cfgSplit.after + "\n[my_section]\nmine = true\n"
	write(t, root, ".blueprint/config.toml", cfgSplit.before+"# blueprint:managed\n"+cfgSplit.inner+"# /blueprint:managed\n"+cfgTail)

	res, err := Upgrade(root, UpgradeOptions{})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if res.Conflicts != 0 {
		t.Fatalf("clean corpus produced conflicts: %+v", res.Files)
	}
	got := actionsByPath(res)

	// [user] tier: byte-for-byte identical.
	for rel, want := range userEdits {
		if got[rel].Action != "skip-user" {
			t.Errorf("%s action = %q, want skip-user", rel, got[rel].Action)
		}
		if cur := read(t, root, rel); cur != want {
			t.Errorf("USER-TIER BYTE LOSS in %s:\n got %q\nwant %q", rel, cur, want)
		}
	}

	// [mixed] tier: bodies preserved byte-for-byte, managed region refreshed.
	tplAgents := templateContent(t, "AGENTS.md")
	tplAgentsInner := splitManaged(tplAgents, "AGENTS.md").inner
	newAgents := read(t, root, "AGENTS.md")
	if got["AGENTS.md"].Action != "merge" {
		t.Errorf("AGENTS.md action = %q", got["AGENTS.md"].Action)
	}
	if !strings.HasPrefix(newAgents, userTop) || !strings.HasSuffix(newAgents, userTail) {
		t.Errorf("MIXED-TIER BODY LOSS in AGENTS.md:\n%s", newAgents)
	}
	if sp := splitManaged(newAgents, "AGENTS.md"); sp.inner != tplAgentsInner {
		t.Errorf("managed region not refreshed:\n%q", sp.inner)
	}

	newCfg := read(t, root, ".blueprint/config.toml")
	if !strings.HasPrefix(newCfg, cfgSplit.before) || !strings.HasSuffix(newCfg, cfgTail) {
		t.Errorf("MIXED-TIER BODY LOSS in config.toml:\n%s", newCfg)
	}
	tplCfgInner := splitManaged(templateContent(t, ".blueprint/config.toml"), ".blueprint/config.toml").inner
	if sp := splitManaged(newCfg, ".blueprint/config.toml"); sp.inner != tplCfgInner {
		t.Errorf("config managed region not refreshed:\n%q", sp.inner)
	}

	// [tool] tier: shim is current template.
	if read(t, root, "CLAUDE.md") != templateContent(t, "CLAUDE.md") {
		t.Error("CLAUDE.md not regenerated")
	}
	// Manifest advanced.
	if mustLoad(t, root).Version != TemplatesVersion {
		t.Error("manifest version not bumped")
	}
}

func templateContent(t *testing.T, rel string) string {
	t.Helper()
	tpl, ok := templateByRelPath(rel)
	if !ok {
		t.Fatalf("no template %s", rel)
	}
	b, err := tpl.Content()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeHandEditedManagedRegionConflicts(t *testing.T) {
	root := t.TempDir()
	seedOldInstall(t, root)
	// Hand-edit INSIDE the managed region of AGENTS.md.
	cur := read(t, root, "AGENTS.md")
	edited, ok := replaceManaged(cur, "AGENTS.md", "my sneaky hand edit\n")
	if !ok {
		t.Fatal("no managed region")
	}
	write(t, root, "AGENTS.md", edited)

	res, err := Upgrade(root, UpgradeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", res.Conflicts)
	}
	got := actionsByPath(res)
	if got["AGENTS.md"].Action != "conflict" {
		t.Fatalf("AGENTS.md action = %q", got["AGENTS.md"].Action)
	}
	body := read(t, root, "AGENTS.md")
	for _, marker := range []string{"<<<<<<< local (hand-edited managed region)", "my sneaky hand edit", "=======", ">>>>>>> blueprint upgrade v" + TemplatesVersion} {
		if !strings.Contains(body, marker) {
			t.Errorf("conflict body missing %q:\n%s", marker, body)
		}
	}
	// Both sides live inside the managed region; the user body is intact.
	sp := splitManaged(body, "AGENTS.md")
	if !sp.found || !strings.Contains(sp.inner, "my sneaky hand edit") {
		t.Error("conflict not confined to the managed region")
	}

	// A re-run still flags it: the conflict text is not silently blessed.
	res2, err := Upgrade(root, UpgradeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Conflicts != 1 {
		t.Errorf("re-run conflicts = %d, want 1", res2.Conflicts)
	}
}

func TestUpgradeMissingRegionReappended(t *testing.T) {
	root := t.TempDir()
	seedOldInstall(t, root)
	write(t, root, "AGENTS.md", "only my body, region deleted\n")
	res, err := Upgrade(root, UpgradeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if actionsByPath(res)["AGENTS.md"].Action != "merge" {
		t.Fatalf("action = %+v", actionsByPath(res)["AGENTS.md"])
	}
	body := read(t, root, "AGENTS.md")
	if !strings.HasPrefix(body, "only my body, region deleted\n") {
		t.Errorf("user body lost: %q", body)
	}
	if sp := splitManaged(body, "AGENTS.md"); !sp.found {
		t.Error("managed region not re-appended")
	}
}

func TestUpgradeDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	seedOldInstall(t, root)
	before := map[string]string{}
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".blueprint/config.toml", ".blueprint/manifest.json"} {
		before[rel] = read(t, root, rel)
	}
	res, err := Upgrade(root, UpgradeOptions{DryRun: true, WithDiff: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun {
		t.Error("result not marked dry-run")
	}
	for rel, want := range before {
		if read(t, root, rel) != want {
			t.Errorf("dry run modified %s", rel)
		}
	}
	// Diffs are attached for the merged mixed file.
	if d := actionsByPath(res)["AGENTS.md"].Diff; !strings.Contains(d, "-old managed guidance from v0.0.1") {
		t.Errorf("diff missing old line:\n%s", d)
	}
}

func TestUpgradeRefusesDirtyTree(t *testing.T) {
	root := gitRepo(t)
	seedOldInstall(t, root)
	gitCommitAll(t, root, "chore: install blueprint")
	write(t, root, "dirty.txt", "uncommitted\n")
	if _, err := Upgrade(root, UpgradeOptions{}); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty tree not refused: %v", err)
	}
	// Dry run stays available for a preview.
	if _, err := Upgrade(root, UpgradeOptions{DryRun: true}); err != nil {
		t.Fatalf("dry run refused on dirty tree: %v", err)
	}
	// Clean tree proceeds.
	gitCommitAll(t, root, "chore: dirt")
	if _, err := Upgrade(root, UpgradeOptions{}); err != nil {
		t.Fatalf("clean tree refused: %v", err)
	}
}

func TestUpgradeUntrackedAndMissingFiles(t *testing.T) {
	root := t.TempDir()
	seedOldInstall(t, root)
	// CLAUDE.md deleted by hand: tracked [tool] file is reinstalled.
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	res, err := Upgrade(root, UpgradeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := actionsByPath(res)
	if got["CLAUDE.md"].Action != "reinstall" || !exists(root, "CLAUDE.md") {
		t.Errorf("CLAUDE.md = %+v", got["CLAUDE.md"])
	}
	// safety.toml is floor (user-tier): tracked, never touched by upgrade.
	if got[".blueprint/safety.toml"].Action != "skip-user" {
		t.Errorf("safety.toml = %+v", got[".blueprint/safety.toml"])
	}
	// Never-materialized optional templates are not touched.
	if got[".blueprint/verifiers.toml"].Action != "skip-untracked" || exists(root, ".blueprint/verifiers.toml") {
		t.Errorf("verifiers.toml = %+v", got[".blueprint/verifiers.toml"])
	}
	// Deleted [user] file stays deleted.
	if _, err := Materialize(root, ".blueprint/registry.toml"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".blueprint", "registry.toml")); err != nil {
		t.Fatal(err)
	}
	res2, err := Upgrade(root, UpgradeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if actionsByPath(res2)[".blueprint/registry.toml"].Action != "skip-user" || exists(root, ".blueprint/registry.toml") {
		t.Error("upgrade resurrected a user-deleted file")
	}
}

func TestUpgradeWithoutManifestErrors(t *testing.T) {
	if _, err := Upgrade(t.TempDir(), UpgradeOptions{}); err == nil {
		t.Fatal("upgrade without manifest must error with remediation")
	}
}

func TestUnifiedDiff(t *testing.T) {
	d := unifiedDiff("f", "a\nb\nc\n", "a\nX\nc\n")
	for _, want := range []string{"-b", "+X", " a", " c"} {
		if !strings.Contains(d, want+"\n") {
			t.Errorf("diff missing %q:\n%s", want, d)
		}
	}
	if unifiedDiff("f", "same\n", "same\n") != "" {
		t.Error("identical content produced a diff")
	}
}

func TestTierConstantsRoundTrip(t *testing.T) {
	// Manifest tiers are the shared core vocabulary.
	for _, tier := range []core.OwnershipTier{core.OwnerUser, core.OwnerTool, core.OwnerMixed} {
		if tier == "" {
			t.Fatal("empty tier constant")
		}
	}
}

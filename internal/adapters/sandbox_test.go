package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// --- safety compile: codex + cursor surfaces -------------------------------

func TestCodexSandboxPolicyNonParity(t *testing.T) {
	globs := []string{".env*", "**/secrets/**"}
	doc := string(RenderCodexSandboxPolicy(globs))

	if !IsGenerated([]byte(doc)) {
		t.Error("sandbox policy lacks the provenance marker")
	}
	for _, g := range globs {
		if !strings.Contains(doc, "`"+g+"`") {
			t.Errorf("policy does not list deny glob %q:\n%s", g, doc)
		}
	}
	// Documented non-parity, never fake enforcement: the artifact must say
	// outright that codex cannot enforce the globs...
	flat := strings.Join(strings.Fields(doc), " ")
	for _, phrase := range []string{"NO mechanical equivalent", "not host-enforced", "Do not treat this file as enforcement"} {
		if !strings.Contains(flat, phrase) {
			t.Errorf("policy missing explicit non-parity statement %q:\n%s", phrase, doc)
		}
	}
	// ...and point at the verify-side tamper layer as the backstop.
	if !strings.Contains(doc, "blueprint verify") || !strings.Contains(doc, "tamper") {
		t.Errorf("policy does not name the verify-side tamper backstop:\n%s", doc)
	}
}

func TestCursorSafetyNoteNonParity(t *testing.T) {
	doc := string(RenderCursorSafetyNote([]string{".env*"}))
	if !IsGenerated([]byte(doc)) {
		t.Error("cursor note lacks the provenance marker")
	}
	if !strings.Contains(doc, "no permission, deny-rule, or sandbox surface") {
		t.Errorf("cursor note must state the missing permission surface:\n%s", doc)
	}
	if !strings.Contains(doc, "`.env*`") {
		t.Errorf("cursor note must list the deny globs:\n%s", doc)
	}
	if !strings.Contains(doc, "blueprint verify") {
		t.Errorf("cursor note must point at the verify backstop:\n%s", doc)
	}
}

func TestMergeCodexConfigGuidance(t *testing.T) {
	servers := map[string]MCPServer{"blueprint": {Command: "blueprint", Args: []string{"mcp", "serve"}}}
	globs := []string{".env*"}

	// Fresh file: guidance block appears with network_access = false.
	out, err := MergeCodexConfig(nil, servers, globs)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sandbox map[string]any `toml:"sandbox_workspace_write"`
		Servers map[string]any `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("merged codex config is not valid TOML: %v\n%s", err, out)
	}
	if got, ok := doc.Sandbox["network_access"].(bool); !ok || got {
		t.Errorf("fresh merge should set network_access = false, got %v", doc.Sandbox)
	}
	if _, ok := doc.Servers["blueprint"]; !ok {
		t.Errorf("mcp server entry missing: %v", doc.Servers)
	}
	if !IsGenerated(out) {
		t.Error("merged codex config lacks the provenance marker")
	}

	// Idempotence: merging our own output changes nothing.
	again, err := MergeCodexConfig(out, servers, globs)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(out) {
		t.Errorf("codex merge not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}

	// Guidance never overrides the user: a hand-set network_access = true
	// and foreign keys both survive.
	user := []byte("model = \"o3\"\n\n[sandbox_workspace_write]\nnetwork_access = true\nwritable_roots = [\"/tmp/scratch\"]\n")
	merged, err := MergeCodexConfig(user, servers, globs)
	if err != nil {
		t.Fatal(err)
	}
	var doc2 struct {
		Model   string         `toml:"model"`
		Sandbox map[string]any `toml:"sandbox_workspace_write"`
	}
	if err := toml.Unmarshal(merged, &doc2); err != nil {
		t.Fatal(err)
	}
	if got, _ := doc2.Sandbox["network_access"].(bool); !got {
		t.Errorf("user's network_access = true was clobbered: %v", doc2.Sandbox)
	}
	if _, ok := doc2.Sandbox["writable_roots"]; !ok {
		t.Errorf("user's writable_roots lost: %v", doc2.Sandbox)
	}
	if doc2.Model != "o3" {
		t.Errorf("foreign top-level key lost: %q", doc2.Model)
	}

	// Servers-only merge (no deny globs) emits no sandbox table.
	noSafety, err := MergeCodexConfig(nil, servers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(noSafety), "sandbox_workspace_write") {
		t.Errorf("sandbox table must not appear without deny globs:\n%s", noSafety)
	}
}

// --- AC-4b: switch-tool round-trip -----------------------------------------

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// stripManagedBlock drops the blueprint managed region (markers inclusive)
// from an AGENTS.md body, leaving only the [user]-tier content.
func stripManagedBlock(content string) string {
	const start, end = "<!-- blueprint:managed -->", "<!-- /blueprint:managed -->"
	var out []string
	inBlock := false
	for _, ln := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(ln)
		if !inBlock && trimmed == start {
			inBlock = true
			continue
		}
		if inBlock {
			if trimmed == end {
				inBlock = false
			}
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "")
}

// userTierHashes captures sha256 of every [user]-tier canonical file: all of
// .blueprint/** plus the AGENTS.md body outside the managed block.
func userTierHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, ".blueprint"), func(p string, d fs.DirEntry, err error) error {
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
		out[filepath.ToSlash(rel)] = sha256hex(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	out["AGENTS.md#user-body"] = sha256hex([]byte(stripManagedBlock(string(agents))))
	return out
}

func assertHashesEqual(t *testing.T, phase string, want, got map[string]string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: user-tier file set changed: want %d files, got %d\nwant=%v\ngot=%v",
			phase, len(want), len(got), keys(want), keys(got))
	}
	for p, h := range want {
		if got[p] != h {
			t.Errorf("%s: [user]-tier content of %s changed byte-for-byte (hash %s -> %s)", phase, p, h[:12], got[p][:12])
		}
	}
}

// TestSwitchToolRoundTrip is AC-4b: generate for all tools, sync, sync again
// (idempotence), switch tools by wiping one target's directory and
// regenerating — and at every step the [user]-tier canonical content
// (.blueprint/** + AGENTS.md body outside the managed block) is byte-for-byte
// untouched and `Check` reports clean.
func TestSwitchToolRoundTrip(t *testing.T) {
	root := seedFixtureRepo(t)
	baseline := userTierHashes(t, root)

	// Sync all targets.
	if _, err := Sync(root, mustBuild(t, root)); err != nil {
		t.Fatal(err)
	}
	assertHashesEqual(t, "after first sync", baseline, userTierHashes(t, root))

	// Sync again: idempotent, zero writes, canonical still untouched.
	res2, err := Sync(root, mustBuild(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Written) != 0 || len(res2.BackedUp) != 0 {
		t.Fatalf("second sync not idempotent: written=%v backed_up=%v", res2.Written, res2.BackedUp)
	}
	assertHashesEqual(t, "after second sync", baseline, userTierHashes(t, root))

	// Simulate switching tools: blow away one target's generated surfaces
	// (the user "moves off Cursor", then comes back) and regenerate.
	if err := os.RemoveAll(filepath.Join(root, ".cursor")); err != nil {
		t.Fatal(err)
	}
	res3, err := Sync(root, mustBuild(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Written) == 0 {
		t.Fatal("regeneration after target-dir removal wrote nothing")
	}
	for _, p := range res3.Written {
		if !strings.HasPrefix(p, ".cursor/") {
			t.Errorf("switch-tool regeneration touched a non-cursor surface: %s", p)
		}
	}
	assertHashesEqual(t, "after switch-tool regeneration", baseline, userTierHashes(t, root))

	// Check reports clean on the round-tripped tree.
	drifts, err := Check(root, mustBuild(t, root), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Fatalf("round-tripped tree reports drift: %v", drifts)
	}

	// Drift case: a hand-edit to a generated safety artifact is caught.
	policy := filepath.Join(root, filepath.FromSlash(CodexSandboxPolicyPath))
	if err := os.WriteFile(policy, []byte("<!-- generated by blueprint v0 --> hand-tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifts, err = Check(root, mustBuild(t, root), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range drifts {
		if d.Path == CodexSandboxPolicyPath && d.Reason == "modified" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hand-edited %s not reported as modified: %v", CodexSandboxPolicyPath, drifts)
	}
}

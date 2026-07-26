package releaseci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowPath = "../../.github/workflows/release.yml"

func loadWorkflow(t *testing.T) (raw string, doc map[string]any) {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(workflowPath))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	doc = map[string]any{}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("release.yml is not valid YAML: %v", err)
	}
	return string(b), doc
}

func TestWorkflowParsesAndTriggersOnTags(t *testing.T) {
	_, doc := loadWorkflow(t)
	on, ok := doc["on"].(map[string]any)
	if !ok {
		t.Fatalf("`on` block missing or wrong shape: %T", doc["on"])
	}
	push, ok := on["push"].(map[string]any)
	if !ok {
		t.Fatalf("`on.push` missing: %T", on["push"])
	}
	tags, ok := push["tags"].([]any)
	if !ok || len(tags) == 0 {
		t.Fatalf("`on.push.tags` missing: %v", push["tags"])
	}
	if tags[0] != "v*" {
		t.Errorf("tag trigger = %v, want v*", tags[0])
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok || perms["contents"] != "write" {
		t.Errorf("permissions.contents = %v, want write (gh release needs it)", doc["permissions"])
	}
}

func TestWorkflowActionsAreAllowlistedAndSHAPinned(t *testing.T) {
	raw, _ := loadWorkflow(t)
	uses := regexp.MustCompile(`uses:\s*(\S+)`).FindAllStringSubmatch(raw, -1)
	if len(uses) == 0 {
		t.Fatal("no `uses:` lines found — checkout/setup-go steps missing?")
	}
	pinned := regexp.MustCompile(`^(actions/checkout|actions/setup-go)@[0-9a-f]{40}$`)
	for _, m := range uses {
		if !pinned.MatchString(m[1]) {
			t.Errorf("action %q — CONTRACTS allows only SHA-pinned actions/checkout and actions/setup-go", m[1])
		}
	}
	// The release plumbing must be plain gh CLI, not a third-party action.
	if strings.Contains(raw, "softprops") || strings.Contains(raw, "upload-artifact") {
		t.Error("third-party or artifact actions found — use the gh CLI instead")
	}
}

func TestWorkflowMatrixCoversAllNativePlatforms(t *testing.T) {
	raw, doc := loadWorkflow(t)
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("`jobs` missing")
	}
	for _, name := range []string{"create-release", "build", "publish"} {
		if _, ok := jobs[name]; !ok {
			t.Errorf("job %q missing", name)
		}
	}
	build, _ := jobs["build"].(map[string]any)
	if build == nil {
		t.Fatal("build job missing")
	}
	strat, _ := build["strategy"].(map[string]any)
	matrix, _ := strat["matrix"].(map[string]any)
	include, _ := matrix["include"].([]any)
	if len(include) != 5 {
		t.Fatalf("matrix.include has %d entries, want 5", len(include))
	}
	want := map[string]string{ // runner -> os/arch
		"ubuntu-latest":    "linux/amd64",
		"ubuntu-24.04-arm": "linux/arm64",
		"macos-latest":     "darwin/arm64",
		"macos-15-intel":   "darwin/amd64",
		"windows-latest":   "windows/amd64",
	}
	got := map[string]string{}
	for _, e := range include {
		m, _ := e.(map[string]any)
		if m == nil {
			t.Fatalf("matrix entry wrong shape: %v", e)
		}
		got[str(m["runner"])] = str(m["goos"]) + "/" + str(m["goarch"])
	}
	for runner, platform := range want {
		if got[runner] != platform {
			t.Errorf("runner %s builds %q, want %q", runner, got[runner], platform)
		}
	}
	// cgo: the build must be native — a GOOS/GOARCH cross-compile env is a bug.
	if strings.Contains(raw, "GOOS=") || strings.Contains(raw, "GOARCH=") {
		t.Error("workflow sets GOOS/GOARCH — cgo (tree-sitter) forbids cross builds; use native runners")
	}
	for _, needle := range []string{
		`-trimpath`, `-s -w -X main.version=`,
		"blueprint-offline-", "INSTALL.md", "LICENSE", "licenses.lock", "THIRD_PARTY.md",
		"sha256", "checksums.txt",
	} {
		if !strings.Contains(raw, needle) {
			t.Errorf("workflow is missing %q", needle)
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

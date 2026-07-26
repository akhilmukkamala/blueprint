package verify

// Tests for the bundled verifier pack (verifierpacks feature): the embedded
// template must parse through the real loader, and {{TEST_CMD}} must expand
// from config, fall back to detection, and FAIL with remediation when
// unresolvable — never a false pass.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/install"
)

// writePackFixture writes content at a repo-relative path under root.
func writePackFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundledPack returns the embedded verifiers.toml template bytes.
func bundledPack(t *testing.T) []byte {
	t.Helper()
	for _, tf := range install.Templates() {
		if tf.RelPath == ".blueprint/verifiers.toml" {
			b, err := tf.Content()
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatal("no embedded template for .blueprint/verifiers.toml")
	return nil
}

func TestBundledPackParsesViaLoader(t *testing.T) {
	root := t.TempDir()
	writePackFixture(t, root, ".blueprint/verifiers.toml", string(bundledPack(t)))

	vs, err := loadVerifiers(root)
	if err != nil {
		t.Fatalf("bundled pack failed the real loader: %v", err)
	}
	byName := map[string]Verifier{}
	for _, v := range vs {
		byName[v.Name] = v
	}

	// The five core scenarios are covered by the active entries.
	scenarioCovered := map[string]bool{}
	for _, v := range vs {
		if !strings.Contains(v.Command, testCmdToken) {
			t.Errorf("active pack entry %q must reference %s (repo test commands vary)", v.Name, testCmdToken)
		}
		for _, s := range v.AppliesTo {
			scenarioCovered[s] = true
		}
	}
	tp, ok := byName["tests-pass"]
	if !ok {
		t.Fatal("bundled pack must ship a tests-pass verifier")
	}
	for _, s := range []string{"bug-fix", "feature", "chore"} {
		if !tp.appliesTo(s) {
			t.Errorf("tests-pass must apply to %s", s)
		}
	}
	bp, ok := byName["behavior-preserved"]
	if !ok {
		t.Fatal("bundled pack must ship a behavior-preserved verifier for refactors")
	}
	if !bp.appliesTo("refactor") {
		t.Error("behavior-preserved must apply to refactor")
	}
	// performance is covered by the commented bench-threshold example (it
	// needs repo-specific wiring), so it must NOT auto-run from the pack.
	if scenarioCovered["performance"] {
		t.Error("no active entry should claim performance — the bench gate is a documented, commented example")
	}
}

func TestResolveTestCmd(t *testing.T) {
	t.Run("config wins", func(t *testing.T) {
		root := t.TempDir()
		writePackFixture(t, root, ".blueprint/config.toml", "[test]\ncommand = \"make check\"\n")
		writePackFixture(t, root, "go.mod", "module x\n") // would detect, but config wins
		cmd, src, ok := resolveTestCmd(root)
		if !ok || cmd != "make check" || !strings.Contains(src, "config.toml") {
			t.Errorf("got (%q, %q, %v)", cmd, src, ok)
		}
	})
	t.Run("detection fallback", func(t *testing.T) {
		root := t.TempDir()
		writePackFixture(t, root, "go.mod", "module x\n")
		cmd, src, ok := resolveTestCmd(root)
		if !ok || cmd != "go test ./..." || !strings.Contains(src, "go.mod") {
			t.Errorf("got (%q, %q, %v)", cmd, src, ok)
		}
	})
	t.Run("empty config command still detects", func(t *testing.T) {
		root := t.TempDir()
		writePackFixture(t, root, ".blueprint/config.toml", "[test]\ncommand = \"\"\n")
		writePackFixture(t, root, "Cargo.toml", "[package]\n")
		cmd, _, ok := resolveTestCmd(root)
		if !ok || cmd != "cargo test" {
			t.Errorf("got (%q, %v)", cmd, ok)
		}
	})
	t.Run("nothing resolves", func(t *testing.T) {
		root := t.TempDir()
		if cmd, src, ok := resolveTestCmd(root); ok {
			t.Errorf("empty repo must not resolve, got (%q, %q)", cmd, src)
		}
	})
}

func TestRunVerifierExpandsTestCmd(t *testing.T) {
	root := t.TempDir()
	// "go version" is guaranteed present under `go test` and exits 0.
	writePackFixture(t, root, ".blueprint/config.toml", "[test]\ncommand = \"go version\"\n")
	v := Verifier{Name: "tests-pass", Command: testCmdToken, ReportFormat: "text", AppliesTo: []string{"bug-fix"}}

	res := runVerifier(root, "c1", v, "")
	if !res.Pass {
		t.Fatalf("expanded verifier should pass: %+v", res)
	}
	if !strings.Contains(res.Detail, "go version") || !strings.Contains(res.Detail, "config.toml") {
		t.Errorf("pass detail should name the resolved command and source, got %q", res.Detail)
	}
	report := filepath.Join(root, ".blueprint", "changes", "c1", "verdict", "tests-pass.report.txt")
	if b, err := os.ReadFile(report); err != nil || !strings.Contains(string(b), "go version") {
		t.Errorf("report should hold the command output: err=%v content=%q", err, string(b))
	}
}

func TestRunVerifierUnresolvedTestCmdFailsWithRemediation(t *testing.T) {
	root := t.TempDir() // no config, no detectable build files
	v := Verifier{Name: "tests-pass", Command: testCmdToken, ReportFormat: "text"}

	res := runVerifier(root, "c1", v, "")
	if res.Pass {
		t.Fatal("unresolvable {{TEST_CMD}} must never be a false pass")
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 (did not run)", res.ExitCode)
	}
	for _, want := range []string{"[test]", ".blueprint/config.toml", "{{TEST_CMD}}"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("remediation detail missing %q: %q", want, res.Detail)
		}
	}
}

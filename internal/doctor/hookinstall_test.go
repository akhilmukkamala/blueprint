package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func gitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInstallHooksWritesPolyglotShims(t *testing.T) {
	root := gitFixture(t)
	res, err := InstallHooks(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 2 {
		t.Fatalf("want post-commit and post-merge written, got %+v", res)
	}
	for _, name := range []string{"post-commit", "post-merge"} {
		path := filepath.Join(root, ".git", "hooks", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("hook %s not written: %v", name, err)
		}
		body := string(raw)
		for _, want := range []string{
			hookMarker,
			"blueprint map --refresh --quiet", // the exact refresh command
			":; exit 0",                       // sh exits before the cmd tail
			"@echo off",                       // cmd section (run-hook.cmd polyglot)
			"exit /b 0",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q:\n%s", name, want, body)
			}
		}
		if strings.Contains(body, "\r\n") {
			t.Errorf("%s must use LF endings only", name)
		}
		if runtime.GOOS != "windows" {
			st, _ := os.Stat(path)
			if st.Mode()&0o100 == 0 {
				t.Errorf("%s is not executable: %v", name, st.Mode())
			}
		}
	}
}

func TestInstallHooksIdempotent(t *testing.T) {
	root := gitFixture(t)
	if _, err := InstallHooks(root, false); err != nil {
		t.Fatal(err)
	}
	// Reinstalling over our own shims needs no --force.
	res, err := InstallHooks(root, false)
	if err != nil {
		t.Fatalf("reinstall over blueprint shims must be idempotent: %v", err)
	}
	if len(res.Written) != 2 {
		t.Fatalf("reinstall should rewrite both shims, got %+v", res)
	}
}

func TestInstallHooksRefusesForeignHook(t *testing.T) {
	root := gitFixture(t)
	foreign := "#!/bin/sh\necho user hook\n"
	writeFileT(t, filepath.Join(root, ".git", "hooks", "post-commit"), foreign)

	_, err := InstallHooks(root, false)
	if err == nil {
		t.Fatal("existing non-blueprint hook must refuse the install")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal must name --force as the override, got %q", err.Error())
	}
	// Two-phase: the refusal must leave BOTH hooks untouched (post-merge
	// did not exist and must still not exist).
	raw, _ := os.ReadFile(filepath.Join(root, ".git", "hooks", "post-commit"))
	if string(raw) != foreign {
		t.Errorf("user hook was modified on refusal:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "hooks", "post-merge")); !os.IsNotExist(err) {
		t.Errorf("refusal must not write the other hook either")
	}

	// --force replaces it.
	res, err := InstallHooks(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 2 {
		t.Fatalf("force install should write both hooks, got %+v", res)
	}
	raw, _ = os.ReadFile(filepath.Join(root, ".git", "hooks", "post-commit"))
	if !strings.Contains(string(raw), hookMarker) {
		t.Errorf("force install must replace the foreign hook")
	}
}

func TestInstallHooksOutsideGitRepo(t *testing.T) {
	_, err := InstallHooks(t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("no .git must fail with a git-pointing remediation, got %v", err)
	}
}

func TestInstallHooksWorktree(t *testing.T) {
	// Layout: main/.git/ is the real git dir; wt/ has a .git FILE pointing at
	// main/.git/worktrees/wt, whose commondir names main/.git — hooks must
	// land in main/.git/hooks (shared by all worktrees).
	base := t.TempDir()
	mainGit := filepath.Join(base, "main", ".git")
	wtGitDir := filepath.Join(mainGit, "worktrees", "wt")
	writeFileT(t, filepath.Join(wtGitDir, "commondir"), "../..\n")
	wt := filepath.Join(base, "wt")
	writeFileT(t, filepath.Join(wt, ".git"), "gitdir: "+wtGitDir+"\n")

	res, err := InstallHooks(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(mainGit, "hooks")
	if filepath.Clean(res.HooksDir) != filepath.Clean(want) {
		t.Fatalf("worktree hooks dir = %s, want %s", res.HooksDir, want)
	}
	if _, err := os.Stat(filepath.Join(want, "post-commit")); err != nil {
		t.Fatalf("post-commit not in the shared hooks dir: %v", err)
	}
}

func TestCheckMapFreshnessHook(t *testing.T) {
	// No .git: vacuous pass (doctor fixtures stub git without a .git dir).
	if c := checkMapFreshnessHook(t.TempDir()); !c.Pass {
		t.Errorf("no .git must pass vacuously, got %+v", c)
	}

	root := gitFixture(t)
	c := checkMapFreshnessHook(root)
	if c.Pass {
		t.Fatalf(".git without shims must fail, got %+v", c)
	}
	if !strings.Contains(c.Remediation, "blueprint doctor --install-hooks") {
		t.Errorf("remediation must name the install command, got %q", c.Remediation)
	}

	if _, err := InstallHooks(root, false); err != nil {
		t.Fatal(err)
	}
	if c := checkMapFreshnessHook(root); !c.Pass {
		t.Errorf("after install the check must pass, got %+v", c)
	}

	// A foreign post-commit without our marker still fails the check.
	writeFileT(t, filepath.Join(root, ".git", "hooks", "post-commit"), "#!/bin/sh\necho other\n")
	if c := checkMapFreshnessHook(root); c.Pass {
		t.Errorf("foreign hook without the marker must fail the check, got %+v", c)
	}
}

func TestDoctorRunIncludesMapFreshnessCheck(t *testing.T) {
	root := t.TempDir()
	writeDevEnvRunbook(t, root)
	rep, err := Run(root, stubTools("git@github.com:acme/app.git"))
	if err != nil {
		t.Fatal(err)
	}
	c := checkByName(t, rep, "map-freshness-hook")
	if !c.Pass {
		t.Errorf("fixture without .git must pass vacuously, got %+v", c)
	}
}

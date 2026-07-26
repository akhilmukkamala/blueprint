// hookinstall.go — the repo-map freshness hook (DESIGN §9: the Tier-1 map is
// "regenerated on demand + post-commit hook; freshness is a lint").
// `blueprint doctor --install-hooks` writes post-commit and post-merge shims
// that run `blueprint map --refresh --quiet` when blueprint is on PATH. The
// shim is an extensionless cmd/sh polyglot (the superpowers run-hook.cmd
// scar: Windows hosts mangle .sh, so one file serves both interpreters).
// Existing non-blueprint hooks are never overwritten without --force.
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookMarker identifies a hook file as ours; its presence makes reinstalls
// idempotent and its absence makes an existing file sacred (user-owned).
const hookMarker = "blueprint map-freshness hook"

// hookNames are the git hooks that must keep the map fresh: every commit and
// every merge/pull changes the code the map describes.
var hookNames = []string{"post-commit", "post-merge"}

// hookBody is a cmd batch + sh polyglot (superpowers run-hook.cmd pattern):
// sh treats ":" lines as no-ops and exits before the cmd section; cmd treats
// ":"-prefixed lines as labels and runs the batch tail. Both branches are
// no-ops when blueprint is not on PATH — the hook must never break commits.
const hookBody = ":; # " + hookMarker + " (v1) — polyglot cmd/sh shim, written by `blueprint doctor --install-hooks`\n" +
	":; # Refreshes .blueprint/map.json after each commit/merge (DESIGN §9 Tier-1 freshness).\n" +
	":; command -v blueprint >/dev/null 2>&1 && blueprint map --refresh --quiet >/dev/null 2>&1 || true\n" +
	":; exit 0\n" +
	"@echo off\n" +
	"rem " + hookMarker + " (v1) — cmd section of the polyglot shim\n" +
	"where blueprint >nul 2>nul && blueprint map --refresh --quiet >nul 2>nul\n" +
	"exit /b 0\n"

// HookInstall reports what --install-hooks did.
type HookInstall struct {
	HooksDir string   `json:"hooks_dir"`
	Written  []string `json:"written"`
}

// InstallHooks writes the map-freshness shims into the repo's git hooks
// directory. Existing hook files that are not ours refuse the install unless
// force is set; reinstalling over our own shims is idempotent. The check is
// two-phase — nothing is written if any hook would be refused.
func InstallHooks(repoRoot string, force bool) (*HookInstall, error) {
	hooksDir, err := gitHooksDir(repoRoot)
	if err != nil {
		return nil, err
	}
	if !force {
		for _, name := range hookNames {
			path := filepath.Join(hooksDir, name)
			raw, err := os.ReadFile(path)
			if err != nil {
				continue // absent (or unreadable — surfaces on write)
			}
			if !strings.Contains(string(raw), hookMarker) {
				return nil, fmt.Errorf("refusing to overwrite %s — it exists and is not a blueprint hook; re-run with `blueprint doctor --install-hooks --force` to replace it, or add `blueprint map --refresh --quiet` to your existing hook yourself", path)
			}
		}
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w — check the .git directory is writable", hooksDir, err)
	}
	res := &HookInstall{HooksDir: hooksDir}
	for _, name := range hookNames {
		path := filepath.Join(hooksDir, name)
		if err := os.WriteFile(path, []byte(hookBody), 0o755); err != nil {
			return nil, fmt.Errorf("cannot write %s: %w — check permissions on the hooks directory", path, err)
		}
		res.Written = append(res.Written, path)
	}
	return res, nil
}

// gitHooksDir resolves the hooks directory for repoRoot, following worktree
// `.git` files (gitdir: pointer + commondir) so worktrees install into the
// shared hooks dir like plain checkouts do.
func gitHooksDir(repoRoot string) (string, error) {
	dotGit := filepath.Join(repoRoot, ".git")
	st, err := os.Stat(dotGit)
	if err != nil {
		return "", fmt.Errorf("%s has no .git — run `blueprint doctor --install-hooks` from inside a git repository (`git init` first if needed)", repoRoot)
	}
	if st.IsDir() {
		return filepath.Join(dotGit, "hooks"), nil
	}
	// Worktree: .git is a file "gitdir: <path>".
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", dotGit, err)
	}
	line := strings.TrimSpace(string(raw))
	gd := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if gd == line || gd == "" {
		return "", fmt.Errorf("%s is a file but not a `gitdir:` pointer — repair the worktree (`git worktree repair`) and re-run", dotGit)
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(repoRoot, gd)
	}
	// Hooks live in the common (main) git dir, named by the commondir file.
	if raw, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		cd := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(cd) {
			cd = filepath.Join(gd, cd)
		}
		return filepath.Join(filepath.Clean(cd), "hooks"), nil
	}
	return filepath.Join(gd, "hooks"), nil
}

// checkMapFreshnessHook is the doctor row for the shim: a repo with a .git
// dir but no live map-freshness hook fails with the install command as the
// remediation. Fixture roots without .git pass vacuously (nothing to hook).
func checkMapFreshnessHook(repoRoot string) Check {
	const name = "map-freshness-hook"
	hooksDir, err := gitHooksDir(repoRoot)
	if err != nil {
		return Check{Name: name, Pass: true,
			Detail: "no .git directory — nothing to hook (run inside the repository to check)"}
	}
	var missing []string
	for _, hook := range hookNames {
		raw, err := os.ReadFile(filepath.Join(hooksDir, hook))
		if err != nil || !strings.Contains(string(raw), hookMarker) {
			missing = append(missing, hook)
		}
	}
	if len(missing) > 0 {
		return Check{Name: name, Pass: false,
			Detail:      fmt.Sprintf("no blueprint map-freshness shim in %s (%s) — .blueprint/map.json goes stale silently after every commit", hooksDir, strings.Join(missing, ", ")),
			Remediation: "run `blueprint doctor --install-hooks` — it writes post-commit/post-merge shims that run `blueprint map --refresh --quiet` when blueprint is on PATH (existing hooks are preserved unless you pass --force)"}
	}
	return Check{Name: name, Pass: true,
		Detail: fmt.Sprintf("post-commit and post-merge shims present in %s", hooksDir)}
}

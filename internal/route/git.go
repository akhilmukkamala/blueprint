package route

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// DiffStats is the router's view of the current working diff: what is touched
// and how big it is. LOC counts added+deleted lines (binary files count 0).
type DiffStats struct {
	Paths      []string `json:"paths"`
	ChangedLOC int      `json:"changed_loc"`
}

// GitDiffStats shells out to git for the uncommitted diff against HEAD plus
// untracked files. It is the "or from git diff" input source (DESIGN §4) when
// the user does not declare --paths. Errors carry remediation: the router
// still works with declared paths in a non-git directory.
func GitDiffStats(repoRoot string) (DiffStats, error) {
	var st DiffStats
	seen := map[string]struct{}{}

	numstat, err := gitOut(repoRoot, "diff", "--numstat", "HEAD")
	if err != nil {
		// A repo with no commits has no HEAD; fall back to the empty diff so
		// `blueprint new` still works right after `git init`.
		numstat, err = gitOut(repoRoot, "diff", "--numstat")
		if err != nil {
			return st, fmt.Errorf("router: git diff failed in %s — run inside a git repository or declare touched paths with --paths: %w", repoRoot, err)
		}
	}
	for _, line := range strings.Split(numstat, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		p := strings.Join(fields[2:], " ")
		if frameworkPath(p) {
			continue
		}
		added, _ := strconv.Atoi(fields[0]) // "-" for binary → 0
		deleted, _ := strconv.Atoi(fields[1])
		st.ChangedLOC += added + deleted
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			st.Paths = append(st.Paths, p)
		}
	}

	untracked, err := gitOut(repoRoot, "ls-files", "--others", "--exclude-standard")
	if err == nil {
		for _, p := range strings.Split(untracked, "\n") {
			if p == "" || frameworkPath(p) {
				continue
			}
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				st.Paths = append(st.Paths, p)
			}
		}
	}
	return st, nil
}

func gitOut(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// frameworkPath filters Blueprint's own state out of diff stats: change
// records, journals, and generated adapters must never count as blast radius
// or escalate a tier (an approved light change would otherwise escalate to
// full just because its own .blueprint/ floor is uncommitted).
func frameworkPath(p string) bool {
	return p == "CLAUDE.md" || strings.HasPrefix(p, ".blueprint/") ||
		strings.HasPrefix(p, ".claude/") || strings.HasPrefix(p, ".cursor/") ||
		strings.HasPrefix(p, ".codex/") || p == "AGENTS.md"
}

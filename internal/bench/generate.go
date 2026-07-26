// Suite auto-generation (AC-8, RESEARCH §3.5): the benchmark corpus is built
// from THIS repo's own history, so every task is post-cutoff by construction —
// the model cannot answer from memorized training data. Two sources, in
// deterministic order:
//
//  1. Closed changes in .blueprint/archive/: prompt from the change title +
//     delta REQ texts; expected_files from the change's journaled route paths
//     plus trace-annotated test files (`verifies: REQ-...`); commit_ref from
//     the close-time commit (git log --grep of the change id) or HEAD.
//  2. When archives are sparse (<5 tasks), recent merge/fix commits: prompt
//     from the subject, expected_files from the diff paths, commit_ref the
//     first parent (the state an agent would have faced before the fix).
//
// Tasks are deduped, capped at 25, and classified into query classes for the
// per-class report breakdown.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// GitRunner executes git in repoRoot and returns stdout; injected in tests.
type GitRunner func(repoRoot string, args ...string) (string, error)

func execGit(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

const (
	maxTasks        = 25
	sparseThreshold = 5 // fewer archived tasks than this pulls in git-history tasks
)

// symbolTokenRe: a camelCase/PascalCase identifier with an internal case
// transition, a snake_case identifier, or a call-like token `name()` — the
// signals that a prompt names one concrete symbol.
var symbolTokenRe = regexp.MustCompile(`\b[A-Za-z][a-z0-9]*[A-Z][A-Za-z0-9]*\b|\b[A-Za-z][A-Za-z0-9]*_[A-Za-z0-9_]+\b|\b[A-Za-z][A-Za-z0-9]*\(\)`)

var refactorRe = regexp.MustCompile(`(?i)\b(refactor\w*|renam\w*)\b`)

// TaskClass returns the task's query class: the declared query_class when
// set, otherwise the DESIGN §9 heuristic — single expected file + symbol-like
// prompt token (or expected symbols) → exact-symbol; ≥3 files → cross-file;
// refactor/rename wording → refactor; else conceptual.
func TaskClass(t Task) string {
	if t.QueryClass != "" {
		return t.QueryClass
	}
	switch {
	case len(t.ExpectedFiles) == 1 && (len(t.ExpectedSymbols) > 0 || symbolTokenRe.MatchString(t.Prompt)):
		return ClassExactSymbol
	case len(t.ExpectedFiles) >= 3:
		return ClassCrossFile
	case refactorRe.MatchString(t.Prompt):
		return ClassRefactor
	default:
		return ClassConceptual
	}
}

// GenerateSuite builds the task suite from the repo's archive and git history.
// git == nil uses the real git binary.
func GenerateSuite(repoRoot string, git GitRunner) (*Suite, error) {
	if git == nil {
		git = execGit
	}
	s := &Suite{Name: "retrieval-generated"}
	seenPrompt := map[string]bool{}
	add := func(t Task) {
		if len(s.Tasks) >= maxTasks || t.Prompt == "" || len(t.ExpectedFiles) == 0 || seenPrompt[t.Prompt] {
			return
		}
		seenPrompt[t.Prompt] = true
		sort.Strings(t.ExpectedFiles)
		t.QueryClass = TaskClass(t)
		s.Tasks = append(s.Tasks, t)
	}

	archived, err := archiveTasks(repoRoot, git)
	if err != nil {
		return nil, err
	}
	for _, t := range archived {
		add(t)
	}
	if len(s.Tasks) < sparseThreshold {
		historic, err := historyTasks(repoRoot, git)
		if err != nil {
			return nil, err
		}
		for _, t := range historic {
			add(t)
		}
	}
	if len(s.Tasks) == 0 {
		return nil, fmt.Errorf("nothing to generate from: no closed changes in .blueprint/archive/ and no merge/fix commits in git history — close a change or make commits, or write the suite by hand via `blueprint bench retrieval --init`")
	}
	return s, nil
}

// archivedChange is the minimal slice of an archived change.md the generator
// needs. Parsed locally (not via internal/spec) because the spec loader is
// bound to the active changes/ directory.
type archivedChange struct {
	ID       string
	Title    string
	ReqIDs   []string
	ReqTexts []string
}

var genDeltaHeading = regexp.MustCompile(`^###\s+(ADDED|MODIFIED|REMOVED)\s+(REQ-[A-Za-z0-9-]+-\d+)`)

func parseArchivedChange(id string, raw string) archivedChange {
	c := archivedChange{ID: id, Title: id}
	body := raw
	if rest, ok := strings.CutPrefix(strings.TrimPrefix(raw, "\ufeff"), "+++"); ok {
		if end := strings.Index(rest, "\n+++"); end >= 0 {
			var fm struct {
				Title string `toml:"title"`
			}
			if _, err := toml.Decode(rest[:end], &fm); err == nil && fm.Title != "" {
				c.Title = fm.Title
			}
			body = rest[end+len("\n+++"):]
		}
	}
	// Delta REQ texts: everything between a delta heading and the next
	// heading, minus the verify: block.
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		m := genDeltaHeading.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		c.ReqIDs = append(c.ReqIDs, m[2])
		if m[1] == "REMOVED" {
			continue
		}
		var text []string
		for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "#"); j++ {
			l := strings.TrimSpace(lines[j])
			if l == "verify:" {
				break
			}
			if l != "" {
				text = append(text, l)
			}
		}
		if len(text) > 0 {
			c.ReqTexts = append(c.ReqTexts, strings.Join(text, " "))
		}
	}
	return c
}

func archiveTasks(repoRoot string, git GitRunner) ([]Task, error) {
	dir := filepath.Join(repoRoot, ".blueprint", "archive")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "change.md")); err == nil {
				ids = append(ids, e.Name())
			}
		}
	}
	sort.Strings(ids)

	var tasks []Task
	for _, id := range ids {
		raw, err := os.ReadFile(filepath.Join(dir, id, "change.md"))
		if err != nil {
			continue
		}
		c := parseArchivedChange(id, string(raw))

		prompt := fmt.Sprintf("Locate the code and tests that implement this closed change: %s.", c.Title)
		if len(c.ReqTexts) > 0 {
			prompt += " Requirements: " + strings.Join(c.ReqTexts, " ")
		}

		files := append(routePathsFromLog(filepath.Join(dir, id, "journal.ndjson"), ""),
			routePathsFromLog(filepath.Join(repoRoot, ".blueprint", "log", "worklog.ndjson"), id)...)
		files = append(files, tracedTestFiles(repoRoot, c.ReqIDs)...)
		files = dedupeSorted(files)
		if len(files) == 0 {
			continue // success must be checkable
		}

		ref := closeCommit(repoRoot, git, id)
		if ref == "" {
			continue // no git history at all — cannot pin a commit
		}
		tasks = append(tasks, Task{
			ID:            id,
			Prompt:        prompt,
			ExpectedFiles: files,
			CommitRef:     ref,
		})
	}
	return tasks, nil
}

// closeCommit finds the commit that mentions the change id (the close-time
// commit), falling back to HEAD.
func closeCommit(repoRoot string, git GitRunner, id string) string {
	if out, err := git(repoRoot, "log", "--grep", id, "-n", "1", "--format=%H"); err == nil {
		if sha := strings.TrimSpace(firstLine(out)); sha != "" {
			return sha
		}
	}
	out, err := git(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// routeEvent is the slice of a journal line the generator reads (same ndjson
// shape as core.JournalEvent; decoded locally so bench stays decoupled).
type routeEvent struct {
	Kind     string `json:"kind"`
	ChangeID string `json:"change_id"`
	Data     struct {
		Paths []string `json:"paths"`
	} `json:"data"`
}

// routePathsFromLog reads one append-only ndjson log tolerantly (corrupt lines
// are skipped, missing file = empty history) and returns the paths of every
// route/route-escalate event; changeID != "" filters to that change (the
// worklog holds every change's routing decisions).
func routePathsFromLog(path, changeID string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev routeEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Kind != "route" && ev.Kind != "route-escalate" {
			continue
		}
		if changeID != "" && ev.ChangeID != changeID {
			continue
		}
		out = append(out, ev.Data.Paths...)
	}
	return out
}

var genTestFilePatterns = []string{
	"*_test.go", "test_*.py", "*_test.py",
	"*.test.js", "*.test.ts", "*.spec.js", "*.spec.ts",
	"*_spec.rb", "*Test.java", "*Tests.cs",
}

var genSkipDirs = map[string]bool{
	".git": true, ".blueprint": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, "target": true, ".venv": true, "__pycache__": true,
}

// tracedTestFiles walks the repo for test files whose `verifies: REQ-...`
// annotations reference any of reqIDs (slash-separated repo-relative paths,
// sorted by the caller's dedupe).
func tracedTestFiles(repoRoot string, reqIDs []string) []string {
	if len(reqIDs) == 0 {
		return nil
	}
	var out []string
	_ = filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, generation stays best-effort
		}
		if info.IsDir() {
			if genSkipDirs[info.Name()] && path != repoRoot {
				return filepath.SkipDir
			}
			return nil
		}
		match := false
		for _, p := range genTestFilePatterns {
			if ok, _ := filepath.Match(p, info.Name()); ok {
				match = true
				break
			}
		}
		if !match {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, id := range reqIDs {
			if strings.Contains(string(data), "verifies: "+id) {
				if rel, err := filepath.Rel(repoRoot, path); err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
				break
			}
		}
		return nil
	})
	return out
}

var fixSubjectRe = regexp.MustCompile(`(?i)\b(fix(es|ed)?|merge[ds]?)\b`)

// historyTasks builds tasks from recent merge/fix commits: prompt from the
// subject, expected_files from the diff, commit_ref the FIRST PARENT — the
// pre-fix state an agent would actually retrieve against.
func historyTasks(repoRoot string, git GitRunner) ([]Task, error) {
	out, err := git(repoRoot, "log", "-n", "50", "--format=%H%x1f%P%x1f%s")
	if err != nil {
		return nil, nil // no git history is not an error; archives may carry the suite
	}
	var tasks []Task
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		sha, parents, subject := parts[0], strings.Fields(parts[1]), strings.TrimSpace(parts[2])
		if len(parents) == 0 || subject == "" || !fixSubjectRe.MatchString(subject) {
			continue
		}
		diff, err := git(repoRoot, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
		if err != nil {
			continue
		}
		var files []string
		for _, f := range strings.Split(strings.TrimSpace(diff), "\n") {
			if f = strings.TrimSpace(f); f != "" {
				files = append(files, filepath.ToSlash(f))
			}
		}
		if len(files) == 0 {
			continue
		}
		short := sha
		if len(short) > 12 {
			short = short[:12]
		}
		tasks = append(tasks, Task{
			ID:            "g-" + short,
			Prompt:        fmt.Sprintf("Locate the code that had to change for this fix: %s", subject),
			ExpectedFiles: files,
			CommitRef:     parents[0],
		})
	}
	return tasks, nil
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = filepath.ToSlash(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// WriteGeneratedSuite writes the suite as TOML; it refuses to overwrite so a
// hand-tuned suite is never clobbered by regeneration.
func WriteGeneratedSuite(path string, s *Suite) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("task suite %s already exists — delete it first if you want to regenerate, or pass --tasks <other-path>", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create %s — check directory permissions: %w", filepath.Dir(path), err)
	}
	var b strings.Builder
	b.WriteString("# Generated by `blueprint bench retrieval --generate` from this repo's own\n")
	b.WriteString("# history (archived changes + merge/fix commits) — post-cutoff by construction.\n")
	b.WriteString("# Edit freely; regeneration refuses to overwrite this file.\n")
	if err := toml.NewEncoder(&b).Encode(s); err != nil {
		return fmt.Errorf("encode generated suite: %w", err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Package doctor is the environment health check (DESIGN §14): git + forge
// probe (written into autonomy.json), binary-on-PATH, dev-env runbook,
// hooks-liveness, safety-deny-rules (the safety compile actually reached
// settings.json), and the per-stage adoption exit checks. Every failing check
// carries a remediation instruction — doctor is a linter that teaches, not a
// status dump.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"blueprint/internal/autonomy"
	"blueprint/internal/core"
)

// Check is one row of the doctor table.
type Check struct {
	Name        string `json:"check"`
	Pass        bool   `json:"pass"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// Report aggregates a doctor run. Pass is the AND of all checks.
type Report struct {
	Checks  []Check                  `json:"checks"`
	Profile *core.EnforcementProfile `json:"profile,omitempty"`
	Pass    bool                     `json:"pass"`
}

// Options carries the injectable impurities (PATH lookup, git execution) so
// tests run against fixture repos deterministically, plus the two flags.
type Options struct {
	AdoptStage int  // -1 = no adoption stage check
	RunDevEnv  bool // actually execute the dev-env runbook's first code block

	LookPath func(file string) (string, error)                     // default exec.LookPath
	Git      func(repoRoot string, args ...string) (string, error) // default real git
}

func (o Options) lookPath(file string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath(file)
	}
	return exec.LookPath(file)
}

func (o Options) git(repoRoot string, args ...string) (string, error) {
	if o.Git != nil {
		return o.Git(repoRoot, args...)
	}
	out, err := exec.Command("git", append([]string{"-C", repoRoot}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// Run executes every check against repoRoot. It returns a Report even when
// checks fail — the non-zero exit is the CLI's job, the table is doctor's.
func Run(repoRoot string, opts Options) (*Report, error) {
	r := &Report{}

	gitOK := r.add(checkGit(repoRoot, opts))
	if gitOK {
		c, profile := checkForge(repoRoot, opts)
		r.add(c)
		r.Profile = profile
	} else {
		r.add(Check{Name: "forge", Pass: false,
			Detail:      "skipped: git check failed",
			Remediation: "fix the git check first — forge detection reads `git remote get-url origin`"})
	}
	r.add(checkBinaryOnPath(opts))
	r.add(checkDevEnvRunbook(repoRoot, opts))
	r.add(checkHooksLiveness(repoRoot, opts))
	r.add(checkSafetyDenyRules(repoRoot))
	r.add(checkMapFreshnessHook(repoRoot))
	r.add(checkRetrievalTiers(repoRoot, opts))

	if opts.AdoptStage >= 0 {
		checks, err := adoptStageChecks(repoRoot, opts)
		if err != nil {
			return nil, err
		}
		for _, c := range checks {
			r.add(c)
		}
	}

	r.Pass = true
	for _, c := range r.Checks {
		r.Pass = r.Pass && c.Pass
	}
	return r, nil
}

func (r *Report) add(c Check) bool {
	r.Checks = append(r.Checks, c)
	return c.Pass
}

func checkGit(repoRoot string, opts Options) Check {
	if _, err := opts.lookPath("git"); err != nil {
		return Check{Name: "git", Pass: false,
			Detail:      "git binary not found on PATH",
			Remediation: "install git and ensure it is on PATH — blueprint's router, tamper evidence, and autonomy recompute all shell out to git"}
	}
	out, err := opts.git(repoRoot, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return Check{Name: "git", Pass: false,
			Detail:      fmt.Sprintf("%s is not inside a git work tree", repoRoot),
			Remediation: "run `git init` (or run blueprint from inside the repository) — journals and specs must live in version control"}
	}
	return Check{Name: "git", Pass: true, Detail: "git on PATH; repository detected"}
}

// checkForge detects the forge from the origin URL and records the profile
// into .blueprint/autonomy.json (preserving class state). An unknown forge is
// a passing check with an advisory profile — absence of a forge is a state,
// not a fault; the notes say what to verify manually.
func checkForge(repoRoot string, opts Options) (Check, *core.EnforcementProfile) {
	url, err := opts.git(repoRoot, "remote", "get-url", "origin")
	if err != nil {
		url = ""
	}
	p := DetectForge(url)
	if err := autonomy.SetProfile(repoRoot, p); err != nil {
		return Check{Name: "forge", Pass: false,
			Detail:      err.Error(),
			Remediation: "fix .blueprint/autonomy.json (tool-owned; restore from git history if corrupt) so the forge profile can be recorded"}, nil
	}
	mode := "advisory"
	if p.Enforced {
		mode = "enforced"
	}
	return Check{Name: "forge", Pass: true,
		Detail:      fmt.Sprintf("forge=%s (%s) — %s", p.Forge, mode, p.Notes),
		Remediation: ""}, &p
}

func checkBinaryOnPath(opts Options) Check {
	if _, err := opts.lookPath("blueprint"); err != nil {
		return Check{Name: "binary-on-path", Pass: false,
			Detail:      "`blueprint` not found on PATH",
			Remediation: "put the blueprint binary on PATH — loop predicate commands and CI hooks invoke `blueprint` by name and fail silently without it"}
	}
	return Check{Name: "binary-on-path", Pass: true, Detail: "blueprint binary found on PATH"}
}

func devEnvRunbookPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "knowledge", "runbooks", "dev-env.md")
}

var fencedBlockRe = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")

// checkDevEnvRunbook verifies the dev-env runbook exists and carries a fenced
// code block; the block is only executed under --run-dev-env (running an
// arbitrary environment command is opt-in, not a side effect of a health
// check).
func checkDevEnvRunbook(repoRoot string, opts Options) Check {
	path := devEnvRunbookPath(repoRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "dev-env-runbook", Pass: false,
			Detail:      fmt.Sprintf("%s missing", filepath.Join(".blueprint", "knowledge", "runbooks", "dev-env.md")),
			Remediation: "create .blueprint/knowledge/runbooks/dev-env.md with the command that brings up a dev environment in a fenced code block (adoption stage 1 deliverable)"}
	}
	m := fencedBlockRe.FindSubmatch(raw)
	if m == nil {
		return Check{Name: "dev-env-runbook", Pass: false,
			Detail:      "dev-env.md has no fenced code block",
			Remediation: "add the dev-env command inside a ``` fenced code block — doctor executes the first block to prove the runbook is live, not prose"}
	}
	if !opts.RunDevEnv {
		return Check{Name: "dev-env-runbook", Pass: true,
			Detail: "runbook exists with a fenced code block (run skipped — pass --run-dev-env to execute it)"}
	}
	script := strings.TrimSpace(string(m[1]))
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", script)
	} else {
		cmd = exec.Command("sh", "-c", script)
	}
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return Check{Name: "dev-env-runbook", Pass: false,
			Detail:      fmt.Sprintf("first fenced block exited non-zero: %v — output: %s", err, firstLine(string(out))),
			Remediation: "fix the dev-env runbook so its first fenced code block runs with exit 0 — a runbook that does not execute is documentation drift"}
	}
	return Check{Name: "dev-env-runbook", Pass: true, Detail: "first fenced code block ran with exit 0"}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

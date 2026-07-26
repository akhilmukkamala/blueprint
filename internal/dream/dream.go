// Package dream is the manual self-improvement pass (DESIGN §12, AC-10):
// `blueprint dream` folds the worklog, every change journal (active and
// archived), and verdict history since the last dream event into evidence-
// cited signals, optionally consolidates them through a user-configured
// external [dream] command, and emits at most one small proposal branch —
// agent/dream/<date> — carrying proposal.md plus git-apply-able patch files
// for any [user]-tier change. Invariants: hard no-op when signal is absent
// (nothing written, exit 0); [user]-tier files are never machine-edited;
// every write passes the secret scrubber; quarantined (untrusted-provenance)
// signals never become patches; merges are human-only. Scheduling stays a
// documentation recipe — the command itself is manual.
package dream

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// Options controls a dream run.
type Options struct {
	// Branch=true creates agent/dream/<date> with the committed proposal;
	// false is a dry run that prints the proposal and writes NOTHING.
	Branch bool
	// Now is injectable for tests; nil means time.Now. The value is only used
	// for the proposal date and the journaled dream event (CONTRACTS rule 5:
	// wall clock only as explicit journal timestamps).
	Now func() time.Time
}

// Result is the --json output of `blueprint dream`.
type Result struct {
	Date         string    `json:"date"`
	Since        time.Time `json:"since,omitzero"`
	NoSignal     bool      `json:"no_signal"`
	DryRun       bool      `json:"dry_run,omitempty"`
	ModelUsed    bool      `json:"model_used,omitempty"`
	Signals      []Signal  `json:"signals,omitempty"`
	Items        []Item    `json:"items,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	ProposalPath string    `json:"proposal_path,omitempty"` // repo-relative, on the branch
	PatchPaths   []string  `json:"patch_paths,omitempty"`   // repo-relative, on the branch
	Proposal     string    `json:"proposal,omitempty"`      // rendered markdown (dry runs)
	Scrubbed     []string  `json:"scrubbed,omitempty"`      // scrub rules that fired
	Warnings     []string  `json:"warnings,omitempty"`
	Instructions string    `json:"instructions,omitempty"`
}

// Dir is the on-branch proposal folder for a date.
func Dir(repoRoot, date string) string {
	return filepath.Join(repoRoot, ".blueprint", "dream", date)
}

// BranchName is the dream branch for a date (safety branch namespace:
// unattended writes live under agent/**, DESIGN §13).
func BranchName(date string) string { return "agent/dream/" + date }

// Run executes the dream pipeline. It returns a NoSignal result — having
// written nothing — when extraction finds no signal (AC-10 hard no-op).
func Run(repoRoot string, opts Options) (*Result, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	stamp := now().UTC()
	date := stamp.Format("2006-01-02")

	signals, since, err := ExtractSignals(repoRoot)
	if err != nil {
		return nil, err
	}
	res := &Result{Date: date, Since: since}
	if len(signals) == 0 {
		res.NoSignal = true
		return res, nil
	}
	res.Signals = signals
	res.Branch = BranchName(date)

	// Stage 2: model consolidation via the [dream] hook; deterministic-only
	// when unconfigured; deterministic fallback (with a warning) on any hook
	// failure — dream output is advisory and must not hard-fail on a flaky
	// external command.
	cfg, err := loadDreamConfig(repoRoot)
	if err != nil {
		return nil, err
	}
	var items []Item
	if cfg.Command != "" {
		packet := buildModelPacket(repoRoot, date, signals, cfg.MaxUSD)
		modelItems, err := runModel(repoRoot, cfg.Command, packet)
		if err == nil {
			items, res.Warnings = consolidate(modelItems, signals)
			res.ModelUsed = true
		} else {
			res.Warnings = append(res.Warnings, "dream: "+err.Error()+" — falling back to deterministic proposals")
		}
	}
	if !res.ModelUsed {
		var warns []string
		items, warns = buildDeterministicItems(repoRoot, date, signals)
		res.Warnings = append(res.Warnings, warns...)
	}
	if len(items) == 0 {
		// A model that returns zero valid items must not erase real signal.
		var warns []string
		items, warns = buildDeterministicItems(repoRoot, date, signals)
		res.Warnings = append(res.Warnings, warns...)
		res.ModelUsed = false
	}

	// Stage 4 safety: quarantine + 40-line cap + stable IDs, then the secret
	// scrubber over everything that could reach disk.
	res.Items = enforceItems(items, date)
	for i := range res.Items {
		it := &res.Items[i]
		var fired []string
		it.Body, fired = Scrub(it.Body)
		res.Scrubbed = mergeFired(res.Scrubbed, fired)
		it.Patch, fired = Scrub(it.Patch)
		res.Scrubbed = mergeFired(res.Scrubbed, fired)
	}
	proposal, fired := Scrub(renderProposal(res))
	res.Scrubbed = mergeFired(res.Scrubbed, fired)

	relDir := ".blueprint/dream/" + date
	res.ProposalPath = relDir + "/proposal.md"
	for _, it := range res.Items {
		if it.PatchPath != "" {
			res.PatchPaths = append(res.PatchPaths, relDir+"/"+it.PatchPath)
		}
	}
	res.Instructions = fmt.Sprintf(
		"review the proposal, then: git push -u origin %s && open a PR — human merge only (AC-10); after merge, apply [user]-tier patches with `git apply %s/patches/<item>.patch`",
		res.Branch, relDir)

	if !opts.Branch {
		res.DryRun = true
		res.Proposal = proposal
		return res, nil // print-only: nothing written, nothing journaled
	}

	if err := writeBranch(repoRoot, res, proposal); err != nil {
		return nil, err
	}

	// Journal the dream event so the next run's "since" boundary moves.
	ev := core.JournalEvent{
		Time: stamp,
		Kind: "dream",
		Data: map[string]any{"date": date, "items": len(res.Items), "branch": res.Branch},
	}
	if err := worklog.Append(repoRoot, ev); err != nil {
		return nil, err
	}
	return res, nil
}

// mergeFired accumulates scrub-rule names without duplicates.
func mergeFired(acc, fired []string) []string {
	for _, f := range fired {
		seen := false
		for _, a := range acc {
			if a == f {
				seen = true
				break
			}
		}
		if !seen {
			acc = append(acc, f)
		}
	}
	return acc
}

// writeBranch creates agent/dream/<date> in a throwaway worktree (the user's
// checkout is never switched), writes proposal.md + patch files, and commits.
// The branch must not already exist: one dream PR per date, refuse rather
// than overwrite.
func writeBranch(repoRoot string, res *Result, proposal string) error {
	if _, err := gitOut(repoRoot, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return fmt.Errorf("dream: %s is not a git repository with at least one commit — the proposal ships as a branch, so commit your repo first (or use --branch=false for a print-only run)", repoRoot)
	}
	if _, err := gitOut(repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+res.Branch); err == nil {
		return fmt.Errorf("dream: branch %s already exists — today's proposal was already generated; review/push that branch, or delete it (`git branch -D %s`) to regenerate", res.Branch, res.Branch)
	}

	tmp, err := os.MkdirTemp("", "blueprint-dream-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "wt")
	if _, err := gitOut(repoRoot, "worktree", "add", "-b", res.Branch, wt); err != nil {
		return fmt.Errorf("dream: cannot create worktree for %s: %w", res.Branch, err)
	}
	defer func() {
		_, _ = gitOut(repoRoot, "worktree", "remove", "--force", wt)
		_, _ = gitOut(repoRoot, "worktree", "prune")
	}()

	dir := Dir(wt, res.Date)
	if err := os.MkdirAll(filepath.Join(dir, "patches"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "proposal.md"), []byte(proposal), 0o644); err != nil {
		return err
	}
	for _, it := range res.Items {
		if it.PatchPath == "" {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(it.PatchPath))
		if err := os.WriteFile(p, []byte(it.Patch), 0o644); err != nil {
			return err
		}
	}

	if _, err := gitOut(wt, "add", ".blueprint/dream"); err != nil {
		return err
	}
	msg := fmt.Sprintf("dream: %s proposal (%d items)\n\nMachine-generated self-improvement proposal (`blueprint dream`, DESIGN §12).\nHuman merge only (AC-10); [user]-tier changes ship as patch files, never applied.\n\nCo-Authored-By: blueprint dream <dream@blueprint.invalid>",
		res.Date, len(res.Items))
	if _, err := gitOut(wt,
		"-c", "user.name=blueprint dream",
		"-c", "user.email=dream@blueprint.invalid",
		"commit", "--no-verify", "--no-gpg-sign", "-m", msg); err != nil {
		return fmt.Errorf("dream: commit on %s failed: %w", res.Branch, err)
	}
	return nil
}

// gitOut runs git in dir and returns trimmed stdout; stderr rides the error.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

package loop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueprint/internal/core"
)

// resumeJournalTail is the DESIGN §6 resume ritual's journal window.
const resumeJournalTail = 20

// TaskGuard is the re-dispatch guard verdict for one task: if a task is
// already recorded complete (change.md checkbox, journal, or git log), resume
// refuses to re-dispatch it and lists the evidence — "trust the ledger over
// recollection" (DESIGN §6).
type TaskGuard struct {
	TaskID   string   `json:"task_id"`
	Text     string   `json:"text"`
	Refuse   bool     `json:"refuse"`
	Evidence []string `json:"evidence,omitempty"`
}

// ResumeContext is the deterministic, ordered context pack (DESIGN §6 resume
// ritual). Field order is the reading order: AGENTS.md, change.md,
// state.json, last 20 journal events, git sanity — prefixed by the
// predicate-first check.
type ResumeContext struct {
	ChangeID        string              `json:"change_id"`
	PredicateCmd    string              `json:"predicate_cmd,omitempty"`
	PredicateGreen  bool                `json:"predicate_green"`
	PredicateOutput string              `json:"predicate_output,omitempty"`
	AgentsMD        string              `json:"agents_md"`
	ChangeMD        string              `json:"change_md"`
	State           *State              `json:"state"`
	JournalTail     []core.JournalEvent `json:"journal_tail"`
	GitStatus       string              `json:"git_status"`
	GitDiffStat     string              `json:"git_diff_stat"`
	Guards          []TaskGuard         `json:"redispatch_guards"`
}

// runCommand abstracts predicate/git execution so tests can stub it.
// Commands are split on whitespace and run directly — no shell — which keeps
// predicates Windows-clean and unquotable by design.
type runCommand func(dir string, argv ...string) (output string, exitCode int, err error)

var execCommand runCommand = func(dir string, argv ...string) (string, int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode(), nil
		}
		return string(out), -1, err
	}
	return string(out), 0, nil
}

// BuildResumeContext assembles the cold-start-proof resume pack for a change.
// Predicate-first: the contract predicate runs before anything else; if it
// exits 0 the change is already done — state is marked done and the pack says
// so instead of priming a resume. Never reads the raw prior transcript.
func BuildResumeContext(repoRoot, id string) (*ResumeContext, error) {
	change, err := Source().LoadChange(repoRoot, id)
	if err != nil {
		return nil, err
	}
	state, err := LoadState(repoRoot, id)
	if err != nil {
		return nil, err
	}
	events, err := ReadJournal(repoRoot, id)
	if err != nil {
		return nil, err
	}

	rc := &ResumeContext{
		ChangeID:     id,
		PredicateCmd: strings.TrimSpace(change.Contract.Predicate),
		State:        state,
		JournalTail:  LastEvents(events, resumeJournalTail),
	}

	// (a) Predicate-first: a loop that does not know it is done burns tokens.
	if rc.PredicateCmd != "" {
		argv := strings.Fields(rc.PredicateCmd)
		out, code, execErr := execCommand(repoRoot, argv...)
		rc.PredicateOutput = strings.TrimSpace(out)
		if execErr != nil {
			rc.PredicateOutput = fmt.Sprintf("predicate %q could not run: %v — fix the [loop] predicate command in change.md", rc.PredicateCmd, execErr)
		} else if code == 0 {
			rc.PredicateGreen = true
			now := time.Now().UTC()
			_ = AppendEvent(repoRoot, id, core.JournalEvent{
				Time: now, Kind: KindResume, ChangeID: id,
				Data: map[string]any{"predicate_done": true},
			})
			state.Phase = PhaseDone
			state.Summary = fmt.Sprintf("predicate %q exited 0 at resume — change is done, nothing to resume", rc.PredicateCmd)
			state.UpdatedAt = now
			if err := SaveState(repoRoot, state); err != nil {
				return nil, err
			}
			return rc, nil
		}
	}

	// Ordered context pack.
	rc.AgentsMD = readOrNote(filepath.Join(repoRoot, "AGENTS.md"))
	rc.ChangeMD = readOrNote(Source().ChangePath(repoRoot, id))
	rc.GitStatus, rc.GitDiffStat = gitSanity(repoRoot, id)

	// (b) Re-dispatch guard over the ledger, not recollection.
	rc.Guards = redispatchGuards(repoRoot, id, change.Tasks, events)

	_ = AppendEvent(repoRoot, id, core.JournalEvent{
		Time: time.Now().UTC(), Kind: KindResume, ChangeID: id,
		Data: map[string]any{"predicate_done": false},
	})
	return rc, nil
}

// gitSanity captures `git status` plus a diff stat against the change branch
// (agent/<id>) when it exists, else the working-tree diff stat.
func gitSanity(repoRoot, id string) (status, diffStat string) {
	out, code, err := execCommand(repoRoot, "git", "status", "--porcelain")
	if err != nil || code != 0 {
		status = "(git status unavailable — is this a git repository?)"
	} else {
		status = strings.TrimRight(out, "\n")
	}
	branch := "agent/" + id
	if _, code, err := execCommand(repoRoot, "git", "rev-parse", "--verify", "--quiet", branch); err == nil && code == 0 {
		out, code, err := execCommand(repoRoot, "git", "diff", "--stat", branch)
		if err == nil && code == 0 {
			diffStat = strings.TrimRight(out, "\n")
			return
		}
	}
	out, code, err = execCommand(repoRoot, "git", "diff", "--stat")
	if err != nil || code != 0 {
		diffStat = "(git diff unavailable)"
		return
	}
	diffStat = strings.TrimRight(out, "\n")
	return
}

// redispatchGuards marks a task refused when any ledger records completion:
// its change.md checkbox, a journal task-done event, or a git log commit
// mentioning the task ID.
func redispatchGuards(repoRoot, id string, tasks []core.Task, events []core.JournalEvent) []TaskGuard {
	journalDone := map[string][]string{}
	for _, ev := range events {
		if ev.Kind != KindTask || dataString(ev, "status") != "done" {
			continue
		}
		tid := dataString(ev, "task_id")
		if tid == "" {
			continue
		}
		journalDone[tid] = append(journalDone[tid],
			fmt.Sprintf("journal: task %s recorded done at %s", tid, stamp(ev)))
	}
	guards := make([]TaskGuard, 0, len(tasks))
	for _, t := range tasks {
		g := TaskGuard{TaskID: t.ID, Text: t.Text}
		if t.Done {
			g.Evidence = append(g.Evidence, fmt.Sprintf("change.md marks task %s as done ([x])", t.ID))
		}
		g.Evidence = append(g.Evidence, journalDone[t.ID]...)
		if out, code, err := execCommand(repoRoot, "git", "log", "--oneline", "--fixed-strings", "--grep", t.ID); err == nil && code == 0 {
			if lines := strings.TrimSpace(out); lines != "" {
				for _, l := range strings.Split(lines, "\n") {
					g.Evidence = append(g.Evidence, "git log: "+strings.TrimSpace(l))
				}
			}
		}
		g.Refuse = len(g.Evidence) > 0
		guards = append(guards, g)
	}
	return guards
}

func readOrNote(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("(%s not found)", filepath.Base(path))
		}
		return fmt.Sprintf("(%s unreadable: %v)", filepath.Base(path), err)
	}
	return string(raw)
}

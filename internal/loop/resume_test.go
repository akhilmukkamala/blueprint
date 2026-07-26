package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueprint/internal/core"
)

// stubExec replaces command execution for hermetic resume tests; restore is
// automatic via t.Cleanup.
func stubExec(t *testing.T, fn runCommand) {
	t.Helper()
	prev := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = prev })
}

// fakeGit answers the git calls gitSanity/redispatchGuards make; predicate
// commands are routed through predicateCode.
func fakeGit(predicateCode int, gitLog string) runCommand {
	return func(dir string, argv ...string) (string, int, error) {
		if argv[0] != "git" {
			return "predicate ran", predicateCode, nil
		}
		switch argv[1] {
		case "status":
			return " M src/a.go\n", 0, nil
		case "rev-parse":
			return "", 1, nil // no agent/<id> branch
		case "diff":
			return " src/a.go | 4 ++--\n", 0, nil
		case "log":
			return gitLog, 0, nil
		}
		return "", 0, nil
	}
}

func TestResumePredicateFirstShortCircuits(t *testing.T) {
	root := t.TempDir()
	installFixtureChange(t, root, "c1")
	stubExec(t, fakeGit(0, "")) // predicate exit 0 -> done

	rc, err := BuildResumeContext(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !rc.PredicateGreen {
		t.Fatal("predicate exit 0 must mark done")
	}
	if rc.AgentsMD != "" || rc.ChangeMD != "" {
		t.Error("done pack should not prime a resume with context")
	}
	s, _ := LoadState(root, "c1")
	if s.Phase != PhaseDone {
		t.Errorf("phase = %q, want done", s.Phase)
	}
	evs, _ := ReadJournal(root, "c1")
	if len(evs) == 0 || evs[len(evs)-1].Kind != KindResume {
		t.Error("resume must journal the predicate-done check")
	}
}

func TestResumeBuildsOrderedPack(t *testing.T) {
	root := t.TempDir()
	installFixtureChange(t, root, "c1")
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err := AppendEvent(root, "c1", iter(i, i+1, fmt.Sprintf("step %d", i), 1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveState(root, &State{ChangeID: "c1", Phase: PhaseRunning, Task: "T2", Iteration: 7}); err != nil {
		t.Fatal(err)
	}
	stubExec(t, fakeGit(1, "")) // predicate red -> full pack

	rc, err := BuildResumeContext(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if rc.PredicateGreen {
		t.Fatal("red predicate must not mark done")
	}
	if !strings.Contains(rc.AgentsMD, "Agents index") {
		t.Errorf("AgentsMD = %q", rc.AgentsMD)
	}
	if !strings.Contains(rc.ChangeMD, "rate limiting") {
		t.Error("ChangeMD missing change.md content")
	}
	if rc.State.Task != "T2" {
		t.Errorf("state = %+v", rc.State)
	}
	if len(rc.JournalTail) != resumeJournalTail {
		t.Errorf("journal tail = %d events, want %d", len(rc.JournalTail), resumeJournalTail)
	}
	if rc.JournalTail[0].Data["action"] != "step 5" {
		t.Errorf("tail must be the LAST 20 events, first = %v", rc.JournalTail[0].Data["action"])
	}
	if !strings.Contains(rc.GitStatus, "src/a.go") || !strings.Contains(rc.GitDiffStat, "src/a.go") {
		t.Errorf("git sanity missing: status=%q diff=%q", rc.GitStatus, rc.GitDiffStat)
	}
}

func TestRedispatchGuardRefusesCompletedTasks(t *testing.T) {
	root := t.TempDir()
	installFixtureChange(t, root, "c1") // T1 checked done in change.md
	if err := AppendEvent(root, "c1", core.JournalEvent{
		Time: at(0), Kind: KindTask, ChangeID: "c1",
		Data: map[string]any{"task_id": "T2", "status": "done"},
	}); err != nil {
		t.Fatal(err)
	}
	stubExec(t, fakeGit(1, ""))

	rc, err := BuildResumeContext(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]TaskGuard{}
	for _, g := range rc.Guards {
		byID[g.TaskID] = g
	}
	if g := byID["T1"]; !g.Refuse || !strings.Contains(strings.Join(g.Evidence, " "), "change.md") {
		t.Errorf("T1 (checked in change.md) must be refused with evidence: %+v", g)
	}
	if g := byID["T2"]; !g.Refuse || !strings.Contains(strings.Join(g.Evidence, " "), "journal") {
		t.Errorf("T2 (journal done) must be refused with evidence: %+v", g)
	}
	if g := byID["T3"]; g.Refuse {
		t.Errorf("T3 is open and must stay dispatchable: %+v", g)
	}
}

func TestRedispatchGuardUsesGitLog(t *testing.T) {
	root := t.TempDir()
	writeChange(t, root, "c1", "```toml\n[loop]\npredicate = \"check\"\n```\n- [ ] T9: wire the adapter\n")
	stubExec(t, func(dir string, argv ...string) (string, int, error) {
		if argv[0] != "git" {
			return "", 1, nil
		}
		if argv[1] == "log" {
			for i, a := range argv {
				if a == "--grep" && argv[i+1] == "T9" {
					return "abc1234 T9: wire the adapter\n", 0, nil
				}
			}
			return "", 0, nil
		}
		return "", 1, nil
	})
	rc, err := BuildResumeContext(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Guards) != 1 {
		t.Fatalf("guards = %+v", rc.Guards)
	}
	g := rc.Guards[0]
	if !g.Refuse || !strings.Contains(strings.Join(g.Evidence, " "), "git log: abc1234") {
		t.Errorf("git-log evidence must refuse re-dispatch: %+v", g)
	}
}

func TestResumeRealPredicateExecution(t *testing.T) {
	// Uses the real exec path with the fixture predicate "go version" —
	// go is guaranteed present in the test environment on every OS.
	root := t.TempDir()
	installFixtureChange(t, root, "c1")
	rc, err := BuildResumeContext(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !rc.PredicateGreen {
		t.Fatalf("go version should exit 0; output: %s", rc.PredicateOutput)
	}
	if !strings.Contains(rc.PredicateOutput, "go") {
		t.Errorf("predicate output not captured: %q", rc.PredicateOutput)
	}
}

func TestResumeMissingChangeIsError(t *testing.T) {
	if _, err := BuildResumeContext(t.TempDir(), "ghost"); err == nil {
		t.Fatal("expected remediation error for missing change")
	}
}

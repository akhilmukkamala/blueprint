package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"blueprint/internal/core"
	"blueprint/internal/loop"
)

func run(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// seedRepo builds a minimal .blueprint repo in a temp dir and chdirs into it.
func seedRepo(t *testing.T, changeMD string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".blueprint", "changes", "c1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "change.md"), []byte(changeMD), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

const doneChange = `+++
id = "c1"
title = "Test change"
type = "fix"
tier = "light"
status = "draft"
scenario = "bug"

[loop]
predicate = "go version"
max_iterations = 3

[loop.breaker]
repeat_action_n = 2
+++

## Tasks

- [x] T1: seed
- [ ] T2: finish
`

func seedJournal(t *testing.T, root string, events ...core.JournalEvent) {
	t.Helper()
	for _, ev := range events {
		if err := loop.AppendEvent(root, "c1", ev); err != nil {
			t.Fatal(err)
		}
	}
}

func iterEvent(sec, n int, action string) core.JournalEvent {
	return core.JournalEvent{
		Time: time.Date(2026, 7, 21, 9, 0, sec, 0, time.UTC),
		Kind: loop.KindIteration,
		Data: map[string]any{"n": n, "action": action, "diff_delta": 1.0},
	}
}

func TestRegisteredCommands(t *testing.T) {
	names := map[string]bool{}
	for _, c := range All() {
		names[strings.Fields(c.Use)[0]] = true
	}
	for _, want := range []string{"resume", "status", "log"} {
		if !names[want] {
			t.Errorf("command %q not registered", want)
		}
	}
}

func TestResumeCommandPredicateGreen(t *testing.T) {
	seedRepo(t, doneChange)
	out, err := run(t, newResumeCmd(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "DONE") || !strings.Contains(out, "Nothing to resume") {
		t.Errorf("green predicate must say done, got:\n%s", out)
	}
}

func TestResumeCommandJSON(t *testing.T) {
	seedRepo(t, doneChange)
	out, err := run(t, newResumeCmd(), "c1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rc loop.ResumeContext
	if err := json.Unmarshal([]byte(out), &rc); err != nil {
		t.Fatalf("--json output not JSON: %v\n%s", err, out)
	}
	if !rc.PredicateGreen || rc.ChangeID != "c1" {
		t.Errorf("payload = %+v", rc)
	}
}

func TestResumeCommandMissingChange(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := run(t, newResumeCmd(), "ghost"); err == nil {
		t.Fatal("missing change must fail non-zero")
	}
}

func TestStatusCommandSingleChangeTripped(t *testing.T) {
	root := seedRepo(t, doneChange)
	seedJournal(t, root, iterEvent(1, 1, "run tests"), iterEvent(2, 2, "run tests"))
	out, err := run(t, newStatusCmd(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "TRIPPED") || !strings.Contains(out, loop.PatternRepeatAction) {
		t.Errorf("status must surface the breaker trip, got:\n%s", out)
	}
	if !strings.Contains(out, "blueprint resume c1") {
		t.Errorf("status must teach the next step, got:\n%s", out)
	}
}

func TestStatusCommandJSON(t *testing.T) {
	root := seedRepo(t, doneChange)
	seedJournal(t, root, iterEvent(1, 1, "edit a"))
	out, err := run(t, newStatusCmd(), "c1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		ChangeID string              `json:"change_id"`
		Consumed loop.Consumption    `json:"consumed"`
		Breaker  loop.BreakerVerdict `json:"breaker"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if v.ChangeID != "c1" || v.Consumed.Iterations != 1 || v.Breaker.Trip {
		t.Errorf("payload = %+v", v)
	}
}

func TestStatusCommandRepoWide(t *testing.T) {
	root := seedRepo(t, doneChange)
	seedJournal(t, root, iterEvent(1, 1, "edit a"))
	out, err := run(t, newStatusCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c1") || !strings.Contains(out, "phase=") {
		t.Errorf("repo status must list changes, got:\n%s", out)
	}
}

func TestStatusCommandEmptyRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	out, err := run(t, newStatusCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "blueprint new") {
		t.Errorf("empty repo status must teach the next step, got:\n%s", out)
	}
}

func TestLogCommandHumanAndJSON(t *testing.T) {
	root := seedRepo(t, doneChange)
	seedJournal(t, root,
		iterEvent(1, 1, "edit a"),
		core.JournalEvent{Time: time.Date(2026, 7, 21, 9, 1, 0, 0, time.UTC),
			Kind: loop.KindCost, Data: map[string]any{"usd": 0.5}},
	)
	human, err := run(t, newLogCmd(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "iteration") || !strings.Contains(human, "$0.5") {
		t.Errorf("human log render wrong:\n%s", human)
	}

	raw, err := run(t, newLogCmd(), "c1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != 2 {
		t.Fatalf("--json must stream one event per line, got %d:\n%s", len(lines), raw)
	}
	var ev core.JournalEvent
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil || ev.Kind != loop.KindCost {
		t.Errorf("raw stream line bad: %v %+v", err, ev)
	}
}

func TestLogCommandEmptyJournal(t *testing.T) {
	seedRepo(t, doneChange)
	out, err := run(t, newLogCmd(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no journal") {
		t.Errorf("empty journal message wrong:\n%s", out)
	}
}

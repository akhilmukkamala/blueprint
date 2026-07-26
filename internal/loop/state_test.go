package loop

import (
	"strings"
	"testing"

	"blueprint/internal/core"
)

func TestLoadStateMissingIsIdle(t *testing.T) {
	s, err := LoadState(t.TempDir(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhaseIdle || s.ChangeID != "c1" {
		t.Errorf("fresh state = %+v, want idle c1", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	in := &State{
		ChangeID: "c1", Phase: PhaseRunning, Task: "T2", Iteration: 4,
		Blockers: []string{"flaky test"},
		Consumed: Consumption{Iterations: 4, Minutes: 12.5, USD: 1.75},
	}
	if err := SaveState(root, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadState(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Phase != PhaseRunning || out.Task != "T2" || out.Iteration != 4 || out.Consumed.USD != 1.75 {
		t.Errorf("round trip lost data: %+v", out)
	}
}

func TestSaveStateRequiresChangeID(t *testing.T) {
	if err := SaveState(t.TempDir(), &State{}); err == nil {
		t.Fatal("expected error for empty change id")
	}
}

func TestConsumedDerivation(t *testing.T) {
	evs := []core.JournalEvent{
		{Time: at(0), Kind: "route"},
		iter(60, 1, "edit", 5),
		{Time: at(120), Kind: KindCost, Data: map[string]any{"usd": 0.5}},
		func() core.JournalEvent {
			ev := iter(600, 2, "test", 0)
			ev.Data["usd"] = 0.25
			return ev
		}(),
	}
	c := Consumed(evs)
	if c.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", c.Iterations)
	}
	if c.USD != 0.75 {
		t.Errorf("usd = %v, want 0.75", c.USD)
	}
	if c.Minutes != 10 { // at(600)-at(0), from timestamps not wall clock
		t.Errorf("minutes = %v, want 10", c.Minutes)
	}
}

func TestParkWritesStateAndJournal(t *testing.T) {
	root := t.TempDir()
	if err := AppendEvent(root, "c1", iter(0, 1, "edit", 0)); err != nil {
		t.Fatal(err)
	}
	if err := Park(root, "c1", PatternNoDiffDelta, "3 iterations with no diff"); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(root, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhaseParked {
		t.Errorf("phase = %q, want parked", s.Phase)
	}
	if !strings.Contains(s.Summary, "no diff") {
		t.Errorf("summary missing: %q", s.Summary)
	}
	if len(s.Blockers) != 1 || s.Blockers[0] != PatternNoDiffDelta {
		t.Errorf("blockers = %v", s.Blockers)
	}
	evs, _ := ReadJournal(root, "c1")
	last := evs[len(evs)-1]
	if last.Kind != KindPark {
		t.Errorf("last journal event = %q, want park", last.Kind)
	}
}

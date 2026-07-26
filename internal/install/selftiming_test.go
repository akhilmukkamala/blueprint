package install

import (
	"testing"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// findInstallEvents returns the self-timing events for one command.
func findInstallEvents(t *testing.T, root, command string) []core.JournalEvent {
	t.Helper()
	events, _, err := worklog.Read(root)
	if err != nil {
		t.Fatalf("worklog.Read: %v", err)
	}
	var out []core.JournalEvent
	for _, ev := range events {
		if ev.Kind == "install" && ev.Data["command"] == command {
			out = append(out, ev)
		}
	}
	return out
}

func checkSelfTiming(t *testing.T, ev core.JournalEvent, wantVersion string) {
	t.Helper()
	d, ok := ev.Data["duration_seconds"].(float64)
	if !ok || d < 0 {
		t.Errorf("duration_seconds = %v, want non-negative float", ev.Data["duration_seconds"])
	}
	if v := ev.Data["version"]; v != wantVersion {
		t.Errorf("version = %v, want %q", v, wantVersion)
	}
	if ev.Time.IsZero() {
		t.Error("event has zero timestamp")
	}
}

func TestInitAppendsSelfTimingEvent(t *testing.T) {
	oldV := Version
	Version = "1.2.3-test"
	defer func() { Version = oldV }()

	root := t.TempDir()
	if _, err := Init(root, InitOptions{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	evs := findInstallEvents(t, root, "init")
	if len(evs) != 1 {
		t.Fatalf("got %d install/init events, want exactly 1", len(evs))
	}
	checkSelfTiming(t, evs[0], "1.2.3-test")

	// Idempotent re-run appends a second timing event — each run is measured.
	if _, err := Init(root, InitOptions{}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if evs = findInstallEvents(t, root, "init"); len(evs) != 2 {
		t.Fatalf("after re-run got %d install/init events, want 2", len(evs))
	}
}

func TestAdoptAppendsSingleSelfTimingEvent(t *testing.T) {
	oldV := Version
	Version = "2.0.0-test"
	defer func() { Version = oldV }()

	root := gitRepo(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	if _, err := Adopt(root, now); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	// Adopt runs init internally but must log exactly one "install" event,
	// with command "adopt" — not a nested "init" one.
	if evs := findInstallEvents(t, root, "init"); len(evs) != 0 {
		t.Errorf("adopt leaked %d nested install/init events", len(evs))
	}
	evs := findInstallEvents(t, root, "adopt")
	if len(evs) != 1 {
		t.Fatalf("got %d install/adopt events, want exactly 1", len(evs))
	}
	checkSelfTiming(t, evs[0], "2.0.0-test")
}

package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
)

func at(sec int) time.Time {
	return time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC).Add(time.Duration(sec) * time.Second)
}

func iter(sec int, n int, action string, delta float64, files ...string) core.JournalEvent {
	data := map[string]any{"n": n, "action": action, "diff_delta": delta}
	if len(files) > 0 {
		data["files"] = files
	}
	return core.JournalEvent{Time: at(sec), Kind: KindIteration, Data: data}
}

func TestAppendAndReadJournal(t *testing.T) {
	root := t.TempDir()
	evs := []core.JournalEvent{
		{Time: at(0), Kind: "route", ChangeID: "c1", Data: map[string]any{"tier": "full"}},
		iter(60, 1, "edit src/a.go", 12),
		{Time: at(120), Kind: KindCost, ChangeID: "c1", Data: map[string]any{"usd": 0.25}},
	}
	for _, ev := range evs {
		if err := AppendEvent(root, "c1", ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	got, err := ReadJournal(root, "c1")
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Kind != "route" || got[2].Kind != KindCost {
		t.Errorf("order not preserved: %v", got)
	}

	// Append-only: appending again grows the file, never rewrites.
	raw1, _ := os.ReadFile(JournalPath(root, "c1"))
	if err := AppendEvent(root, "c1", iter(180, 2, "run tests", 0)); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(JournalPath(root, "c1"))
	if !strings.HasPrefix(string(raw2), string(raw1)) {
		t.Error("journal was rewritten, not appended")
	}
}

func TestReadJournalMissingAndTornLine(t *testing.T) {
	root := t.TempDir()
	if evs, err := ReadJournal(root, "nope"); err != nil || evs != nil {
		t.Fatalf("missing journal should be empty, got %v, %v", evs, err)
	}
	// A torn trailing line (crashed writer) must not break the readable spine.
	if err := AppendEvent(root, "c1", iter(0, 1, "a", 1)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(JournalPath(root, "c1"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"time":"2026-07-21T10:`)
	f.Close()
	evs, err := ReadJournal(root, "c1")
	if err != nil {
		t.Fatalf("ReadJournal with torn line: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("want 1 intact event, got %d", len(evs))
	}
}

func TestLastEvents(t *testing.T) {
	var evs []core.JournalEvent
	for i := 0; i < 30; i++ {
		evs = append(evs, iter(i, i, "a", 1))
	}
	if got := LastEvents(evs, 20); len(got) != 20 || got[0].Data["n"] != 10 {
		t.Errorf("LastEvents window wrong: len=%d first n=%v", len(got), got[0].Data["n"])
	}
	if got := LastEvents(evs[:5], 20); len(got) != 5 {
		t.Errorf("short journal should return all events, got %d", len(got))
	}
}

func TestJournalPathIsCanonical(t *testing.T) {
	got := JournalPath("root", "c1")
	want := filepath.Join("root", ".blueprint", "changes", "c1", "journal.ndjson")
	if got != want {
		t.Errorf("JournalPath = %q, want %q", got, want)
	}
}

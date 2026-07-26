package worklog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
)

func TestAppendReadRoundtrip(t *testing.T) {
	root := t.TempDir()
	evs := []core.JournalEvent{
		{Kind: "route", ChangeID: "c1", Data: map[string]any{"tier": "light"}},
		{Kind: "override", ChangeID: "c1", Data: map[string]any{"tier": "full", "reason": "touches auth"}},
	}
	for _, ev := range evs {
		if err := Append(root, ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, warns, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != "route" || got[1].Kind != "override" {
		t.Errorf("kinds = %q, %q", got[0].Kind, got[1].Kind)
	}
	if got[0].Time.IsZero() {
		t.Error("Append must stamp a zero Time")
	}
	if got[1].Data["reason"] != "touches auth" {
		t.Errorf("data lost: %v", got[1].Data)
	}
}

func TestAppendPreservesExplicitTime(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	if err := Append(root, core.JournalEvent{Time: ts, Kind: "route"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, _, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got[0].Time.Equal(ts) {
		t.Errorf("time = %v, want %v", got[0].Time, ts)
	}
}

func TestReadMissingFile(t *testing.T) {
	got, warns, err := Read(t.TempDir())
	if err != nil || len(got) != 0 || len(warns) != 0 {
		t.Fatalf("missing file must read as empty, got %v %v %v", got, warns, err)
	}
}

func TestReadSkipsCorruptLines(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, core.JournalEvent{Kind: "route", ChangeID: "a"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Simulate a crashed writer: a truncated line in the middle, then a good one.
	f, err := os.OpenFile(Path(root), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"kind\":\"trunc\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Append(root, core.JournalEvent{Kind: "verdict", ChangeID: "b"}); err != nil {
		t.Fatalf("Append after corrupt line: %v", err)
	}

	got, warns, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (corrupt skipped)", len(got))
	}
	if got[1].Kind != "verdict" {
		t.Errorf("events after corruption must still parse, got kind %q", got[1].Kind)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "line 2") {
		t.Errorf("want one warning naming line 2, got %v", warns)
	}
}

func TestAppendIsOneLinePerEvent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Append(root, core.JournalEvent{Kind: "route"}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, ".blueprint", "log", "worklog.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
}

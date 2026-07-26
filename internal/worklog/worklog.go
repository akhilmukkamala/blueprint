// Package worklog appends and reads the repo-level event journal
// (.blueprint/log/worklog.ndjson, DESIGN §2). The file is append-only ndjson of
// core.JournalEvent: writers never rewrite existing lines, and the reader is
// tolerant — corrupt lines are skipped with a warning instead of failing the
// whole read, because a half-written trailing line must never brick the repo.
package worklog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"blueprint/internal/core"
)

// Path returns the worklog location under repoRoot.
func Path(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "log", "worklog.ndjson")
}

// Append writes one event as a single ndjson line. The line is written with one
// Write call on an O_APPEND handle so concurrent appenders cannot interleave
// partial lines (POSIX and Windows both keep sub-pipe-size appends atomic).
// A zero Time is stamped with the current time — the only wall-clock use
// permitted by CONTRACTS rule 5 (explicit journal timestamps).
func Append(repoRoot string, ev core.JournalEvent) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("worklog: cannot encode event kind %q: %w", ev.Kind, err)
	}
	dir := filepath.Dir(Path(repoRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("worklog: cannot create %s — check directory permissions: %w", dir, err)
	}
	f, err := os.OpenFile(Path(repoRoot), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("worklog: cannot open %s for append — check file permissions: %w", Path(repoRoot), err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("worklog: append to %s failed: %w", Path(repoRoot), err)
	}
	return f.Close()
}

// Read returns every parseable event in file order plus one warning string per
// skipped corrupt line. A missing worklog is an empty repo history, not an
// error.
func Read(repoRoot string) ([]core.JournalEvent, []string, error) {
	f, err := os.Open(Path(repoRoot))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("worklog: cannot read %s: %w", Path(repoRoot), err)
	}
	defer f.Close()

	var (
		events   []core.JournalEvent
		warnings []string
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ev core.JournalEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"worklog line %d is corrupt and was skipped (%v); the file is append-only — do not hand-edit it", lineNo, err))
			continue
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return events, warnings, fmt.Errorf("worklog: scan of %s failed at line %d: %w", Path(repoRoot), lineNo, err)
	}
	return events, warnings, nil
}

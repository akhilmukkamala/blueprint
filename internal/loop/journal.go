// Package loop is the per-change loop harness (DESIGN §6): the append-only
// journal + state spine, the five-pattern no-progress breaker, cap
// enforcement (iterations / minutes / USD), and the cold-start-proof resume
// ritual. Invariants: the journal is append-only ndjson of core.JournalEvent
// (never rewritten); state.json is replaced atomically; on a breaker trip or
// cap breach the loop is parked with a summary — never silently retried; all
// duration math derives from journal timestamps, never the wall clock.
package loop

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"blueprint/internal/core"
)

// Journal event kinds the harness itself understands. Other kinds (route,
// verdict, approve, ...) pass through untouched.
const (
	KindIteration = "iteration" // data: n, action, files, diff_delta, monologue_tokens, error_fingerprint, usd
	KindCost      = "cost"      // data: usd
	KindTask      = "task"      // data: task_id, status ("done" marks completion)
	KindBreaker   = "breaker"   // data: pattern, evidence
	KindPark      = "park"      // data: reason, summary
	KindResume    = "resume"    // data: predicate_done
)

// changeDir is the canonical per-change folder (DESIGN §2).
func changeDir(repoRoot, id string) string {
	return filepath.Join(repoRoot, ".blueprint", "changes", id)
}

// JournalPath is .blueprint/changes/<id>/journal.ndjson.
func JournalPath(repoRoot, id string) string {
	return filepath.Join(changeDir(repoRoot, id), "journal.ndjson")
}

// AppendEvent appends one event as a single ndjson line. The line is encoded
// first and written with one O_APPEND write so concurrent appenders cannot
// interleave partial lines; the file is never truncated or rewritten.
func AppendEvent(repoRoot, id string, ev core.JournalEvent) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode journal event: %w", err)
	}
	dir := changeDir(repoRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create change dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(JournalPath(repoRoot, id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open journal for append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append journal event: %w", err)
	}
	return nil
}

// ReadJournal returns every event in file order. A missing journal is an
// empty journal, not an error — a change that never ran has no spine yet.
// Malformed lines are skipped with their position noted, never fatal: an
// append-only file must stay readable even if one writer crashed mid-line.
func ReadJournal(repoRoot, id string) ([]core.JournalEvent, error) {
	f, err := os.Open(JournalPath(repoRoot, id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open journal: %w", err)
	}
	defer f.Close()

	var events []core.JournalEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ev core.JournalEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue // torn or foreign line; the spine must remain readable
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return events, fmt.Errorf("read journal: %w", err)
	}
	return events, nil
}

// LastEvents returns the trailing n events (all of them if fewer exist).
func LastEvents(events []core.JournalEvent, n int) []core.JournalEvent {
	if n <= 0 || len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

package verify

import (
	"encoding/json"
	"os"

	"blueprint/internal/core"
)

// appendJournal appends one core.JournalEvent line to the change's
// journal.ndjson. Append-only (CONTRACTS rule 7): open O_APPEND, never
// rewrite. Schema matches internal/loop's journal exactly (core.JournalEvent)
// so the two writers interleave safely.
func appendJournal(repoRoot, id string, ev core.JournalEvent) error {
	if err := os.MkdirAll(changeDir(repoRoot, id), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(journalPath(repoRoot, id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"blueprint/internal/core"
)

// Loop phases stored in state.json. "parked" is terminal until a human (or
// resume with a green predicate) moves it; parking always carries a summary.
const (
	PhaseIdle    = "idle"
	PhaseRunning = "running"
	PhaseParked  = "parked"
	PhaseDone    = "done"
)

// Consumption is cap usage derived from the journal (deterministic: minutes
// come from event timestamps, USD from cost/iteration events, iterations
// from iteration events — never from the wall clock).
type Consumption struct {
	Iterations int     `json:"iterations"`
	Minutes    float64 `json:"minutes"`
	USD        float64 `json:"usd"`
}

// State is .blueprint/changes/<id>/state.json — the resume anchor.
type State struct {
	ChangeID  string      `json:"change_id"`
	Phase     string      `json:"phase"`
	Task      string      `json:"task,omitempty"`
	Iteration int         `json:"iteration"`
	Blockers  []string    `json:"blockers,omitempty"`
	Consumed  Consumption `json:"consumed"`
	Summary   string      `json:"summary,omitempty"` // required when Phase == parked
	UpdatedAt time.Time   `json:"updated_at"`
}

// StatePath is .blueprint/changes/<id>/state.json.
func StatePath(repoRoot, id string) string {
	return filepath.Join(changeDir(repoRoot, id), "state.json")
}

// LoadState reads state.json; a missing file yields a fresh idle state so
// callers never special-case first contact.
func LoadState(repoRoot, id string) (*State, error) {
	raw, err := os.ReadFile(StatePath(repoRoot, id))
	if err != nil {
		if os.IsNotExist(err) {
			return &State{ChangeID: id, Phase: PhaseIdle}, nil
		}
		return nil, fmt.Errorf("read state.json: %w", err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("state.json for change %q is corrupt: %v — restore it from git or delete it to reset the loop to idle", id, err)
	}
	if s.ChangeID == "" {
		s.ChangeID = id
	}
	return &s, nil
}

// SaveState writes state.json atomically (temp file + rename in the same
// directory) so a crash can never leave a half-written resume anchor.
func SaveState(repoRoot string, s *State) error {
	if s.ChangeID == "" {
		return fmt.Errorf("state has no change id — set State.ChangeID before saving")
	}
	dir := changeDir(repoRoot, s.ChangeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create change dir %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Rename(tmpName, StatePath(repoRoot, s.ChangeID)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace state.json: %w", err)
	}
	return nil
}

// Consumed derives cap consumption from the journal. Iterations counts
// iteration events; minutes is last-minus-first event timestamp; USD sums
// data["usd"] on cost and iteration events.
func Consumed(events []core.JournalEvent) Consumption {
	var c Consumption
	for _, ev := range events {
		switch ev.Kind {
		case KindIteration:
			c.Iterations++
			c.USD += dataFloat(ev, "usd")
		case KindCost:
			c.USD += dataFloat(ev, "usd")
		}
	}
	if len(events) >= 2 {
		first, last := events[0].Time, events[len(events)-1].Time
		if last.After(first) {
			c.Minutes = last.Sub(first).Minutes()
		}
	}
	return c
}

// Park moves the loop to phase=parked with a summary, records why in the
// journal, and syncs consumption. Parking is the only allowed response to a
// breaker trip or cap breach — never a silent retry (DESIGN §6 on_trip).
func Park(repoRoot, id, reason, summary string) error {
	s, err := LoadState(repoRoot, id)
	if err != nil {
		return err
	}
	events, err := ReadJournal(repoRoot, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := AppendEvent(repoRoot, id, core.JournalEvent{
		Time:     now,
		Kind:     KindPark,
		ChangeID: id,
		Data:     map[string]any{"reason": reason, "summary": summary},
	}); err != nil {
		return err
	}
	s.Phase = PhaseParked
	s.Summary = summary
	s.Blockers = appendUnique(s.Blockers, reason)
	s.Consumed = Consumed(events)
	s.UpdatedAt = now
	return SaveState(repoRoot, s)
}

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

func dataFloat(ev core.JournalEvent, key string) float64 {
	if ev.Data == nil {
		return 0
	}
	switch v := ev.Data[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func dataString(ev core.JournalEvent, key string) string {
	if ev.Data == nil {
		return ""
	}
	if s, ok := ev.Data[key].(string); ok {
		return s
	}
	return ""
}

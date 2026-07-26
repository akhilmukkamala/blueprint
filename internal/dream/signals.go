// signals.go — deterministic, in-binary signal extraction (DESIGN §12 stage 1).
// Folds the repo worklog + every change journal (active and archived) into
// evidence-cited signals. Pure over repo state: no wall clock, no network, no
// model — the only inputs are journals, verdict events, and living specs.
// The "since" boundary is the last worklog `dream` event: each dream run only
// re-reads history it has not already dreamt about (registry candidacy and the
// human-verify audit are state-shaped, so they read full history).
package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
	"blueprint/internal/spec"
	"blueprint/internal/worklog"
)

// Signal kinds, in severity order (the proposal builder ranks by this).
const (
	SigTamper            = "tamper"
	SigRepeatedFailure   = "repeated-failure"
	SigRegistryCandidate = "registry-candidate"
	SigOverrideCluster   = "override-cluster"
	SigBreakerPattern    = "breaker-pattern"
	SigUngatedHuman      = "ungated-human-verify"
)

// Thresholds (DESIGN §12 / feature brief).
const (
	repeatThreshold  = 3 // same verify fingerprint this many times
	clusterThreshold = 3 // same type overridden same direction this many times
	breakerThreshold = 3 // same breaker pattern tripped this many times
	// RegistryCleanRuns is the consecutive clean verified runs a scenario class
	// needs to surface as a standard-change registry candidate (mirrors the
	// autonomy promotion window, DESIGN §8).
	RegistryCleanRuns = 8
)

// Evidence is one journal-event citation.
type Evidence struct {
	Source   string    `json:"source"` // "worklog" | "changes/<id>" | "archive/<id>"
	Kind     string    `json:"kind"`
	Time     time.Time `json:"time"`
	ChangeID string    `json:"change_id,omitempty"`
}

// String renders the canonical citation form used in proposal.md.
func (e Evidence) String() string {
	s := fmt.Sprintf("%s %s kind=%s", e.Source, e.Time.UTC().Format(time.RFC3339), e.Kind)
	if e.ChangeID != "" {
		s += " change=" + e.ChangeID
	}
	return s
}

// Signal is one extracted observation. Quarantined signals derive from
// untrusted-content markers (journal event data.source="web" or
// data.tainted=true) and may never become patches (SpAIware/MINJA defense).
type Signal struct {
	ID          string         `json:"id"` // S-N, assigned in severity order
	Kind        string         `json:"kind"`
	Summary     string         `json:"summary"`
	Count       int            `json:"count"`
	Quarantined bool           `json:"quarantined,omitempty"`
	Evidence    []Evidence     `json:"evidence"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// changeEvents is one change folder's journal plus its identity.
type changeEvents struct {
	ID       string
	Source   string // "changes/<id>" or "archive/<id>"
	Scenario string
	Events   []core.JournalEvent
}

// tainted reports whether an event carries an untrusted-content marker.
func tainted(ev core.JournalEvent) bool {
	if src, _ := ev.Data["source"].(string); src == "web" {
		return true
	}
	b, _ := ev.Data["tainted"].(bool)
	return b
}

// readNDJSON reads an append-only journal, skipping corrupt lines — a
// half-written trailing line must never brick a dream run.
func readNDJSON(path string) []core.JournalEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var events []core.JournalEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var ev core.JournalEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events
}

// readScenario extracts the scenario field from a change.md's +++ TOML
// frontmatter. Parsed directly (not via spec.LoadChange) because archived
// changes live outside spec's changes/ root and a stale archive must not fail
// a dream run.
func readScenario(changeMD string) string {
	raw, err := os.ReadFile(changeMD)
	if err != nil {
		return ""
	}
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	const fence = "+++\n"
	if !strings.HasPrefix(s, fence) {
		return ""
	}
	rest := s[len(fence):]
	end := strings.Index(rest, "\n+++")
	if end < 0 {
		return ""
	}
	var fm struct {
		Scenario string `toml:"scenario"`
	}
	if err := toml.Unmarshal([]byte(rest[:end+1]), &fm); err != nil {
		return ""
	}
	return fm.Scenario
}

// collectChanges walks .blueprint/changes and .blueprint/archive.
func collectChanges(repoRoot string) ([]changeEvents, error) {
	var out []changeEvents
	for _, sub := range []string{"changes", "archive"} {
		root := filepath.Join(repoRoot, ".blueprint", sub)
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("dream: cannot list %s: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			ce := changeEvents{
				ID:       e.Name(),
				Source:   sub + "/" + e.Name(),
				Scenario: readScenario(filepath.Join(dir, "change.md")),
				Events:   readNDJSON(filepath.Join(dir, "journal.ndjson")),
			}
			out = append(out, ce)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out, nil
}

// lastDreamTime is the timestamp of the newest worklog `dream` event; zero if
// the repo has never dreamt.
func lastDreamTime(wl []core.JournalEvent) time.Time {
	var t time.Time
	for _, ev := range wl {
		if ev.Kind == "dream" && ev.Time.After(t) {
			t = ev.Time
		}
	}
	return t
}

// ExtractSignals folds repo history into signals. The returned since time is
// the last-dream boundary applied to event-derived signals (zero = full
// history). Deterministic: group keys are sorted before ID assignment.
func ExtractSignals(repoRoot string) ([]Signal, time.Time, error) {
	wl, _, err := worklog.Read(repoRoot)
	if err != nil {
		return nil, time.Time{}, err
	}
	changes, err := collectChanges(repoRoot)
	if err != nil {
		return nil, time.Time{}, err
	}
	since := lastDreamTime(wl)

	var signals []Signal
	signals = append(signals, tamperSignals(wl, changes, since)...)
	signals = append(signals, repeatedFailureSignals(changes, since)...)
	signals = append(signals, registryCandidateSignals(changes)...)
	signals = append(signals, overrideClusterSignals(wl, since)...)
	signals = append(signals, breakerSignals(changes, since)...)
	signals = append(signals, ungatedHumanSignals(repoRoot, wl, changes)...)

	for i := range signals {
		signals[i].ID = fmt.Sprintf("S-%d", i+1)
		// Citations read chronologically regardless of which folder (active vs
		// archive) an event was folded from.
		ev := signals[i].Evidence
		sort.SliceStable(ev, func(a, b int) bool { return ev[a].Time.Before(ev[b].Time) })
	}
	return signals, since, nil
}

// tamperSignals: every TAMPER event is a signal — one aggregated signal, since
// even a single tamper is approved-input drift that a human must see.
func tamperSignals(wl []core.JournalEvent, changes []changeEvents, since time.Time) []Signal {
	var ev []Evidence
	quarantined := false
	note := func(source string, e core.JournalEvent) {
		if e.Kind != "tamper" || !e.Time.After(since) {
			return
		}
		ev = append(ev, Evidence{Source: source, Kind: e.Kind, Time: e.Time, ChangeID: e.ChangeID})
		quarantined = quarantined || tainted(e)
	}
	for _, e := range wl {
		note("worklog", e)
	}
	for _, ce := range changes {
		for _, e := range ce.Events {
			if e.ChangeID == "" {
				e.ChangeID = ce.ID
			}
			note(ce.Source, e)
		}
	}
	if len(ev) == 0 {
		return nil
	}
	ids := map[string]bool{}
	for _, e := range ev {
		if e.ChangeID != "" {
			ids[e.ChangeID] = true
		}
	}
	return []Signal{{
		Kind:        SigTamper,
		Summary:     fmt.Sprintf("%d tamper event(s) recorded — approved inputs drifted under verification on %s", len(ev), joinSortedKeys(ids)),
		Count:       len(ev),
		Quarantined: quarantined,
		Evidence:    ev,
		Detail:      map[string]any{"changes": sortedKeys(ids)},
	}}
}

// repeatedFailureSignals: the same verify failure fingerprint appearing
// repeatThreshold+ times (verdict events with pass=false) across changes.
func repeatedFailureSignals(changes []changeEvents, since time.Time) []Signal {
	type group struct {
		evidence []Evidence
		changes  map[string]bool
		taint    bool
	}
	groups := map[string]*group{}
	for _, ce := range changes {
		for _, e := range ce.Events {
			if e.Kind != "verdict" || !e.Time.After(since) {
				continue
			}
			if pass, _ := e.Data["pass"].(bool); pass {
				continue
			}
			fp, _ := e.Data["fingerprint"].(string)
			if fp == "" {
				continue
			}
			g := groups[fp]
			if g == nil {
				g = &group{changes: map[string]bool{}}
				groups[fp] = g
			}
			g.evidence = append(g.evidence, Evidence{Source: ce.Source, Kind: e.Kind, Time: e.Time, ChangeID: ce.ID})
			g.changes[ce.ID] = true
			g.taint = g.taint || tainted(e)
		}
	}
	var out []Signal
	for _, fp := range sortedGroupKeys(groups) {
		g := groups[fp]
		if len(g.evidence) < repeatThreshold {
			continue
		}
		out = append(out, Signal{
			Kind: SigRepeatedFailure,
			Summary: fmt.Sprintf("verify failure fingerprint %.12s… recurred %d times across %s",
				fp, len(g.evidence), joinSortedKeys(g.changes)),
			Count:       len(g.evidence),
			Quarantined: g.taint,
			Evidence:    g.evidence,
			Detail:      map[string]any{"fingerprint": fp, "changes": sortedKeys(g.changes)},
		})
	}
	return out
}

// registryCandidateSignals: scenario classes whose trailing RegistryCleanRuns
// verified runs were all clean (first verdict green, no tamper). Full-history
// by design — candidacy is a state, not an increment.
func registryCandidateSignals(changes []changeEvents) []Signal {
	type run struct {
		change string
		source string
		time   time.Time
		clean  bool
		taint  bool
	}
	byScenario := map[string][]run{}
	for _, ce := range changes {
		var (
			firstVerdict *core.JournalEvent
			last         time.Time
			tampered     bool
			taint        bool
		)
		for i, e := range ce.Events {
			switch e.Kind {
			case "verdict":
				if firstVerdict == nil {
					firstVerdict = &ce.Events[i]
				}
				last = e.Time
				if b, _ := e.Data["tamper"].(bool); b {
					tampered = true
				}
				taint = taint || tainted(e)
			case "tamper":
				tampered = true
			}
		}
		if firstVerdict == nil {
			continue // never verified — not a run
		}
		scenario := ce.Scenario
		if scenario == "" {
			scenario = "unclassified"
		}
		pass, _ := firstVerdict.Data["pass"].(bool)
		byScenario[scenario] = append(byScenario[scenario], run{
			change: ce.ID, source: ce.Source, time: last, clean: pass && !tampered, taint: taint,
		})
	}

	var out []Signal
	for _, scenario := range sortedGroupKeys(byScenario) {
		if scenario == "unclassified" {
			continue // an exempt-class proposal needs a real scenario class
		}
		runs := byScenario[scenario]
		sort.Slice(runs, func(i, j int) bool { return runs[i].time.Before(runs[j].time) })
		streak := 0
		for i := len(runs) - 1; i >= 0; i-- {
			if !runs[i].clean {
				break
			}
			streak++
		}
		if streak < RegistryCleanRuns {
			continue
		}
		tail := runs[len(runs)-streak:]
		var ev []Evidence
		taint := false
		for _, r := range tail {
			ev = append(ev, Evidence{Source: r.source, Kind: "verdict", Time: r.time, ChangeID: r.change})
			taint = taint || r.taint
		}
		out = append(out, Signal{
			Kind: SigRegistryCandidate,
			Summary: fmt.Sprintf("scenario class %q: %d consecutive clean verified runs (last on %s) — standard-change registry candidate",
				scenario, streak, tail[len(tail)-1].time.UTC().Format("2006-01-02")),
			Count:       streak,
			Quarantined: taint,
			Evidence:    ev,
			Detail:      map[string]any{"scenario": scenario},
		})
	}
	return out
}

// overrideClusterSignals: the same change type overridden in the same
// direction clusterThreshold+ times. Each worklog `override` event is joined
// to its routing decision (route event for the same change, else the nearest
// preceding route event) to recover the routed tier and change type.
func overrideClusterSignals(wl []core.JournalEvent, since time.Time) []Signal {
	tierRank := map[string]int{"exempt": 0, "light": 1, "full": 2}
	type group struct {
		evidence []Evidence
		taint    bool
	}
	groups := map[string]*group{}

	var lastRoute *core.JournalEvent
	routeByChange := map[string]core.JournalEvent{}
	for i, ev := range wl {
		switch ev.Kind {
		case "route":
			lastRoute = &wl[i]
			if ev.ChangeID != "" {
				routeByChange[ev.ChangeID] = ev
			}
		case "override":
			if !ev.Time.After(since) {
				continue
			}
			route, ok := routeByChange[ev.ChangeID]
			if !ok || ev.ChangeID == "" {
				if lastRoute == nil {
					continue // an override with no routing decision to compare against
				}
				route = *lastRoute
			}
			fromTier, _ := route.Data["tier"].(string)
			toTier, _ := ev.Data["tier"].(string)
			changeType, _ := route.Data["type"].(string)
			if changeType == "" {
				changeType = "unknown"
			}
			dir := "down"
			if tierRank[toTier] > tierRank[fromTier] {
				dir = "up"
			}
			key := changeType + "|" + dir
			g := groups[key]
			if g == nil {
				g = &group{}
				groups[key] = g
			}
			g.evidence = append(g.evidence, Evidence{Source: "worklog", Kind: ev.Kind, Time: ev.Time, ChangeID: ev.ChangeID})
			g.taint = g.taint || tainted(ev) || tainted(route)
		}
	}

	var out []Signal
	for _, key := range sortedGroupKeys(groups) {
		g := groups[key]
		if len(g.evidence) < clusterThreshold {
			continue
		}
		changeType, dir, _ := strings.Cut(key, "|")
		out = append(out, Signal{
			Kind: SigOverrideCluster,
			Summary: fmt.Sprintf("router override cluster: type %q overridden %sward %d times — the router is mis-tiering this class",
				changeType, dir, len(g.evidence)),
			Count:       len(g.evidence),
			Quarantined: g.taint,
			Evidence:    g.evidence,
			Detail:      map[string]any{"type": changeType, "direction": dir},
		})
	}
	return out
}

// breakerSignals: the same no-progress breaker pattern tripping
// breakerThreshold+ times across change journals.
func breakerSignals(changes []changeEvents, since time.Time) []Signal {
	type group struct {
		evidence []Evidence
		taint    bool
	}
	groups := map[string]*group{}
	for _, ce := range changes {
		for _, e := range ce.Events {
			if e.Kind != "breaker" || !e.Time.After(since) {
				continue
			}
			pattern, _ := e.Data["pattern"].(string)
			if pattern == "" {
				pattern = "unknown"
			}
			g := groups[pattern]
			if g == nil {
				g = &group{}
				groups[pattern] = g
			}
			g.evidence = append(g.evidence, Evidence{Source: ce.Source, Kind: e.Kind, Time: e.Time, ChangeID: ce.ID})
			g.taint = g.taint || tainted(e)
		}
	}
	var out []Signal
	for _, pattern := range sortedGroupKeys(groups) {
		g := groups[pattern]
		if len(g.evidence) < breakerThreshold {
			continue
		}
		out = append(out, Signal{
			Kind:        SigBreakerPattern,
			Summary:     fmt.Sprintf("breaker pattern %q tripped %d times — loops are burning budget on a recurring stall", pattern, len(g.evidence)),
			Count:       len(g.evidence),
			Quarantined: g.taint,
			Evidence:    g.evidence,
			Detail:      map[string]any{"pattern": pattern},
		})
	}
	return out
}

// ungatedHumanSignals: `verify: human` methods in living specs that never
// produced a human-gate journal event even though verification has run in this
// repo — the escape hatch is being silently skipped, not gated (DESIGN §3:
// a human method always triggers a gate).
func ungatedHumanSignals(repoRoot string, wl []core.JournalEvent, changes []changeEvents) []Signal {
	// Verification must have happened at least once, or absence proves nothing.
	var lastVerdict *Evidence
	for _, ce := range changes {
		for _, e := range ce.Events {
			if e.Kind == "verdict" && (lastVerdict == nil || e.Time.After(lastVerdict.Time)) {
				lastVerdict = &Evidence{Source: ce.Source, Kind: e.Kind, Time: e.Time, ChangeID: ce.ID}
			}
		}
	}
	if lastVerdict == nil {
		return nil
	}

	gated := map[string]bool{}
	noteGate := func(e core.JournalEvent) {
		if e.Kind != "human-gate" && e.Kind != "gate" {
			return
		}
		if req, _ := e.Data["req"].(string); req != "" {
			gated[req] = true
		}
	}
	for _, e := range wl {
		noteGate(e)
	}
	for _, ce := range changes {
		for _, e := range ce.Events {
			noteGate(e)
		}
	}

	areas, err := spec.ListSpecs(repoRoot)
	if err != nil {
		return nil
	}
	var ungated []string
	questions := map[string]string{}
	for _, area := range areas {
		ls, err := spec.LoadSpec(repoRoot, area)
		if err != nil {
			continue // a malformed spec is lint's finding, not dream's
		}
		for _, r := range ls.Requirements {
			for _, v := range r.Verify {
				if v.Kind == "human" && !gated[r.ID] {
					ungated = append(ungated, r.ID)
					questions[r.ID] = v.Ref
					break
				}
			}
		}
	}
	if len(ungated) == 0 {
		return nil
	}
	sort.Strings(ungated)
	return []Signal{{
		Kind: SigUngatedHuman,
		Summary: fmt.Sprintf("%d `verify: human` method(s) never triggered a human-gate event despite verification running: %s",
			len(ungated), strings.Join(ungated, ", ")),
		Count:    len(ungated),
		Evidence: []Evidence{*lastVerdict},
		Detail:   map[string]any{"requirements": ungated, "questions": questions},
	}}
}

func sortedGroupKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(m map[string]bool) []string {
	return sortedGroupKeys(m)
}

func joinSortedKeys(m map[string]bool) string {
	return strings.Join(sortedKeys(m), ", ")
}

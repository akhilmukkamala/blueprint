// runs.go — folds per-change journals (live and archived), the repo worklog,
// and git history into per-scenario-class RunRecords. Pure over repo state
// except the injectable revert probe (CONTRACTS rule 5).
package autonomy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// demoteSignal is one escaped-defect/tamper observation attributable to a
// scenario class. Signals older than the class's last journaled demotion are
// spent — that timestamp comparison is what makes demotion idempotent across
// repeated recomputes.
type demoteSignal struct {
	Scenario string
	Time     time.Time
	Reason   string
}

// readNDJSON reads an append-only journal, skipping corrupt lines (a
// half-written trailing line must never brick a recompute).
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
// frontmatter. It parses directly (not via spec.LoadChange) because archived
// changes live outside spec's changes/ root and a stale archive must not fail
// a recompute.
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

// collectRuns walks .blueprint/changes and .blueprint/archive, turning every
// change that has at least one verdict into a RunRecord, and gathers demotion
// signals (tamper events; incidents and reverts within reworkSince).
func collectRuns(repoRoot string, reworkSince time.Time, reverted map[string]time.Time) (map[string][]RunRecord, []demoteSignal, error) {
	runs := map[string][]RunRecord{}
	var signals []demoteSignal

	// Repo-level incidents (worklog kind "incident") attribute rework to
	// their change; per-change journals may carry incidents too.
	incidents := map[string]time.Time{} // changeID -> latest incident time
	wl, _, err := worklog.Read(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	noteIncident := func(ev core.JournalEvent) {
		if ev.Kind == "incident" && ev.ChangeID != "" && ev.Time.After(reworkSince) {
			if ev.Time.After(incidents[ev.ChangeID]) {
				incidents[ev.ChangeID] = ev.Time
			}
		}
	}
	for _, ev := range wl {
		noteIncident(ev)
	}

	type changeRef struct{ id, dir string }
	var refs []changeRef
	for _, sub := range []string{"changes", "archive"} {
		root := filepath.Join(repoRoot, ".blueprint", sub)
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("autonomy: cannot list %s: %w", root, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				refs = append(refs, changeRef{e.Name(), filepath.Join(root, e.Name())})
			}
		}
	}

	for _, ref := range refs {
		events := readNDJSON(filepath.Join(ref.dir, "journal.ndjson"))
		for _, ev := range events {
			if ev.ChangeID == "" {
				ev.ChangeID = ref.id
			}
			noteIncident(ev)
		}
		scenario := readScenario(filepath.Join(ref.dir, "change.md"))
		if scenario == "" {
			scenario = "unclassified"
		}

		var (
			firstVerdict  *core.JournalEvent
			lastVerdictAt time.Time
			sawVerdict    bool
			tampered      bool
			tamperAt      time.Time
		)
		for i, ev := range events {
			switch ev.Kind {
			case "verdict":
				if !sawVerdict {
					firstVerdict = &events[i]
					sawVerdict = true
				}
				lastVerdictAt = ev.Time
				if b, _ := ev.Data["tamper"].(bool); b {
					tampered, tamperAt = true, ev.Time
				}
			case "tamper":
				tampered, tamperAt = true, ev.Time
			}
		}
		if !sawVerdict {
			continue // never verified — not a run
		}

		revertAt, wasReverted := reverted[ref.id]
		incidentAt, hadIncident := incidents[ref.id]
		rec := RunRecord{
			ChangeID: ref.id,
			Time:     lastVerdictAt,
			Clean:    boolData(firstVerdict, "pass") && !tampered,
			Tamper:   tampered,
			Rework:   wasReverted || hadIncident,
		}
		runs[scenario] = append(runs[scenario], rec)

		if tampered {
			signals = append(signals, demoteSignal{scenario, tamperAt,
				fmt.Sprintf("tamper event on change %s", ref.id)})
		}
		if wasReverted {
			signals = append(signals, demoteSignal{scenario, revertAt,
				fmt.Sprintf("revert commit touching archived change %s within %d days", ref.id, ReworkDays)})
		}
		if hadIncident {
			signals = append(signals, demoteSignal{scenario, incidentAt,
				fmt.Sprintf("incident journaled for change %s", ref.id)})
		}
	}

	for s := range runs {
		rs := runs[s]
		sort.Slice(rs, func(i, j int) bool { return rs[i].Time.Before(rs[j].Time) })
		runs[s] = rs
	}
	return runs, signals, nil
}

func boolData(ev *core.JournalEvent, key string) bool {
	if ev == nil {
		return false
	}
	b, _ := ev.Data[key].(bool)
	return b
}

// gitReverts is the default revert probe: commits since `since` whose subject
// marks a revert and whose diff touches .blueprint/archive/<id>/ files.
// Returns changeID -> commit time. URL-free and local — no network.
func gitReverts(repoRoot string, since time.Time) (map[string]time.Time, error) {
	cmd := exec.Command("git", "-C", repoRoot, "log",
		"--since="+since.UTC().Format(time.RFC3339),
		"--pretty=format:\x00%ct\x1f%s", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		// Not a git repo (or no commits yet): no revert evidence, not an
		// error — recompute must work in fixture repos without history.
		return map[string]time.Time{}, nil
	}
	res := map[string]time.Time{}
	for _, block := range strings.Split(string(out), "\x00") {
		if block == "" {
			continue
		}
		head, files, _ := strings.Cut(block, "\n")
		ct, subject, ok := strings.Cut(head, "\x1f")
		if !ok || !strings.Contains(strings.ToLower(subject), "revert") {
			continue
		}
		sec, err := strconv.ParseInt(strings.TrimSpace(ct), 10, 64)
		if err != nil {
			continue
		}
		when := time.Unix(sec, 0).UTC()
		for _, f := range strings.Split(files, "\n") {
			f = strings.TrimSpace(f)
			rest, found := strings.CutPrefix(filepath.ToSlash(f), ".blueprint/archive/")
			if !found {
				continue
			}
			id, _, _ := strings.Cut(rest, "/")
			if id != "" {
				if prev, ok := res[id]; !ok || when.After(prev) {
					res[id] = when
				}
			}
		}
	}
	return res, nil
}

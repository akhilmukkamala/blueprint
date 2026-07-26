// Source collection: the worklog plus every change journal, active
// (.blueprint/changes/<id>/) and archived (.blueprint/archive/<id>/). The
// reader is tolerant like worklog.Read — a corrupt trailing line becomes a
// warning, never a failed metrics run.
package metrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
	"blueprint/internal/loop"
	"blueprint/internal/worklog"
)

// changeInfo is one change's identity plus its full journal.
type changeInfo struct {
	ID       string
	Tier     core.CeremonyTier
	Status   core.ChangeStatus
	Archived bool
	Events   []core.JournalEvent
	Phase    string // loop state phase; active changes only
}

// sources is everything the folds read.
type sources struct {
	Worklog  []core.JournalEvent
	Changes  []changeInfo
	Warnings []string
}

func collect(repoRoot string) (*sources, error) {
	src := &sources{}
	evs, warns, err := worklog.Read(repoRoot)
	if err != nil {
		return nil, err
	}
	src.Worklog = evs
	src.Warnings = append(src.Warnings, warns...)

	for _, dir := range []struct {
		name     string
		archived bool
	}{
		{filepath.Join(repoRoot, ".blueprint", "changes"), false},
		{filepath.Join(repoRoot, ".blueprint", "archive"), true},
	} {
		entries, err := os.ReadDir(dir.name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", dir.name, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ci, err := loadChangeInfo(repoRoot, dir.name, e.Name(), dir.archived)
			if err != nil {
				src.Warnings = append(src.Warnings, err.Error())
				continue
			}
			src.Changes = append(src.Changes, *ci)
		}
	}
	sort.Slice(src.Changes, func(i, j int) bool { return src.Changes[i].ID < src.Changes[j].ID })
	return src, nil
}

// changeFront is the minimal +++ TOML frontmatter of change.md — metrics only
// needs identity, tier, and status, so it does not depend on the full spec
// parser (which is bound to the active changes/ directory).
type changeFront struct {
	ID     string `toml:"id"`
	Tier   string `toml:"tier"`
	Status string `toml:"status"`
}

func loadChangeInfo(repoRoot, dir, id string, archived bool) (*changeInfo, error) {
	ci := &changeInfo{ID: id, Archived: archived}

	raw, err := os.ReadFile(filepath.Join(dir, id, "change.md"))
	if err == nil {
		var fm changeFront
		if front, ok := frontmatter(string(raw)); ok {
			if _, err := toml.Decode(front, &fm); err == nil {
				ci.Tier = core.CeremonyTier(fm.Tier)
				ci.Status = core.ChangeStatus(fm.Status)
			}
		}
	}

	evs, warns, err := readNDJSON(filepath.Join(dir, id, "journal.ndjson"))
	if err != nil {
		return nil, fmt.Errorf("change %s: %v", id, err)
	}
	_ = warns
	ci.Events = evs

	if !archived {
		if st, err := loop.LoadState(repoRoot, id); err == nil {
			ci.Phase = st.Phase
		}
	}
	return ci, nil
}

// frontmatter extracts the body between the leading +++ fences.
func frontmatter(text string) (string, bool) {
	t := strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(t, "+++") {
		return "", false
	}
	rest := t[len("+++"):]
	end := strings.Index(rest, "\n+++")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// readNDJSON reads one append-only journal tolerantly: parseable lines in file
// order, one warning per corrupt line, missing file = empty history.
func readNDJSON(path string) ([]core.JournalEvent, []string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var (
		evs   []core.JournalEvent
		warns []string
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var ev core.JournalEvent
		if err := json.Unmarshal([]byte(text), &ev); err != nil {
			warns = append(warns, fmt.Sprintf("%s:%d: skipping corrupt journal line: %v", path, line, err))
			continue
		}
		evs = append(evs, ev)
	}
	if err := sc.Err(); err != nil {
		return evs, warns, fmt.Errorf("scan %s: %w", path, err)
	}
	return evs, warns, nil
}

// dataFloat reads a numeric data field regardless of JSON number decoding.
func dataFloat(ev core.JournalEvent, key string) (float64, bool) {
	v, ok := ev.Data[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func dataString(ev core.JournalEvent, key string) string {
	if s, ok := ev.Data[key].(string); ok {
		return s
	}
	return ""
}

// dataStrings reads a []string data field decoded from JSON as []any.
func dataStrings(ev core.JournalEvent, key string) []string {
	raw, ok := ev.Data[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

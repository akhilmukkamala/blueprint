package spec

// Change files (.blueprint/changes/<id>/change.md) are the ONE user-authored
// artifact per change (DESIGN §3, ADR-0005): TOML frontmatter (identity +
// loop contract) fenced by `+++`, then markdown sections:
//
//	+++
//	id = "2026-07-21-auth-login"
//	title = "Add login endpoint"
//	type = "feat"
//	tier = "full"
//	status = "draft"
//	scenario = "greenfield"
//
//	[loop]
//	predicate = "blueprint verify 2026-07-21-auth-login"
//	max_iterations = 12
//	max_minutes = 90
//	max_usd = 15.0
//
//	[loop.breaker]
//	repeat_action_n = 3
//	...
//
//	[loop.boundaries]
//	writable = ["src/**"]
//	readonly = [".blueprint/specs/**"]
//	+++
//
//	## Delta
//
//	### ADDED REQ-auth-001 (event-driven)
//	...requirement text...
//
//	verify:
//	- test: TestLoginRejectsInvalid
//
//	### REMOVED REQ-auth-003
//
//	## Tasks
//
//	- [ ] T1: Implement the login handler
//	  - Consumes: .blueprint/specs/auth/spec.md
//	  - Produces: src/auth/handler.go
//
//	## Design
//
//	Free-form design notes (full tier only).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
)

// changeFrontmatter is the TOML shape of the fence block. It differs from
// core.Change/core.LoopContract only in nesting boundaries under
// [loop.boundaries] (the documented on-disk format, DESIGN §6).
type changeFrontmatter struct {
	ID       string     `toml:"id"`
	Title    string     `toml:"title"`
	Type     string     `toml:"type"`
	Tier     string     `toml:"tier"`
	Status   string     `toml:"status"`
	Scenario string     `toml:"scenario"`
	SLA      *time.Time `toml:"sla,omitempty"`
	Loop     loopTOML   `toml:"loop"`
}

type loopTOML struct {
	Predicate     string         `toml:"predicate"`
	MaxIterations int            `toml:"max_iterations"`
	MaxMinutes    int            `toml:"max_minutes"`
	MaxUSD        float64        `toml:"max_usd"`
	Breaker       core.Breaker   `toml:"breaker"`
	Boundaries    boundariesTOML `toml:"boundaries"`
}

type boundariesTOML struct {
	Writable []string `toml:"writable"`
	Readonly []string `toml:"readonly"`
}

// deltaHeading matches "### <OP> REQ-<area>-NNN (pattern)"; pattern optional
// (REMOVED entries carry only the ID).
var deltaHeading = regexp.MustCompile(`^###\s+(ADDED|MODIFIED|REMOVED)\s+(REQ-[A-Za-z0-9-]+-\d+)\s*(?:\(([a-z-]+)\))?\s*$`)

// taskLine matches "- [ ] T1: text" (ID optional: "- [x] text" also parses).
var taskLine = regexp.MustCompile(`^- \[([ xX])\]\s+(?:([A-Za-z0-9_-]+):\s+)?(.+?)\s*$`)

// taskMeta matches an indented "  - Consumes: a, b" / "  - Produces: c" line.
var taskMeta = regexp.MustCompile(`^\s+-\s+(Consumes|Produces):\s*(.*?)\s*$`)

func changesDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "changes")
}

// ChangePath returns .blueprint/changes/<id>/change.md under repoRoot.
func ChangePath(repoRoot, id string) string {
	return filepath.Join(changesDir(repoRoot), id, "change.md")
}

// ListChanges returns the sorted change IDs that have a change.md.
func ListChanges(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(changesDir(repoRoot))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", changesDir(repoRoot), err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(ChangePath(repoRoot, e.Name())); err == nil {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// LoadChange parses .blueprint/changes/<id>/change.md.
func LoadChange(repoRoot, id string) (*core.Change, error) {
	path := ChangePath(repoRoot, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no change %q: expected %s — run `blueprint new` to create it, or `blueprint lint` to see known changes", id, path)
	}
	c, err := parseChange(path, string(raw))
	if err != nil {
		return nil, err
	}
	if c.ID == "" {
		c.ID = id
	}
	if c.ID != id {
		return nil, fmt.Errorf("%s: frontmatter id %q does not match folder name %q — rename the folder or fix the id field", path, c.ID, id)
	}
	return c, nil
}

func parseChange(path, raw string) (*core.Change, error) {
	fmRaw, body, err := splitFrontmatter(raw, "+++")
	if err != nil {
		return nil, fmt.Errorf("%s: %v — change files start with a `+++` TOML frontmatter block (id/title/type/tier/status/scenario + [loop] contract)", path, err)
	}
	var fm changeFrontmatter
	if _, err := toml.Decode(fmRaw, &fm); err != nil {
		return nil, fmt.Errorf("%s: frontmatter is not valid TOML (%v) — fix the block between the `+++` fences", path, err)
	}
	c := &core.Change{
		ID:       fm.ID,
		Title:    fm.Title,
		Type:     fm.Type,
		Tier:     core.CeremonyTier(fm.Tier),
		Status:   core.ChangeStatus(fm.Status),
		Scenario: fm.Scenario,
		SLA:      fm.SLA,
		Contract: core.LoopContract{
			Predicate:     fm.Loop.Predicate,
			MaxIterations: fm.Loop.MaxIterations,
			MaxMinutes:    fm.Loop.MaxMinutes,
			MaxUSD:        fm.Loop.MaxUSD,
			Breaker:       fm.Loop.Breaker,
			Writable:      fm.Loop.Boundaries.Writable,
			ReadOnly:      fm.Loop.Boundaries.Readonly,
		},
	}

	sections := splitSections(body)
	if delta, ok := sections["Delta"]; ok {
		d, err := parseDelta(path, delta)
		if err != nil {
			return nil, err
		}
		c.Delta = d
	}
	if tasks, ok := sections["Tasks"]; ok {
		t, err := parseTasks(path, tasks)
		if err != nil {
			return nil, err
		}
		c.Tasks = t
	}
	if design, ok := sections["Design"]; ok {
		c.Design = strings.TrimSpace(design)
	}
	return c, nil
}

// splitSections cuts a markdown body on "## <Name>" headings.
func splitSections(body string) map[string]string {
	sections := map[string]string{}
	lines := strings.Split(body, "\n")
	current := ""
	var buf []string
	flush := func() {
		if current != "" {
			sections[current] = strings.Join(buf, "\n")
		}
		buf = nil
	}
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if name, ok := strings.CutPrefix(l, "## "); ok {
			flush()
			current = strings.TrimSpace(name)
			continue
		}
		buf = append(buf, l)
	}
	flush()
	return sections
}

func parseDelta(path, body string) ([]core.DeltaEntry, error) {
	lines := strings.Split(body, "\n")
	var entries []core.DeltaEntry
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "### ") {
			i++
			continue
		}
		m := deltaHeading.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s: delta heading %q is malformed — use `### ADDED|MODIFIED|REMOVED REQ-<area>-NNN (pattern)`", path, line)
		}
		op, id, pattern := core.DeltaOp(m[1]), m[2], m[3]
		area := AreaOf(id)
		// Collect the block until the next heading, then reuse the
		// requirement text/verify parser on it.
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "### ") && !strings.HasPrefix(lines[j], "## ") {
			j++
		}
		block := strings.Join(lines[i+1:j], "\n")
		req := core.Requirement{ID: id, Pattern: core.EARSPattern(pattern)}
		text, verify := parseReqBlock(block)
		req.Text = text
		req.Verify = verify
		if op == core.DeltaRemoved && (req.Text != "" || len(req.Verify) > 0) {
			return nil, fmt.Errorf("%s: REMOVED entry %s must carry only the ID — the living spec is the source of the removed text", path, id)
		}
		entries = append(entries, core.DeltaEntry{Op: op, Area: area, Requirement: req})
		i = j
	}
	return entries, nil
}

// parseReqBlock extracts requirement text and the verify: block from the
// lines between two headings.
func parseReqBlock(block string) (string, []core.VerifyMethod) {
	lines := strings.Split(block, "\n")
	var text []string
	var verify []core.VerifyMethod
	i := 0
	for i < len(lines) {
		l := lines[i]
		if strings.TrimSpace(l) == "verify:" {
			i++
			for i < len(lines) {
				vm := verifyEntry.FindStringSubmatch(strings.TrimSpace(lines[i]))
				if vm == nil {
					break
				}
				verify = append(verify, core.VerifyMethod{Kind: vm[1], Ref: vm[2]})
				i++
			}
			continue
		}
		text = append(text, l)
		i++
	}
	return strings.TrimSpace(strings.Join(text, "\n")), verify
}

func parseTasks(path, body string) ([]core.Task, error) {
	lines := strings.Split(body, "\n")
	var tasks []core.Task
	n := 0
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		m := taskLine.FindStringSubmatch(l)
		if m == nil {
			if mm := taskMeta.FindStringSubmatch(l); mm != nil && len(tasks) > 0 {
				vals := splitCSV(mm[2])
				t := &tasks[len(tasks)-1]
				if mm[1] == "Consumes" {
					t.Consumes = append(t.Consumes, vals...)
				} else {
					t.Produces = append(t.Produces, vals...)
				}
			} else if strings.HasPrefix(strings.TrimSpace(l), "- [") {
				return nil, fmt.Errorf("%s: task line %q is malformed — use `- [ ] T<n>: <imperative step>`", path, l)
			}
			continue
		}
		n++
		id := m[2]
		if id == "" {
			id = fmt.Sprintf("T%d", n)
		}
		tasks = append(tasks, core.Task{
			ID:   id,
			Text: m[3],
			Done: m[1] != " ",
		})
	}
	return tasks, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SaveChange serializes c to .blueprint/changes/<id>/change.md, creating the
// folder if needed. The output round-trips through LoadChange.
func SaveChange(repoRoot string, c *core.Change) error {
	if c.ID == "" {
		return fmt.Errorf("change has no id — set Change.ID before saving")
	}
	path := ChangePath(repoRoot, c.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create change directory for %q: %v", c.ID, err)
	}

	var b strings.Builder
	b.WriteString("+++\n")
	enc := toml.NewEncoder(&b)
	if err := enc.Encode(changeFrontmatter{
		ID:       c.ID,
		Title:    c.Title,
		Type:     c.Type,
		Tier:     string(c.Tier),
		Status:   string(c.Status),
		Scenario: c.Scenario,
		SLA:      c.SLA,
		Loop: loopTOML{
			Predicate:     c.Contract.Predicate,
			MaxIterations: c.Contract.MaxIterations,
			MaxMinutes:    c.Contract.MaxMinutes,
			MaxUSD:        c.Contract.MaxUSD,
			Breaker:       c.Contract.Breaker,
			Boundaries: boundariesTOML{
				Writable: c.Contract.Writable,
				Readonly: c.Contract.ReadOnly,
			},
		},
	}); err != nil {
		return fmt.Errorf("cannot encode change frontmatter: %v", err)
	}
	b.WriteString("+++\n")

	fmt.Fprintf(&b, "\n# %s\n", c.Title)
	if len(c.Delta) > 0 {
		b.WriteString("\n## Delta\n")
		for _, d := range c.Delta {
			b.WriteString("\n")
			r := d.Requirement
			if d.Op == core.DeltaRemoved {
				fmt.Fprintf(&b, "### %s %s\n", d.Op, r.ID)
				continue
			}
			writeRequirement(&b, r, string(d.Op)+" ")
		}
	}
	if len(c.Tasks) > 0 {
		b.WriteString("\n## Tasks\n\n")
		for _, t := range c.Tasks {
			box := " "
			if t.Done {
				box = "x"
			}
			fmt.Fprintf(&b, "- [%s] %s: %s\n", box, t.ID, t.Text)
			if len(t.Consumes) > 0 {
				fmt.Fprintf(&b, "  - Consumes: %s\n", strings.Join(t.Consumes, ", "))
			}
			if len(t.Produces) > 0 {
				fmt.Fprintf(&b, "  - Produces: %s\n", strings.Join(t.Produces, ", "))
			}
		}
	}
	if c.Design != "" {
		fmt.Fprintf(&b, "\n## Design\n\n%s\n", c.Design)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
)

// ChangeSource is the subset of the internal/spec API this package needs
// (LoadChange / ListChanges / ChangePath). loop codes against this seam so it
// builds before internal/spec lands; at integration a one-line RegisterSource
// call (or direct wiring in cli) swaps in the real spec package. The seam is
// intentionally identical in shape to the spec API contract.
type ChangeSource interface {
	LoadChange(repoRoot, id string) (*core.Change, error)
	ListChanges(repoRoot string) ([]string, error)
	ChangePath(repoRoot, id string) string
}

var source ChangeSource = fallbackSource{}

// RegisterSource swaps the change source (integration wires internal/spec
// here; tests inject stubs). Registering nil restores the built-in fallback.
func RegisterSource(s ChangeSource) {
	if s == nil {
		source = fallbackSource{}
		return
	}
	source = s
}

// Source returns the active change source.
func Source() ChangeSource { return source }

// fallbackSource is a minimal change.md reader covering only what the loop
// needs: the TOML loop contract and the task checklist. It is NOT the spec
// package — no delta parsing, no living specs — and exists so this feature
// is self-sufficient until internal/spec is merged.
type fallbackSource struct{}

func (fallbackSource) ChangePath(repoRoot, id string) string {
	return filepath.Join(repoRoot, ".blueprint", "changes", id, "change.md")
}

func (f fallbackSource) ListChanges(repoRoot string) ([]string, error) {
	dir := filepath.Join(repoRoot, ".blueprint", "changes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list changes: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (f fallbackSource) LoadChange(repoRoot, id string) (*core.Change, error) {
	path := f.ChangePath(repoRoot, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("change %q has no change.md at %s — create it with `blueprint new %s` before running the loop", id, path, id)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	text := string(raw)
	c := &core.Change{ID: id, Status: core.StatusDraft}

	frontmatter := extractFrontmatter(text)
	if frontmatter != "" {
		var doc struct {
			Loop struct {
				core.LoopContract
				Breaker    core.Breaker `toml:"breaker"`
				Boundaries struct {
					Writable []string `toml:"writable"`
					ReadOnly []string `toml:"readonly"`
				} `toml:"boundaries"`
			} `toml:"loop"`
		}
		if _, err := toml.Decode(frontmatter, &doc); err != nil {
			return nil, fmt.Errorf("change %q: loop contract TOML in %s does not parse (%v) — fix the frontmatter; see DESIGN §6 for the [loop] schema", id, path, err)
		}
		c.Contract = doc.Loop.LoopContract
		c.Contract.Breaker = doc.Loop.Breaker
		if len(doc.Loop.Boundaries.Writable) > 0 {
			c.Contract.Writable = doc.Loop.Boundaries.Writable
		}
		if len(doc.Loop.Boundaries.ReadOnly) > 0 {
			c.Contract.ReadOnly = doc.Loop.Boundaries.ReadOnly
		}
	}
	if t := firstHeading(text); t != "" {
		c.Title = t
	}
	c.Tasks = parseTasks(text)
	return c, nil
}

// extractFrontmatter accepts the two forms in the wild: +++ delimited TOML at
// the top of the file, or the first fenced ```toml block containing [loop].
func extractFrontmatter(text string) string {
	trimmed := strings.TrimLeft(text, "\r\n \t")
	if strings.HasPrefix(trimmed, "+++") {
		rest := trimmed[3:]
		if i := strings.Index(rest, "+++"); i >= 0 {
			return rest[:i]
		}
	}
	re := regexp.MustCompile("(?s)```toml\\s*\n(.*?)```")
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if strings.Contains(m[1], "[loop]") {
			return m[1]
		}
	}
	return ""
}

func firstHeading(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

var taskLine = regexp.MustCompile(`^\s*[-*]\s*\[([ xX])\]\s*(.+)$`)
var taskID = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*\d+)[:.]\s+`)

// parseTasks reads checklist items. A task ID is a leading token like
// "T3:" or "task-2."; otherwise tasks get ordinal IDs T1..Tn.
func parseTasks(text string) []core.Task {
	var tasks []core.Task
	n := 0
	for _, line := range strings.Split(text, "\n") {
		m := taskLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n++
		body := strings.TrimSpace(m[2])
		id := fmt.Sprintf("T%d", n)
		if im := taskID.FindStringSubmatch(body); im != nil {
			id = im[1]
			body = strings.TrimSpace(body[len(im[0]):])
		}
		tasks = append(tasks, core.Task{
			ID:   id,
			Text: body,
			Done: m[1] == "x" || m[1] == "X",
		})
	}
	return tasks
}

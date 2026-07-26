// Package docscheck pins the human-facing root documents (ADOPT.md,
// QUICKSTART.md, INSTALL.md) to reality: they must exist, keep their required
// sections, name only CLI subcommands that actually exist, and contain no
// dead relative links. The docs promise exact commands and transcripts;
// drifting docs are a worse failure than missing docs.
package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is where the root documents live, relative to this package
// directory (go test runs with the package dir as cwd).
var repoRoot = filepath.Join("..", "..")

// knownSubcommands is the static allowlist of `blueprint <sub>` commands,
// mirroring the cobra registrations under internal/cli (root `blueprint
// --help` output). Adding a command to the CLI that the docs mention means
// adding it here; removing one from the CLI without updating the docs makes
// this list — and the docs — stale together, which is the point: update both.
var knownSubcommands = map[string]bool{
	"adopt":      true,
	"approve":    true,
	"autonomy":   true,
	"bench":      true,
	"close":      true,
	"completion": true,
	"doctor":     true,
	"dream":      true,
	"help":       true,
	"init":       true,
	"lint":       true,
	"log":        true,
	"map":        true,
	"mcp":        true,
	"metrics":    true,
	"new":        true,
	"registry":   true,
	"resume":     true,
	"status":     true,
	"sync":       true,
	"uninstall":  true,
	"upgrade":    true,
	"verify":     true,
	"version":    true,
}

// docs maps each required root document to the section headers it must keep.
// Headers are matched as line prefixes so wording after the em-dash may
// evolve without breaking the pin.
var docs = []struct {
	name            string
	requiredHeaders []string
	requiredPhrases []string
}{
	{
		name: "ADOPT.md",
		requiredHeaders: []string{
			"## Stage 0",
			"## Stage 1",
			"## Stage 2",
			"## Stage 3",
			"## Stage 4",
			"## Ratchet doctrine",
			"## Coexistence with methodology plugins",
			"## Stop-anywhere guarantee",
		},
		requiredPhrases: []string{
			// Every stage's exit check command must be spelled out.
			"blueprint doctor --adopt-stage 0",
			"blueprint doctor --adopt-stage 1",
			"blueprint doctor --adopt-stage 2",
			"blueprint doctor --adopt-stage 3",
			"blueprint doctor --adopt-stage 4",
			// AC-1 fast-path protocol and the interview deliverable.
			"timeboxed to 15 minutes",
			"30-minute interviews",
		},
	},
	{
		name: "QUICKSTART.md",
		requiredHeaders: []string{
			"## 1. Install",
			"## 2. Adopt",
			"## 3. Orient",
			"## 4. First change",
			"## 5. Parallel changes with worktrees",
			"## 6. Wire MCP",
		},
		requiredPhrases: []string{
			"blueprint new --worktree",
			"// verifies: REQ-",
			"blueprint mcp serve",
		},
	},
	{
		name: "INSTALL.md",
		requiredHeaders: []string{
			"## Online install",
			"## Offline / air-gapped install",
			"## Per-OS notes",
			"## Upgrade",
			"## Rollback",
			"## Uninstall",
			"## Network disclosure",
		},
	},
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, name))
	if err != nil {
		t.Fatalf("required root document missing: %v", err)
	}
	return string(b)
}

func TestDocsExistWithRequiredSections(t *testing.T) {
	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			body := readDoc(t, d.name)
			for _, h := range d.requiredHeaders {
				if !hasHeaderPrefix(body, h) {
					t.Errorf("%s: required section header %q not found", d.name, h)
				}
			}
			for _, p := range d.requiredPhrases {
				if !strings.Contains(body, p) {
					t.Errorf("%s: required phrase %q not found", d.name, p)
				}
			}
		})
	}
}

// hasHeaderPrefix reports whether some line of body starts with prefix.
func hasHeaderPrefix(body, prefix string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// TestDocLineBudget: each playbook stays readable in one sitting (<=250
// lines, per the deliverable).
func TestDocLineBudget(t *testing.T) {
	for _, name := range []string{"ADOPT.md", "QUICKSTART.md"} {
		body := readDoc(t, name)
		if n := len(strings.Split(strings.TrimRight(body, "\n"), "\n")); n > 250 {
			t.Errorf("%s: %d lines, budget is 250 — cut, don't append", name, n)
		}
	}
}

// blueprintCmd matches "blueprint <sub>" inside code regions. The word
// "blueprint" must stand alone (not `scoop-blueprint`, a path segment, or an
// env var), the subcommand follows on the same line, and it starts with a
// lowercase letter — so "command -v blueprint)", asset names like
// blueprint-offline-…, and repo slugs don't match.
var blueprintCmd = regexp.MustCompile(`(?:^|[^A-Za-z0-9/_.@-])blueprint +([a-z][a-z-]*)`)

func TestNamedCommandsExist(t *testing.T) {
	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			body := readDoc(t, d.name)
			for _, region := range codeRegions(body) {
				for _, m := range blueprintCmd.FindAllStringSubmatchIndex(region, -1) {
					sub := region[m[2]:m[3]]
					// A URL scheme right after ("blueprint https://…") means
					// blueprint was an argument, not the command.
					if strings.HasPrefix(region[m[3]:], "://") {
						continue
					}
					if !knownSubcommands[sub] {
						t.Errorf("%s: names `blueprint %s`, which is not a CLI subcommand (allowlist in docscheck_test.go mirrors internal/cli)", d.name, sub)
					}
				}
			}
		})
	}
}

// codeRegions extracts fenced code blocks and inline backtick spans — the
// places where docs state commands as commands. Prose is not scanned, so
// sentences like "the blueprint binary" cannot false-positive.
func codeRegions(body string) []string {
	var regions []string
	var fence []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "````") {
			if inFence {
				regions = append(regions, strings.Join(fence, "\n"))
				fence = fence[:0]
			}
			inFence = !inFence
			continue
		}
		if inFence {
			fence = append(fence, line)
			continue
		}
		// Inline spans on prose lines: `...` pairs.
		parts := strings.Split(line, "`")
		for i := 1; i < len(parts); i += 2 {
			regions = append(regions, parts[i])
		}
	}
	return regions
}

var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

func TestNoDeadRelativeLinks(t *testing.T) {
	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			body := readDoc(t, d.name)
			for _, m := range mdLink.FindAllStringSubmatch(body, -1) {
				target := m[1]
				if strings.HasPrefix(target, "http://") ||
					strings.HasPrefix(target, "https://") ||
					strings.HasPrefix(target, "mailto:") {
					continue
				}
				if frag, ok := strings.CutPrefix(target, "#"); ok {
					if !anchorExists(body, frag) {
						t.Errorf("%s: fragment link #%s matches no heading", d.name, frag)
					}
					continue
				}
				path := target
				if i := strings.IndexByte(path, '#'); i >= 0 {
					path = path[:i]
				}
				if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
					t.Errorf("%s: relative link target %q does not exist", d.name, target)
				}
			}
		})
	}
}

// anchorExists implements the GitHub heading-anchor slug: lowercase, spaces
// to hyphens, drop everything that is not a letter, digit, or hyphen.
func anchorExists(body, frag string) bool {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		text := strings.TrimSpace(strings.TrimLeft(line, "#"))
		var b strings.Builder
		for _, r := range strings.ToLower(text) {
			switch {
			case r == ' ':
				b.WriteByte('-')
			case r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
				b.WriteRune(r)
			}
		}
		if b.String() == frag {
			return true
		}
	}
	return false
}

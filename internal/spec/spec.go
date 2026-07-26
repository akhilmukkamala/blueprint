// Package spec parses and serializes Blueprint's two user-authored artifact
// types (DESIGN §3): living specs (.blueprint/specs/<area>/spec.md — YAML
// frontmatter + EARS requirements) and delta changes
// (.blueprint/changes/<id>/change.md — TOML frontmatter + ADDED/MODIFIED/
// REMOVED delta + tasks + optional Design section). It also owns Close, the
// mechanical merge of a verified change into the living specs.
//
// Living-spec file format:
//
//	---
//	id: auth
//	status: approved
//	owner: alice
//	reviewed: 2026-07-21
//	---
//
//	# auth
//
//	### REQ-auth-001 (event-driven)
//
//	When a login request carries invalid credentials, the system shall
//	respond 401 without setting a session cookie.
//
//	verify:
//	- test: TestLoginRejectsInvalid
package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"blueprint/internal/core"
)

// LivingSpec is the public projection of a living spec file.
type LivingSpec struct {
	Area         string
	Path         string
	Status       core.ChangeStatus
	Requirements []core.Requirement
}

// fullSpec keeps every frontmatter field so rewrites (Close) never drop
// user-authored metadata that the public LivingSpec projection omits.
type fullSpec struct {
	Area         string
	Status       string
	Owner        string
	Reviewed     string
	Requirements []core.Requirement
}

type specFrontmatter struct {
	ID       string `yaml:"id"`
	Status   string `yaml:"status"`
	Owner    string `yaml:"owner"`
	Reviewed string `yaml:"reviewed"`
}

// reqHeading matches "### REQ-<area>-NNN (pattern)"; the pattern group is
// optional so the parser can report a targeted remediation when it is missing.
var reqHeading = regexp.MustCompile(`^###\s+(REQ-[A-Za-z0-9-]+-\d+)\s*(?:\(([a-z-]+)\))?\s*$`)

// reqID splits a requirement ID into area and number. The trailing \d+ anchor
// keeps hyphenated areas intact (REQ-user-auth-007 -> area "user-auth").
var reqID = regexp.MustCompile(`^REQ-([A-Za-z0-9-]+)-(\d+)$`)

// verifyEntry matches one "- <kind>: <ref>" line inside a verify: block.
var verifyEntry = regexp.MustCompile(`^-\s+([a-z]+):\s*(.+?)\s*$`)

// AreaOf returns the <area> segment of a REQ-<area>-NNN ID, or "" if the ID
// is malformed.
func AreaOf(id string) string {
	m := reqID.FindStringSubmatch(id)
	if m == nil {
		return ""
	}
	return m[1]
}

func specsDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "specs")
}

func specPath(repoRoot, area string) string {
	return filepath.Join(specsDir(repoRoot), area, "spec.md")
}

// LoadSpec reads .blueprint/specs/<area>/spec.md.
func LoadSpec(repoRoot, area string) (*LivingSpec, error) {
	full, err := loadFullSpec(repoRoot, area)
	if err != nil {
		return nil, err
	}
	return &LivingSpec{
		Area:         full.Area,
		Path:         specPath(repoRoot, area),
		Status:       core.ChangeStatus(full.Status),
		Requirements: full.Requirements,
	}, nil
}

// ListSpecs returns the sorted area names that have a spec.md.
func ListSpecs(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(specsDir(repoRoot))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", specsDir(repoRoot), err)
	}
	var areas []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(specPath(repoRoot, e.Name())); err == nil {
			areas = append(areas, e.Name())
		}
	}
	sort.Strings(areas)
	return areas, nil
}

func loadFullSpec(repoRoot, area string) (*fullSpec, error) {
	path := specPath(repoRoot, area)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no living spec for area %q: expected %s — create it or run `blueprint new` to draft a change that adds it", area, path)
	}
	return parseSpec(path, area, string(raw))
}

func parseSpec(path, area, raw string) (*fullSpec, error) {
	fmRaw, body, err := splitFrontmatter(raw, "---")
	if err != nil {
		return nil, fmt.Errorf("%s: %v — living specs start with a `---` YAML frontmatter block carrying id/status/owner/reviewed", path, err)
	}
	var fm specFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, fmt.Errorf("%s: frontmatter is not valid YAML (%v) — fix the block between the `---` fences", path, err)
	}
	if fm.ID == "" {
		fm.ID = area
	}
	reqs, err := parseRequirements(path, body)
	if err != nil {
		return nil, err
	}
	return &fullSpec{
		Area:         fm.ID,
		Status:       fm.Status,
		Owner:        fm.Owner,
		Reviewed:     fm.Reviewed,
		Requirements: reqs,
	}, nil
}

// parseRequirements scans a markdown body for "### REQ-..." blocks. Delta
// headings (with ADDED/MODIFIED/REMOVED ops) are parsed separately in
// change.go.
func parseRequirements(path, body string) ([]core.Requirement, error) {
	lines := strings.Split(body, "\n")
	var reqs []core.Requirement
	i := 0
	for i < len(lines) {
		line := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(line, "### ") {
			i++
			continue
		}
		m := reqHeading.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s: heading %q is not a requirement heading — use `### REQ-<area>-NNN (pattern)` where pattern is one of ubiquitous|event-driven|state-driven|optional|unwanted|complex", path, line)
		}
		req := core.Requirement{ID: m[1], Pattern: core.EARSPattern(m[2])}
		i++
		var text []string
		for i < len(lines) {
			l := strings.TrimRight(lines[i], "\r")
			if strings.HasPrefix(l, "### ") || strings.HasPrefix(l, "## ") {
				break
			}
			if strings.TrimSpace(l) == "verify:" {
				i++
				for i < len(lines) {
					vl := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
					vm := verifyEntry.FindStringSubmatch(vl)
					if vm == nil {
						break
					}
					req.Verify = append(req.Verify, core.VerifyMethod{Kind: vm[1], Ref: vm[2]})
					i++
				}
				continue
			}
			text = append(text, l)
			i++
		}
		req.Text = strings.TrimSpace(strings.Join(text, "\n"))
		reqs = append(reqs, req)
	}
	return reqs, nil
}

func saveFullSpec(repoRoot string, s *fullSpec) error {
	path := specPath(repoRoot, s.Area)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create spec directory for area %q: %v", s.Area, err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", s.Area)
	fmt.Fprintf(&b, "status: %s\n", s.Status)
	if s.Owner != "" {
		fmt.Fprintf(&b, "owner: %s\n", s.Owner)
	}
	if s.Reviewed != "" {
		fmt.Fprintf(&b, "reviewed: %s\n", s.Reviewed)
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n", s.Area)
	for _, r := range s.Requirements {
		b.WriteString("\n")
		writeRequirement(&b, r, "")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// writeRequirement renders one requirement block; opPrefix is "" for living
// specs and "ADDED "/"MODIFIED "/"REMOVED " inside change deltas.
func writeRequirement(b *strings.Builder, r core.Requirement, opPrefix string) {
	if r.Pattern != "" {
		fmt.Fprintf(b, "### %s%s (%s)\n", opPrefix, r.ID, r.Pattern)
	} else {
		fmt.Fprintf(b, "### %s%s\n", opPrefix, r.ID)
	}
	if r.Text != "" {
		fmt.Fprintf(b, "\n%s\n", r.Text)
	}
	if len(r.Verify) > 0 {
		b.WriteString("\nverify:\n")
		for _, v := range r.Verify {
			fmt.Fprintf(b, "- %s: %s\n", v.Kind, v.Ref)
		}
	}
}

// splitFrontmatter cuts a document into its fenced frontmatter and body.
// fence is "---" (YAML, specs) or "+++" (TOML, changes).
func splitFrontmatter(raw, fence string) (front, body string, err error) {
	// Normalize CRLF so fence detection is Windows-clean.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, fence+"\n") {
		return "", "", fmt.Errorf("missing opening %q frontmatter fence on line 1", fence)
	}
	rest := raw[len(fence)+1:]
	idx := strings.Index(rest, "\n"+fence+"\n")
	if idx < 0 {
		if strings.HasSuffix(rest, "\n"+fence) {
			return rest[:len(rest)-len(fence)-1], "", nil
		}
		return "", "", fmt.Errorf("missing closing %q frontmatter fence", fence)
	}
	return rest[:idx+1], rest[idx+len(fence)+2:], nil
}

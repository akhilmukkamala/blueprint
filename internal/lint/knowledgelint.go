// knowledgelint.go — the knowledge-store linter (DESIGN §9): freshness
// against per-class max-ages (warn at 80%, fail past), dead links, orphan
// detection (a knowledge file no index path reaches fails — unreachable
// knowledge doesn't exist), index line cap, and the relative-date ban.
// Pure static, no model, no network; the clock is injected.
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Per-class freshness max-ages (DESIGN §9): architecture-class docs 90d,
// runbooks 180d, ADRs exempt once accepted.
const (
	KnowledgeDefaultMaxAge = 90 * 24 * time.Hour
	KnowledgeRunbookMaxAge = 180 * 24 * time.Hour
)

// IndexLineCap is the AGENTS.md budget (DESIGN §2 index rules).
const IndexLineCap = 120

// KnowledgeFileInfo describes one knowledge artifact — exported so the
// metrics fold can bucket freshness without re-parsing frontmatter.
type KnowledgeFileInfo struct {
	RelPath  string        // repo-relative, slash-separated
	Class    string        // architecture | runbook | decision | other
	Status   string        // frontmatter status: (empty when absent)
	Reviewed time.Time     // zero when frontmatter has no parsable reviewed:
	MaxAge   time.Duration // 0 = freshness-exempt (accepted ADRs)
	Lines    int
}

func knowledgeDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "knowledge")
}

// KnowledgeFiles walks .blueprint/knowledge and classifies every .md file.
// A missing knowledge dir returns an empty slice — the store appears on
// first use (DESIGN §2) and its absence is not a lint error.
func KnowledgeFiles(repoRoot string) ([]KnowledgeFileInfo, error) {
	dir := knowledgeDir(repoRoot)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, nil
	}
	var infos []KnowledgeFileInfo
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		content := readFileOrEmpty(path)
		front := parseFrontmatter(content)
		info := KnowledgeFileInfo{
			RelPath: relPath(repoRoot, path),
			Status:  front["status"],
			Lines:   countLines(content),
		}
		switch {
		case strings.Contains(info.RelPath, "knowledge/decisions/"):
			info.Class = "decision"
		case strings.Contains(info.RelPath, "knowledge/runbooks/"):
			info.Class = "runbook"
		case strings.HasSuffix(info.RelPath, "architecture.md"):
			info.Class = "architecture"
		default:
			info.Class = "other"
		}
		if t, err := time.Parse("2006-01-02", front["reviewed"]); err == nil {
			info.Reviewed = t
		}
		switch {
		case info.Class == "decision" && adrAccepted(front["status"]):
			info.MaxAge = 0 // ADRs are exempt once accepted
		case info.Class == "runbook":
			info.MaxAge = KnowledgeRunbookMaxAge
		default:
			info.MaxAge = KnowledgeDefaultMaxAge
		}
		infos = append(infos, info)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot walk %s: %w", dir, err)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].RelPath < infos[j].RelPath })
	return infos, nil
}

func adrAccepted(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "accepted", "approved", "verified":
		return true
	}
	return false
}

// Knowledge runs the knowledge-store linter. A repo without a knowledge dir
// yields no findings (the store appears on first use); a repo WITH knowledge
// but no AGENTS.md index fails, because unindexed knowledge is unreachable.

var knowledgeLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

type deadLink struct {
	target string
	line   int
}

// deadLinks returns relative markdown link targets in content that do not
// resolve against the file's directory (or the repo root as a fallback).
func deadLinks(repoRoot, abs, content string) []deadLink {
	var out []deadLink
	for i, l := range strings.Split(content, "\n") {
		for _, m := range knowledgeLinkRe.FindAllStringSubmatch(l, -1) {
			target := strings.SplitN(m[1], "#", 2)[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(m[1], "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			p := filepath.Join(filepath.Dir(abs), filepath.FromSlash(target))
			if _, err := os.Stat(p); err == nil {
				continue
			}
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(target))); err == nil {
				continue
			}
			out = append(out, deadLink{target: target, line: i + 1})
		}
	}
	return out
}

// parseFrontmatter reads a leading `---` fenced block of `key: value` lines.
// Deliberately loose: no YAML dependency (frozen deps), unknown keys ignored.
func parseFrontmatter(content string) map[string]string {
	out := map[string]string{}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

func countLines(content string) int {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Count(strings.TrimSuffix(content, "\n"), "\n") + 1
}

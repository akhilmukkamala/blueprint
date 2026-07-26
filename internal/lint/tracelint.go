package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"blueprint/internal/core"
	"blueprint/internal/spec"
)

// traceAnnotation matches "verifies: REQ-<area>-NNN" in any comment syntax —
// the linter scans raw lines, so //, #, --, /* ... */, <!-- --> all work.
var traceAnnotation = regexp.MustCompile(`(?i)verifies:\s*(REQ-[A-Za-z0-9-]+-\d+)`)

// sourceExts are the file types scanned for trace annotations.
var sourceExts = map[string]bool{
	".go": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".py": true, ".rb": true, ".java": true, ".kt": true, ".scala": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".cs": true, ".rs": true, ".swift": true, ".php": true,
	".sh": true, ".bash": true, ".ps1": true, ".psm1": true, ".bat": true, ".cmd": true,
	".sql": true, ".tf": true, ".yaml": true, ".yml": true,
}

// skipDirs are never walked: tool state, dependency trees, build output.
var skipDirs = map[string]bool{
	".git": true, ".blueprint": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, "target": true, ".idea": true, ".vscode": true,
}

// maxTraceFileBytes caps per-file reads so a stray binary blob with a source
// extension cannot stall the linter.
const maxTraceFileBytes = 4 << 20

type annotation struct {
	file string // repo-relative, forward slashes
	line int
	req  string
}

// Trace runs the bidirectional traceability check (DESIGN §3, DO-178C core):
// every approved/verified requirement needs at least one "verifies: REQ-..."
// annotation in the source tree, and every annotation must name a REQ ID that
// exists in a living spec or an open change's ADDED/MODIFIED delta.
func Trace(repoRoot string, cfg Config) ([]core.LintFinding, error) {
	known := map[string]bool{}    // every resolvable REQ ID
	needed := map[string]string{} // approved REQ ID -> defining spec file

	areas, err := spec.ListSpecs(repoRoot)
	if err != nil {
		return nil, err
	}
	var findings []core.LintFinding
	for _, area := range areas {
		s, err := spec.LoadSpec(repoRoot, area)
		if err != nil {
			// Spec lint owns parse findings; trace just skips the file.
			continue
		}
		approved := s.Status == core.StatusApproved || s.Status == core.StatusVerified
		for _, r := range s.Requirements {
			known[r.ID] = true
			if approved {
				needed[r.ID] = relPath(repoRoot, s.Path)
			}
		}
	}
	ids, err := spec.ListChanges(repoRoot)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c, err := spec.LoadChange(repoRoot, id)
		if err != nil {
			continue
		}
		for _, d := range c.Delta {
			if d.Op == core.DeltaAdded || d.Op == core.DeltaModified {
				known[d.Requirement.ID] = true
			}
		}
	}

	annotations, err := scanAnnotations(repoRoot)
	if err != nil {
		return nil, err
	}

	covered := map[string]bool{}
	for _, a := range annotations {
		if !known[a.req] {
			findings = append(findings, finding(a.file, a.line, "trace-dangling", SevError,
				fmt.Sprintf("annotation names %s, which exists in no living spec or open change.", a.req),
				"Fix the REQ ID, or add the requirement to the relevant .blueprint/specs/<area>/spec.md."))
			continue
		}
		covered[a.req] = true
	}

	missing := make([]string, 0, len(needed))
	for id := range needed {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		findings = append(findings, finding(needed[id], 0, "trace-uncovered", SevError,
			fmt.Sprintf("approved requirement %s has no test annotation anywhere in the source tree.", id),
			fmt.Sprintf("Add a `verifies: %s` comment to the test that exercises it (any comment syntax).", id)))
	}
	return findings, nil
}

// scanAnnotations walks the repo source tree collecting trace annotations in
// deterministic (sorted walk) order.
func scanAnnotations(repoRoot string) ([]annotation, error) {
	var out []annotation
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != repoRoot && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > maxTraceFileBytes {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read %s while scanning trace annotations: %w", path, err)
		}
		rel := relPath(repoRoot, path)
		for i, line := range strings.Split(string(content), "\n") {
			for _, m := range traceAnnotation.FindAllStringSubmatch(line, -1) {
				out = append(out, annotation{file: rel, line: i + 1, req: m[1]})
			}
		}
		return nil
	})
	return out, err
}

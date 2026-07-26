package mcpserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/repomap"
	"blueprint/internal/spec"
)

// spec_lookup is the uniquely-SDD retrieval primitive (DESIGN §9): REQ ID ->
// living-spec text + trace-annotated test files + latest verdict summary.
// Every missing layer degrades honestly instead of erroring.

type verdictSummary struct {
	ChangeID string    `json:"change_id"`
	Time     time.Time `json:"time"`
	Pass     bool      `json:"pass"`
	Tamper   bool      `json:"tamper"`
	Checks   int       `json:"checks"`
	Failing  []string  `json:"failing,omitempty"`
}

func handleSpecLookup(s *Server, args map[string]any) (any, error) {
	reqID := stringArg(args, "req_id")
	if reqID == "" {
		return nil, fmt.Errorf("spec_lookup needs req_id, e.g. {\"req_id\": \"REQ-auth-001\"}")
	}
	area := spec.AreaOf(reqID)
	if area == "" {
		return nil, fmt.Errorf("malformed requirement ID %q — expected REQ-<area>-NNN", reqID)
	}

	result := map[string]any{"req_id": reqID, "area": area}

	req, specErr := findRequirement(s.RepoRoot, area, reqID)
	if specErr != "" {
		result["found"] = false
		result["degraded"] = true
		result["reason"] = specErr
		result["remediation"] = fmt.Sprintf("author the requirement in .blueprint/specs/%s/spec.md or check the ID for typos", area)
	} else {
		result["found"] = true
		result["pattern"] = req.Pattern
		result["text"] = req.Text
		result["verify"] = req.Verify
	}

	tests := tracedTestFiles(s.RepoRoot, reqID)
	result["tests"] = tests
	if len(tests) == 0 {
		result["tests_note"] = fmt.Sprintf("no test file carries a `verifies: %s` annotation yet — the executed-test-count floor is 0", reqID)
	}

	verdicts := verdictHistory(s.RepoRoot, reqID)
	result["verdicts"] = verdicts
	if len(verdicts) > 0 {
		result["latest_verdict"] = verdicts[0]
	} else {
		result["verdict_note"] = "no verifier verdict references this requirement yet — run `blueprint verify <change-id>`"
	}
	return result, nil
}

func findRequirement(repoRoot, area, reqID string) (core.Requirement, string) {
	sp, err := spec.LoadSpec(repoRoot, area)
	if err != nil {
		areas, lerr := spec.ListSpecs(repoRoot)
		if lerr != nil || len(areas) == 0 {
			return core.Requirement{}, "no living specs exist under .blueprint/specs/"
		}
		return core.Requirement{}, fmt.Sprintf("no living spec for area %q (have: %v)", area, areas)
	}
	for _, r := range sp.Requirements {
		if r.ID == reqID {
			return r, ""
		}
	}
	return core.Requirement{}, fmt.Sprintf("%s is not in .blueprint/specs/%s/spec.md", reqID, area)
}

// tracedTestFiles finds test files annotated `verifies: <reqID>` (any
// comment leader — only the token matters, same convention the verifier's
// tamper stack hashes).
func tracedTestFiles(repoRoot, reqID string) []string {
	re := regexp.MustCompile(`verifies:\s*` + regexp.QuoteMeta(reqID) + `\b`)
	files, err := repomap.ScanFiles(repoRoot)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, rel := range files {
		if !isTestFile(filepath.Base(rel)) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		if re.Match(b) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// testFilePatterns mirrors the verifier's trace conventions (internal/verify).
var testFilePatterns = []string{
	"*_test.go", "test_*.py", "*_test.py",
	"*.test.js", "*.test.ts", "*.test.jsx", "*.test.tsx",
	"*.spec.js", "*.spec.ts", "*.spec.jsx", "*.spec.tsx",
	"*_spec.rb", "*Test.java", "*Tests.cs",
}

func isTestFile(name string) bool {
	for _, p := range testFilePatterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// verdictHistory scans open changes whose delta touches reqID and summarizes
// their verdict/verdict.json, newest first. Archived changes are out of
// scope: the map skips .blueprint/archive by design.
func verdictHistory(repoRoot, reqID string) []verdictSummary {
	ids, err := spec.ListChanges(repoRoot)
	if err != nil {
		return nil
	}
	out := []verdictSummary{}
	for _, id := range ids {
		c, err := spec.LoadChange(repoRoot, id)
		if err != nil || !deltaTouches(c, reqID) {
			continue
		}
		vPath := filepath.Join(repoRoot, ".blueprint", "changes", id, "verdict", "verdict.json")
		b, err := os.ReadFile(vPath)
		if err != nil {
			continue
		}
		var v core.Verdict
		if json.Unmarshal(b, &v) != nil {
			continue
		}
		sum := verdictSummary{ChangeID: id, Time: v.Time, Pass: v.Pass, Tamper: v.Tamper, Checks: len(v.Checks)}
		for _, ch := range v.Checks {
			if !ch.Pass {
				sum.Failing = append(sum.Failing, ch.Name)
			}
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

func deltaTouches(c *core.Change, reqID string) bool {
	for _, d := range c.Delta {
		if d.Requirement.ID == reqID {
			return true
		}
	}
	return false
}

package verify

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Trace annotations link tests to requirements: `// verifies: REQ-<area>-NNN`
// (comment leader varies by language; only the token matters). The tamper
// stack hashes every test file carrying an annotation for the change's REQ
// IDs and counts the annotations — the executed-test-count floor (DESIGN §7).

var annotationRe = regexp.MustCompile(`verifies:\s*(REQ-[A-Za-z0-9_-]+)`)

// skipMarkerRes match test-disabling edits across ecosystems. Any appearance
// in an approved test file fails TAMPER: skipping a spec-linked test is a
// verifier-weakening edit (METR reward-hacking evidence, RESEARCH §1.4).
var skipMarkerRes = []*regexp.Regexp{
	regexp.MustCompile(`\bt\.Skip`),                          // Go
	regexp.MustCompile(`\.only\s*\(`),                        // jest/mocha .only
	regexp.MustCompile(`\b(?:x|f)(?:it|describe|test)\s*\(`), // xit/fit/xdescribe...
	regexp.MustCompile(`\b(?:it|describe|test)\.skip\b`),     // jest/mocha .skip
	regexp.MustCompile(`@pytest\.mark\.skip`),                // pytest
	regexp.MustCompile(`@unittest\.skip`),                    // unittest
	regexp.MustCompile(`#\[ignore\]`),                        // rust
}

var testFilePatterns = []string{
	"*_test.go", "test_*.py", "*_test.py",
	"*.test.js", "*.test.ts", "*.test.jsx", "*.test.tsx",
	"*.spec.js", "*.spec.ts", "*.spec.jsx", "*.spec.tsx",
	"*_spec.rb", "*Test.java", "*Tests.cs",
}

var skipDirs = map[string]bool{
	".git": true, ".blueprint": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, "target": true, ".venv": true,
	"__pycache__": true, ".idea": true, ".vscode": true,
}

func isTestFile(name string) bool {
	for _, p := range testFilePatterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// tracedTest is one test file plus the relevant annotations found in it.
type tracedTest struct {
	RelPath     string // slash-separated, repo-relative
	Annotations int    // count of annotations matching the change's REQ IDs
}

// scanTracedTests walks the repo for test files whose trace annotations
// reference any of reqIDs. Deterministic: results sorted by path.
func scanTracedTests(repoRoot string, reqIDs map[string]bool) ([]tracedTest, error) {
	var out []tracedTest
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] && path != repoRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTestFile(info.Name()) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n := 0
		for _, m := range annotationRe.FindAllStringSubmatch(string(data), -1) {
			if reqIDs[m[1]] {
				n++
			}
		}
		if n == 0 {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		out = append(out, tracedTest{RelPath: filepath.ToSlash(rel), Annotations: n})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

// findSkipMarkers returns human-readable descriptions of any test-disabling
// markers present in the file content.
func findSkipMarkers(content string) []string {
	var hits []string
	for _, re := range skipMarkerRes {
		if m := re.FindString(content); m != "" {
			hits = append(hits, strings.TrimSpace(m))
		}
	}
	return hits
}

// Package testcmd detects a repository's canonical test command from its
// build-system marker files. It backs the {{TEST_CMD}} token in the bundled
// verifier pack (DESIGN §5/§7): repos differ in how tests run, so the pack
// references the token and internal/verify resolves it — from the [test]
// table in .blueprint/config.toml when set, otherwise from Detect.
//
// Pure file inspection only: no exec, no network (CONTRACTS: netaudit),
// filepath.Join throughout (Windows-clean).
package testcmd

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Detect inspects repoRoot for well-known build files and returns the
// conventional test command for the first match, with the marker file that
// justified it. ok is false when nothing detectable is present — callers must
// then ask the user to set [test] command in .blueprint/config.toml rather
// than guess.
//
// Detection order (first hit wins): package.json with a "test" script,
// go.mod, pyproject.toml / pytest.ini, Cargo.toml, Makefile with a `test`
// target.
func Detect(repoRoot string) (cmd string, source string, ok bool) {
	if hasNpmTestScript(filepath.Join(repoRoot, "package.json")) {
		return "npm test", "package.json", true
	}
	if fileExists(filepath.Join(repoRoot, "go.mod")) {
		return "go test ./...", "go.mod", true
	}
	for _, marker := range []string{"pyproject.toml", "pytest.ini"} {
		if fileExists(filepath.Join(repoRoot, marker)) {
			return "pytest", marker, true
		}
	}
	if fileExists(filepath.Join(repoRoot, "Cargo.toml")) {
		return "cargo test", "Cargo.toml", true
	}
	if hasMakeTestTarget(filepath.Join(repoRoot, "Makefile")) {
		return "make test", "Makefile", true
	}
	return "", "", false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// hasNpmTestScript reports whether package.json declares a non-empty
// scripts.test. A bare package.json without one gets npm's placeholder
// "Error: no test specified" script — running that would be a guaranteed
// false failure, so it does not count as detection.
func hasNpmTestScript(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return false
	}
	t := strings.TrimSpace(pkg.Scripts["test"])
	return t != "" && !strings.Contains(t, "no test specified")
}

// hasMakeTestTarget reports whether the Makefile defines a `test` target
// (a line starting `test:`, allowing `test: deps` style prerequisites).
func hasMakeTestTarget(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "test:") || strings.HasPrefix(line, "test :") {
			return true
		}
	}
	return false
}

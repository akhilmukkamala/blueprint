package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureFiles is a small polyglot tree: Go (two packages, one import edge),
// JS/TS (relative imports), Python (module + package import), plus an
// unknown-language file that must degrade to a file-level entry.
var fixtureFiles = map[string]string{
	"go.mod": "module example.com/fix\n\ngo 1.22\n",
	"pkg/util/util.go": `package util

// Helper is imported by main.
func Helper() int { return 1 }

type Thing struct{}

func (t *Thing) Do() {}
`,
	"main.go": `package main

import (
	"fmt"

	"example.com/fix/pkg/util"
)

func main() { fmt.Println(util.Helper()) }
`,
	"web/app.js": `import { render } from './lib/dom';
const start = () => render();
export class App {
  boot() {}
}
`,
	"web/lib/dom.js": `export function render() {}
`,
	"web/types.ts": `export interface Shape { area(): number }
export type ID = string;
export function makeID(): ID { return "x"; }
`,
	"py/service.py": `import helpers
from py.models import User

def serve():
    pass

class Service:
    def handle(self):
        pass
`,
	"py/models.py": `class User:
    pass
`,
	"helpers.py": `def util():
    pass
`,
	"notes.txt":     "not code\n",
	"legacy/old.rb": "def ruby_thing; end\n",
}

func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range fixtureFiles {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func buildFixture(t *testing.T) *Map {
	t.Helper()
	m, err := Build(writeFixture(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

func symbolNames(f *File) []string {
	var out []string
	for _, s := range f.Symbols {
		out = append(out, s.Name)
	}
	return out
}

func TestBuildExtractsSymbols(t *testing.T) {
	m := buildFixture(t)
	cases := []struct {
		path string
		want []string
	}{
		{"pkg/util/util.go", []string{"Helper", "Thing", "Thing.Do"}},
		{"main.go", []string{"main"}},
		{"web/app.js", []string{"start", "App", "App.boot"}},
		{"web/types.ts", []string{"Shape", "ID", "makeID"}},
		{"py/service.py", []string{"serve", "Service", "Service.handle"}},
	}
	for _, tc := range cases {
		f := m.Lookup(tc.path)
		if f == nil {
			t.Fatalf("%s missing from map", tc.path)
		}
		got := symbolNames(f)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s symbols = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestBuildResolvesImports(t *testing.T) {
	m := buildFixture(t)
	cases := []struct {
		path string
		want []string
	}{
		{"main.go", []string{"pkg/util/util.go"}}, // stdlib fmt dropped
		{"web/app.js", []string{"web/lib/dom.js"}},
		{"py/service.py", []string{"helpers.py", "py/models.py"}},
		{"legacy/old.rb", nil}, // unknown language: file-level entry only
	}
	for _, tc := range cases {
		f := m.Lookup(tc.path)
		if f == nil {
			t.Fatalf("%s missing from map", tc.path)
		}
		got := strings.Join(f.Imports, ",")
		if got != strings.Join(tc.want, ",") {
			t.Errorf("%s imports = %q, want %q", tc.path, got, strings.Join(tc.want, ","))
		}
	}
}

func TestUnknownLanguageDegradesToFileEntry(t *testing.T) {
	m := buildFixture(t)
	f := m.Lookup("legacy/old.rb")
	if f == nil {
		t.Fatal("legacy/old.rb should be in the map as a file-level entry")
	}
	if f.Lang != "" || len(f.Symbols) != 0 {
		t.Errorf("unknown language should carry no symbols, got lang=%q symbols=%v", f.Lang, f.Symbols)
	}
	if m.Lookup("notes.txt") == nil {
		t.Error("notes.txt should be present as a file-level entry")
	}
}

func TestRankImportedFileOutranksLeaf(t *testing.T) {
	m := buildFixture(t)
	util := m.Lookup("pkg/util/util.go")
	leaf := m.Lookup("notes.txt")
	if util.Rank <= leaf.Rank {
		t.Errorf("imported util.go rank %f should exceed unreferenced notes.txt %f", util.Rank, leaf.Rank)
	}
}

// TestRenderGolden pins the exact rendered map for the fixture tree. Churn
// is zero (no git repo), so ordering is pure PageRank with path tiebreak —
// fully deterministic.
func TestRenderGolden(t *testing.T) {
	m := buildFixture(t)
	got := m.Render(DefaultBudget)
	golden := filepath.Join("testdata", "map.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Errorf("rendered map drifted from golden:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestRenderBudgetClips(t *testing.T) {
	m := buildFixture(t)
	out := m.Render(10) // 40 chars: forces the clip trailer
	if !strings.Contains(out, "more files") {
		t.Errorf("tiny budget should clip and say so, got:\n%s", out)
	}
	full := m.Render(100000)
	if strings.Contains(full, "more files") {
		t.Errorf("huge budget should show everything, got trailer:\n%s", full)
	}
}

func TestRadiusTransitiveDependents(t *testing.T) {
	m := buildFixture(t)
	// util.go is imported by main.go: radius = {util.go, main.go}.
	n, err := m.Radius([]string{"pkg/util/util.go"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Radius(util.go) = %d, want 2 (self + main.go)", n)
	}
	// dom.js <- app.js; nothing imports app.js.
	deps := m.Dependents([]string{"web/lib/dom.js"})
	want := "web/app.js,web/lib/dom.js"
	if strings.Join(deps, ",") != want {
		t.Errorf("Dependents(dom.js) = %v, want %s", deps, want)
	}
	// Unknown path still counts itself (never below the PathCount fallback).
	n, err = m.Radius([]string{"no/such/file.zig"})
	if err != nil || n != 1 {
		t.Errorf("Radius(unknown) = %d, %v; want 1, nil", n, err)
	}
}

func TestCacheRoundTripAndInvalidation(t *testing.T) {
	root := writeFixture(t)
	m1, err := LoadOrBuild(root, false)
	if err != nil {
		t.Fatalf("first LoadOrBuild: %v", err)
	}
	if _, err := os.Stat(CachePath(root)); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	m2, err := LoadOrBuild(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m2.Generated.Equal(m1.Generated) {
		t.Error("unchanged tree should serve the cached map (Generated must match)")
	}
	// Touch a source file with different content: cache must invalidate.
	p := filepath.Join(root, "helpers.py")
	if err := os.WriteFile(p, []byte("def util():\n    pass\n\ndef extra():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpMTime(t, p)
	m3, err := LoadOrBuild(root, false)
	if err != nil {
		t.Fatal(err)
	}
	f := m3.Lookup("helpers.py")
	if len(f.Symbols) != 2 {
		t.Errorf("cache did not invalidate on source change: helpers.py symbols = %v", symbolNames(f))
	}
}

func bumpMTime(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	newTime := info.ModTime().Add(2e9) // +2s beats coarse filesystem mtime
	if err := os.Chtimes(path, newTime, newTime); err != nil {
		t.Fatal(err)
	}
}

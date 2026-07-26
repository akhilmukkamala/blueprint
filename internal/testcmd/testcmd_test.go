package testcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		wantCmd    string
		wantSource string
		wantOK     bool
	}{
		{
			name:       "npm with test script",
			files:      map[string]string{"package.json": `{"name":"x","scripts":{"test":"vitest run"}}`},
			wantCmd:    "npm test",
			wantSource: "package.json",
			wantOK:     true,
		},
		{
			name: "npm placeholder test script does not count",
			files: map[string]string{
				"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`,
			},
			wantOK: false,
		},
		{
			name:   "npm without scripts does not count",
			files:  map[string]string{"package.json": `{"name":"x"}`},
			wantOK: false,
		},
		{
			name:   "malformed package.json does not count",
			files:  map[string]string{"package.json": `{not json`},
			wantOK: false,
		},
		{
			name:       "go module",
			files:      map[string]string{"go.mod": "module example.com/m\n\ngo 1.22\n"},
			wantCmd:    "go test ./...",
			wantSource: "go.mod",
			wantOK:     true,
		},
		{
			name:       "pyproject",
			files:      map[string]string{"pyproject.toml": "[project]\nname = \"m\"\n"},
			wantCmd:    "pytest",
			wantSource: "pyproject.toml",
			wantOK:     true,
		},
		{
			name:       "pytest.ini",
			files:      map[string]string{"pytest.ini": "[pytest]\n"},
			wantCmd:    "pytest",
			wantSource: "pytest.ini",
			wantOK:     true,
		},
		{
			name:       "cargo",
			files:      map[string]string{"Cargo.toml": "[package]\nname = \"m\"\n"},
			wantCmd:    "cargo test",
			wantSource: "Cargo.toml",
			wantOK:     true,
		},
		{
			name:       "makefile with test target",
			files:      map[string]string{"Makefile": "build:\n\tgo build\n\ntest: build\n\tgo test ./...\n"},
			wantCmd:    "make test",
			wantSource: "Makefile",
			wantOK:     true,
		},
		{
			name:   "makefile without test target",
			files:  map[string]string{"Makefile": "build:\n\tgo build\n"},
			wantOK: false,
		},
		{
			name: "package.json wins over go.mod (detection order)",
			files: map[string]string{
				"package.json": `{"scripts":{"test":"jest"}}`,
				"go.mod":       "module example.com/m\n",
			},
			wantCmd:    "npm test",
			wantSource: "package.json",
			wantOK:     true,
		},
		{
			name: "go.mod wins over Makefile",
			files: map[string]string{
				"go.mod":   "module example.com/m\n",
				"Makefile": "test:\n\tgo test ./...\n",
			},
			wantCmd:    "go test ./...",
			wantSource: "go.mod",
			wantOK:     true,
		},
		{
			name:   "empty repo",
			files:  nil,
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range c.files {
				writeFile(t, root, rel, content)
			}
			cmd, source, ok := Detect(root)
			if ok != c.wantOK || cmd != c.wantCmd || source != c.wantSource {
				t.Errorf("Detect = (%q, %q, %v), want (%q, %q, %v)", cmd, source, ok, c.wantCmd, c.wantSource, c.wantOK)
			}
		})
	}
}

func TestDetectIgnoresDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Detect(root); ok {
		t.Error("a directory named go.mod must not count as a marker file")
	}
}

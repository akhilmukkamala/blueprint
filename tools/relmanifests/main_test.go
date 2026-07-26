package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files from current generator output")

// repoRoot is two levels up from tools/relmanifests.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGeneratorGolden(t *testing.T) {
	root := repoRoot(t)
	out := t.TempDir()
	opts := options{
		ChecksumsPath: filepath.Join("testdata", "checksums.txt"),
		Version:       "1.2.3",
		Repo:          "akhilmukkamala/blueprint",
		WingetID:      "AkhilMukkamala.Blueprint",
		Publisher:     "Akhil Mukkamala",
		TemplatesDir:  filepath.Join(root, "dist"),
		OutDir:        out,
	}
	if err := run(opts); err != nil {
		t.Fatalf("run: %v", err)
	}

	rels := []string{
		filepath.Join("homebrew", "blueprint.rb"),
		filepath.Join("scoop", "blueprint.json"),
		filepath.Join("winget", "blueprint.yaml"),
		filepath.Join("winget", "blueprint.installer.yaml"),
		filepath.Join("winget", "blueprint.locale.en-US.yaml"),
	}
	for _, rel := range rels {
		got, err := os.ReadFile(filepath.Join(out, rel))
		if err != nil {
			t.Fatalf("missing output %s: %v", rel, err)
		}
		goldenPath := filepath.Join("testdata", "golden", rel)
		if *update {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("missing golden %s (regenerate with `go test -run Golden -update ./tools/relmanifests`): %v", goldenPath, err)
		}
		// Compare with normalized line endings so goldens survive Windows
		// checkouts with autocrlf.
		if normalize(got) != normalize(want) {
			t.Errorf("%s differs from golden %s;\n--- got ---\n%s", rel, goldenPath, got)
		}
	}
}

func normalize(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func TestGeneratorOutputsHaveNoPlaceholders(t *testing.T) {
	root := repoRoot(t)
	out := t.TempDir()
	opts := options{
		ChecksumsPath: filepath.Join("testdata", "checksums.txt"),
		Version:       "1.2.3",
		Repo:          "akhilmukkamala/blueprint",
		WingetID:      "AkhilMukkamala.Blueprint",
		Publisher:     "Akhil Mukkamala",
		TemplatesDir:  filepath.Join(root, "dist"),
		OutDir:        out,
	}
	if err := run(opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	err := filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := placeholderRe.FindAll(b, -1); m != nil {
			t.Errorf("%s still contains placeholders: %s", path, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneratorRejectsWrongVersion(t *testing.T) {
	root := repoRoot(t)
	opts := options{
		ChecksumsPath: filepath.Join("testdata", "checksums.txt"),
		Version:       "9.9.9", // fixture is 1.2.3
		Repo:          "akhilmukkamala/blueprint",
		WingetID:      "AkhilMukkamala.Blueprint",
		Publisher:     "Akhil Mukkamala",
		TemplatesDir:  filepath.Join(root, "dist"),
		OutDir:        t.TempDir(),
	}
	if err := run(opts); err == nil {
		t.Fatal("expected error for checksums file of a different version, got nil")
	}
}

func TestParseChecksumsRejectsMalformed(t *testing.T) {
	cases := []struct{ name, data string }{
		{"short hash", "abcd  blueprint-1.2.3-linux-amd64.tar.gz"},
		{"non-hex hash", strings.Repeat("z", 64) + "  blueprint-1.2.3-linux-amd64.tar.gz"},
		{"missing filename", strings.Repeat("a", 64)},
		{"empty file", "\n\n"},
	}
	for _, tc := range cases {
		if _, err := parseChecksums(tc.data, "1.2.3"); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestParseChecksumsIgnoresExtraAssets(t *testing.T) {
	data := strings.Repeat("a", 64) + "  blueprint-1.2.3-linux-amd64.tar.gz\n" +
		strings.Repeat("b", 64) + "  blueprint-offline-1.2.3-linux-amd64.tar.gz\n" +
		strings.Repeat("c", 64) + "  blueprint-sbom-1.2.3.spdx.json\n"
	sums, err := parseChecksums(data, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums["LINUX_AMD64"] != strings.Repeat("a", 64) {
		t.Errorf("unexpected sums: %v", sums)
	}
}

// TestInstallShIsPOSIX is a shellcheck-lite: the installer must run under
// plain /bin/sh (dash, ash), so common bashisms are forbidden.
func TestInstallShIsPOSIX(t *testing.T) {
	path := filepath.Join(repoRoot(t), "dist", "install.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dist/install.sh missing: %v", err)
	}
	src := string(b)
	if !strings.HasPrefix(src, "#!/bin/sh") {
		t.Errorf("install.sh must start with #!/bin/sh, got %q", strings.SplitN(src, "\n", 2)[0])
	}
	bashisms := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"[[ ]] test", regexp.MustCompile(`\[\[`)},
		{"local keyword", regexp.MustCompile(`(?m)^\s*local\s`)},
		{"array assignment", regexp.MustCompile(`(?m)^\s*\w+=\(`)},
		{"function keyword", regexp.MustCompile(`(?m)^\s*function\s+\w`)},
		{"echo -e", regexp.MustCompile(`(?m)\becho\s+-e\b`)},
		{"source builtin", regexp.MustCompile(`(?m)^\s*source\s`)},
		{"$'...' quoting", regexp.MustCompile(`\$'`)},
		{"== in test", regexp.MustCompile(`\[ [^]]*==`)},
		{"select loop", regexp.MustCompile(`(?m)^\s*select\s+\w+\s+in\s`)},
		{"process substitution", regexp.MustCompile(`<\(`)},
	}
	for _, b := range bashisms {
		if loc := b.re.FindStringIndex(src); loc != nil {
			line := 1 + strings.Count(src[:loc[0]], "\n")
			t.Errorf("install.sh line %d: bashism (%s) — must stay POSIX sh", line, b.name)
		}
	}
}

// TestInstallMdSections pins the INSTALL.md contract from DESIGN §11 /
// brief deliverable 7: every documented path must have its section.
func TestInstallMdSections(t *testing.T) {
	path := filepath.Join(repoRoot(t), "INSTALL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("INSTALL.md missing at repo root: %v", err)
	}
	doc := string(b)
	sections := []string{
		"# Installing Blueprint",
		"## Online install",
		"## Offline / air-gapped install",
		"## Per-OS notes",
		"## Versioning policy",
		"## Upgrade",
		"## Rollback",
		"## Uninstall",
		"## Network disclosure (AC-12)",
	}
	for _, s := range sections {
		if !strings.Contains(doc, s) {
			t.Errorf("INSTALL.md is missing required section %q", s)
		}
	}
	// The AC-12 opt-in list must name every permitted network path.
	for _, phrase := range []string{"model-checker", "batch model API", "release fetch", "Tier-2/3", "no telemetry", "init --offline"} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("INSTALL.md network/offline coverage is missing %q", phrase)
		}
	}
}

// relmanifests fills the distribution-manifest templates under dist/
// (Homebrew formula, scoop manifest, winget manifest set) from a GitHub
// release checksums file, so published manifests always carry the exact
// sha256 of the released artifacts. Templates use {{PLACEHOLDER}} tokens and
// plain string substitution (no text/template) because the target formats —
// Ruby, JSON, YAML — all contain braces and sigils of their own.
//
// Usage:
//
//	go run ./tools/relmanifests -checksums checksums.txt -version 1.2.3 -out dist/generated
//
// The checksums file is the standard release format: one "<sha256>  <filename>"
// line per artifact, filenames shaped blueprint-<version>-<os>-<arch>.{tar.gz,zip}.
// Generation fails loudly if a template placeholder cannot be resolved — a
// half-filled manifest must never ship.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// platforms is the closed set of release binaries the manifests reference.
// Matching is by exact expected filename (not regex over all assets) so that
// extra release artifacts — SBOMs, provenance, offline bundles — can coexist
// in the same checksums.txt without confusing generation.
var platforms = []struct{ OS, Arch, Ext string }{
	{"darwin", "arm64", "tar.gz"},
	{"darwin", "amd64", "tar.gz"},
	{"linux", "amd64", "tar.gz"},
	{"linux", "arm64", "tar.gz"},
	{"windows", "amd64", "zip"},
}

// placeholderRe matches any unresolved {{TOKEN}} left after substitution.
var placeholderRe = regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)

type options struct {
	ChecksumsPath string
	Version       string
	Repo          string
	WingetID      string
	Publisher     string
	TemplatesDir  string
	OutDir        string
}

func main() {
	var opts options
	flag.StringVar(&opts.ChecksumsPath, "checksums", "", "path to the release checksums.txt (required)")
	flag.StringVar(&opts.Version, "version", "", "release version, e.g. 1.2.3 (required, no leading v)")
	flag.StringVar(&opts.Repo, "repo", "AkhilMukkamala/blueprint", "GitHub owner/repo slug")
	flag.StringVar(&opts.WingetID, "winget-id", "AkhilMukkamala.Blueprint", "winget PackageIdentifier")
	flag.StringVar(&opts.Publisher, "publisher", "Akhil Mukkamala", "winget Publisher")
	flag.StringVar(&opts.TemplatesDir, "templates", "dist", "directory holding the manifest templates")
	flag.StringVar(&opts.OutDir, "out", filepath.Join("dist", "generated"), "output directory")
	flag.Parse()

	if opts.ChecksumsPath == "" || opts.Version == "" {
		fmt.Fprintln(os.Stderr, "relmanifests: -checksums and -version are required; run with -h for usage")
		os.Exit(2)
	}
	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "relmanifests: error: %v\n", err)
		os.Exit(1)
	}
}

// parseChecksums returns a map of "<OS>_<ARCH>" (upper-case) to sha256 hex
// for the known release binaries, rejecting malformed lines so a corrupted
// checksums file cannot silently produce half-filled manifests. A checksums
// file for a different version yields zero matches and errors, catching the
// stale-file mistake.
func parseChecksums(data string, version string) (map[string]string, error) {
	byName := map[string]string{}
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("checksums line %d is not '<sha256>  <filename>': %q", i+1, line)
		}
		sum, name := strings.ToLower(fields[0]), fields[1]
		if len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("checksums line %d: %q is not a sha256 hex digest", i+1, fields[0])
		}
		if prev, dup := byName[name]; dup && prev != sum {
			return nil, fmt.Errorf("conflicting checksums for %s", name)
		}
		byName[name] = sum
	}

	sums := map[string]string{}
	for _, p := range platforms {
		name := fmt.Sprintf("blueprint-%s-%s-%s.%s", version, p.OS, p.Arch, p.Ext)
		if sum, ok := byName[name]; ok {
			sums[strings.ToUpper(p.OS+"_"+p.Arch)] = sum
		}
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("no blueprint %s release binaries found in checksums file — wrong file or wrong -version?", version)
	}
	return sums, nil
}

// fill substitutes every placeholder and fails if any {{TOKEN}} survives,
// including SHA256 tokens for platforms absent from the checksums file.
func fill(template string, repl map[string]string) (string, error) {
	out := template
	for k, v := range repl {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	if left := placeholderRe.FindAllString(out, -1); left != nil {
		return "", fmt.Errorf("unresolved placeholders %v — checksums file is missing those artifacts", left)
	}
	return out, nil
}

func run(opts options) error {
	raw, err := os.ReadFile(opts.ChecksumsPath)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}
	sums, err := parseChecksums(string(raw), opts.Version)
	if err != nil {
		return err
	}

	repl := map[string]string{
		"VERSION":   opts.Version,
		"REPO":      opts.Repo,
		"WINGET_ID": opts.WingetID,
		"PUBLISHER": opts.Publisher,
	}
	for key, sum := range sums {
		repl["SHA256_"+key] = sum
		// winget's InstallerSha256 is conventionally upper-case hex.
		repl["SHA256_"+key+"_UPPER"] = strings.ToUpper(sum)
	}

	// Template -> output, relative to TemplatesDir / OutDir.
	files := []string{
		filepath.Join("homebrew", "blueprint.rb"),
		filepath.Join("scoop", "blueprint.json"),
		filepath.Join("winget", "blueprint.yaml"),
		filepath.Join("winget", "blueprint.installer.yaml"),
		filepath.Join("winget", "blueprint.locale.en-US.yaml"),
	}
	for _, rel := range files {
		src := filepath.Join(opts.TemplatesDir, rel)
		tmpl, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("reading template: %w", err)
		}
		filled, err := fill(string(tmpl), repl)
		if err != nil {
			return fmt.Errorf("%s: %w", src, err)
		}
		dst := filepath.Join(opts.OutDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(filled), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", dst)
	}
	return nil
}

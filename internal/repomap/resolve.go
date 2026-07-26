package repomap

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// resolveImports rewrites each file's raw import strings (as parsed) into
// repo-relative slash paths of files inside the repo. External modules
// resolve to nothing and are dropped: the graph only ranks what we own.
func resolveImports(repoRoot string, files []*File) {
	idx := newResolveIndex(repoRoot, files)
	for _, f := range files {
		raw := f.Imports
		f.Imports = nil
		seen := map[string]bool{}
		for _, imp := range raw {
			for _, target := range idx.resolve(f, imp) {
				if target != f.Path && !seen[target] {
					seen[target] = true
					f.Imports = append(f.Imports, target)
				}
			}
		}
		sort.Strings(f.Imports)
	}
}

type resolveIndex struct {
	byPath   map[string]bool
	goModule string              // module path from go.mod, "" if absent
	goPkgs   map[string][]string // package dir -> .go files in it
}

var goModuleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func newResolveIndex(repoRoot string, files []*File) *resolveIndex {
	idx := &resolveIndex{byPath: map[string]bool{}, goPkgs: map[string][]string{}}
	for _, f := range files {
		idx.byPath[f.Path] = true
		if f.Lang == "go" {
			dir := filepath.ToSlash(filepath.Dir(f.Path))
			idx.goPkgs[dir] = append(idx.goPkgs[dir], f.Path)
		}
	}
	if b, err := os.ReadFile(filepath.Join(repoRoot, "go.mod")); err == nil {
		if m := goModuleRe.FindSubmatch(b); m != nil {
			idx.goModule = string(m[1])
		}
	}
	return idx
}

func (idx *resolveIndex) resolve(f *File, imp string) []string {
	switch f.Lang {
	case "go":
		return idx.resolveGo(imp)
	case "javascript", "typescript", "tsx":
		return idx.resolveJS(f.Path, imp)
	case "python":
		return idx.resolvePython(f.Path, imp)
	}
	return nil
}

// resolveGo maps a module-internal import path to every .go file in the
// package directory: a Go import is a package-level edge by construction.
func (idx *resolveIndex) resolveGo(imp string) []string {
	if idx.goModule == "" {
		return nil
	}
	var dir string
	switch {
	case imp == idx.goModule:
		dir = "."
	case strings.HasPrefix(imp, idx.goModule+"/"):
		dir = strings.TrimPrefix(imp, idx.goModule+"/")
	default:
		return nil
	}
	return idx.goPkgs[dir]
}

var jsExts = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"}

// resolveJS handles relative specifiers only; bare specifiers are external.
func (idx *resolveIndex) resolveJS(fromPath, imp string) []string {
	if !strings.HasPrefix(imp, "./") && !strings.HasPrefix(imp, "../") {
		return nil
	}
	base := joinSlash(filepath.ToSlash(filepath.Dir(fromPath)), imp)
	if idx.byPath[base] {
		return []string{base}
	}
	for _, ext := range jsExts {
		if idx.byPath[base+ext] {
			return []string{base + ext}
		}
	}
	for _, ext := range jsExts {
		if p := base + "/index" + ext; idx.byPath[p] {
			return []string{p}
		}
	}
	return nil
}

// resolvePython maps "a.b.c" (or ".rel.mod" from-imports) to a/b/c.py or the
// package __init__.py, trying repo-root-absolute then from-file-relative.
func (idx *resolveIndex) resolvePython(fromPath, imp string) []string {
	fromDir := filepath.ToSlash(filepath.Dir(fromPath))
	mod := imp
	base := ""
	if strings.HasPrefix(mod, ".") {
		// one leading dot = current package; each extra dot walks up one.
		mod = strings.TrimPrefix(mod, ".")
		base = fromDir
		for strings.HasPrefix(mod, ".") {
			mod = strings.TrimPrefix(mod, ".")
			base = filepath.ToSlash(filepath.Dir(base))
		}
	}
	rel := strings.ReplaceAll(mod, ".", "/")
	candidates := []string{}
	if base != "" {
		candidates = append(candidates, joinSlash(base, rel))
	} else {
		candidates = append(candidates, rel, joinSlash(fromDir, rel))
	}
	for _, c := range candidates {
		if idx.byPath[c+".py"] {
			return []string{c + ".py"}
		}
		if p := c + "/__init__.py"; idx.byPath[p] {
			return []string{p}
		}
	}
	return nil
}

// joinSlash is path.Join semantics on slash paths (keeps Windows-clean rule
// by never mixing separators).
func joinSlash(a, b string) string {
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(a), filepath.FromSlash(b)))
}

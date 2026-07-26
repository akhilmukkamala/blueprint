package repomap

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Directory skip set (DESIGN §9): .git, node_modules, vendor always; and
// .blueprint itself — the map orients agents in the CODEBASE; spec content is
// served by the spec_lookup MCP tool and framework state files would only
// burn map budget. Other dot-directories are tool caches and skip wholesale.
var skipDirNames = map[string]bool{
	".blueprint":   true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	"target":       true,
}

// maxFileSize keeps generated bundles and data blobs out of the map.
const maxFileSize = 1 << 20 // 1 MiB

// walkRepo lists mappable files with identity metadata (no parsing). Paths
// are repo-relative and slash-separated (Windows-clean, CONTRACTS rule 6).
func walkRepo(repoRoot string) ([]*File, error) {
	var out []*File
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries degrade to absence, not failure
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || skipDirNames[name] {
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".bak") {
			return nil
		}
		// The map's own cache can never be a map subject: it would
		// invalidate itself on every rebuild.
		if rel == ".blueprint/map.json" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > maxFileSize {
			return nil
		}
		if isBinary(path) {
			return nil
		}
		out = append(out, &File{
			Path:  rel,
			Lang:  langOf(rel),
			Size:  info.Size(),
			MTime: info.ModTime().UnixNano(),
		})
		return nil
	})
	return out, err
}

// ScanFiles lists the mappable file paths (repo-relative, slash-separated,
// sorted) using the map's walk rules — shared with the MCP lexical search so
// search and map agree on what "the repo" is.
func ScanFiles(repoRoot string) ([]string, error) {
	files, err := walkRepo(repoRoot)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	sort.Strings(out)
	return out, nil
}

// isBinary sniffs the first 8 KiB for a NUL byte.
func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

// langOf maps an extension to a grammar name; "" means file-level fallback.
func langOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".py":
		return "python"
	}
	return ""
}

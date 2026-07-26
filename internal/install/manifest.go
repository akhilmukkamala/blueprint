package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"blueprint/internal/core"
)

// ManifestEntry records one installed file. SHA256 is the whole-file hash as
// written; for mixed files ManagedSHA256 additionally pins the managed-region
// content so upgrade can tell "user edited the body" (fine) from "user edited
// the managed region" (conflict).
type ManifestEntry struct {
	SHA256        string             `json:"sha256"`
	Tier          core.OwnershipTier `json:"tier"`
	ManagedSHA256 string             `json:"managed_sha256,omitempty"`
}

// Manifest is .blueprint/manifest.json: the install record upgrade and
// uninstall operate from. Keys are repo-relative slash-separated paths.
type Manifest struct {
	// Version is the TEMPLATES version (TemplatesVersion) the layout was
	// written from — the three-way-merge base for upgrades. BinaryVersion
	// records which blueprint binary performed the install (ldflags-stamped;
	// "0.0.0-dev" for source builds). They evolve independently.
	Version       string                   `json:"templates_version"`
	BinaryVersion string                   `json:"binary_version,omitempty"`
	Files         map[string]ManifestEntry `json:"files"`
}

// ManifestPath returns the manifest location under repoRoot.
func ManifestPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "manifest.json")
}

// LoadManifest reads the install manifest. A missing manifest returns
// (nil, nil): the repo has no Blueprint install.
func LoadManifest(repoRoot string) (*Manifest, error) {
	b, err := os.ReadFile(ManifestPath(repoRoot))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("install: cannot read %s: %w", ManifestPath(repoRoot), err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("install: %s is corrupt (%v) — restore it from git or re-run `blueprint init` after removing it", ManifestPath(repoRoot), err)
	}
	if m.Files == nil {
		m.Files = map[string]ManifestEntry{}
	}
	return &m, nil
}

// Save writes the manifest atomically (temp file + rename in the same
// directory — Windows-clean, no symlinks).
func (m *Manifest) Save(repoRoot string) error {
	p := ManifestPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("install: cannot create %s — check directory permissions: %w", filepath.Dir(p), err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("install: cannot encode manifest: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("install: cannot write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("install: cannot move %s into place: %w", tmp, err)
	}
	return nil
}

// record stores an entry for a just-written file, computing the managed-region
// hash for mixed tiers.
func (m *Manifest) record(relPath string, tier core.OwnershipTier, content []byte) {
	e := ManifestEntry{SHA256: hashBytes(content), Tier: tier}
	if tier == core.OwnerMixed {
		if h, ok := managedHash(string(content), relPath); ok {
			e.ManagedSHA256 = h
		}
	}
	m.Files[relPath] = e
}

// Drift is one file's state relative to the manifest.
type Drift struct {
	Path   string             `json:"path"`
	Tier   core.OwnershipTier `json:"tier"`
	Status string             `json:"status"` // ok | modified | missing
}

// Verify compares every manifest-tracked file against its recorded hash and
// reports drift in stable path order. Drift on a [user] or [mixed] file is
// expected life; drift on a [tool] file means a hand edit that upgrade will
// overwrite.
func Verify(repoRoot string) ([]Drift, error) {
	m, err := LoadManifest(repoRoot)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("install: %s not found — run `blueprint init` (greenfield) or `blueprint adopt` (existing repo) first", ManifestPath(repoRoot))
	}
	paths := make([]string, 0, len(m.Files))
	for p := range m.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []Drift
	for _, p := range paths {
		e := m.Files[p]
		d := Drift{Path: p, Tier: e.Tier, Status: "ok"}
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(p)))
		switch {
		case os.IsNotExist(err):
			d.Status = "missing"
		case err != nil:
			return nil, fmt.Errorf("install: cannot read %s while verifying the manifest: %w", p, err)
		case hashBytes(b) != e.SHA256:
			d.Status = "modified"
		}
		out = append(out, d)
	}
	return out, nil
}

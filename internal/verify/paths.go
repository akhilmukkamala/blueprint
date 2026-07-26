package verify

import "path/filepath"

// Repo-relative layout (DESIGN §2). All joins go through filepath.Join so the
// package is Windows-clean; lock files store slash-separated relative paths so
// hashes are portable across OSes.
func changeDir(repoRoot, id string) string {
	return filepath.Join(repoRoot, ".blueprint", "changes", id)
}

func changePath(repoRoot, id string) string {
	return filepath.Join(changeDir(repoRoot, id), "change.md")
}

func specPath(repoRoot, area string) string {
	return filepath.Join(repoRoot, ".blueprint", "specs", area, "spec.md")
}

func verifiersPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "verifiers.toml")
}

func configPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "config.toml")
}

func lockPath(repoRoot, id string) string {
	return filepath.Join(changeDir(repoRoot, id), "approved.lock")
}

func verdictDir(repoRoot, id string) string {
	return filepath.Join(changeDir(repoRoot, id), "verdict")
}

func journalPath(repoRoot, id string) string {
	return filepath.Join(changeDir(repoRoot, id), "journal.ndjson")
}

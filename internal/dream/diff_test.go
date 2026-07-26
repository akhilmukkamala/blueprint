package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// applyPatch writes patch to a temp file and git-applies it in repo.
func applyPatch(t *testing.T, repo, patch string) {
	t.Helper()
	pf := filepath.Join(t.TempDir(), "p.patch")
	if err := os.WriteFile(pf, []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, repo, "apply", "--check", pf)
	gitDo(t, repo, "apply", pf)
}

func TestNewFileDiffGitApply(t *testing.T) {
	repo := gitRepo(t)
	patch := newFileDiff(".blueprint/steering/dream-x.md", "---\nid: x\n---\n\nbody\n")
	applyPatch(t, repo, patch)
	b, err := os.ReadFile(filepath.Join(repo, ".blueprint", "steering", "dream-x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "---\nid: x\n---\n\nbody\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestAppendDiffGitApplyTerminatedFile(t *testing.T) {
	repo := gitRepo(t)
	existing := "a = 1\nb = 2\nc = 3\nd = 4\n"
	writeRel(t, repo, "reg.toml", existing)
	gitDo(t, repo, "add", "reg.toml")
	gitDo(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "reg")

	patch := appendDiff("reg.toml", []byte(existing), "# added\ne = 5\n")
	applyPatch(t, repo, patch)
	b, _ := os.ReadFile(filepath.Join(repo, "reg.toml"))
	if string(b) != existing+"# added\ne = 5\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestAppendDiffGitApplyShortFile(t *testing.T) {
	repo := gitRepo(t)
	existing := "only\n"
	writeRel(t, repo, "one.txt", existing)
	gitDo(t, repo, "add", "one.txt")
	gitDo(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "one")

	applyPatch(t, repo, appendDiff("one.txt", []byte(existing), "two\n"))
	b, _ := os.ReadFile(filepath.Join(repo, "one.txt"))
	if string(b) != "only\ntwo\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestAppendDiffGitApplyNoTrailingNewline(t *testing.T) {
	repo := gitRepo(t)
	existing := "a\nb"
	writeRel(t, repo, "x.txt", existing)
	gitDo(t, repo, "add", "x.txt")
	gitDo(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x")

	applyPatch(t, repo, appendDiff("x.txt", []byte(existing), "c\n"))
	b, _ := os.ReadFile(filepath.Join(repo, "x.txt"))
	if string(b) != "a\nb\nc\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestAppendDiffMissingFileDegradesToNewFile(t *testing.T) {
	repo := gitRepo(t)
	applyPatch(t, repo, appendDiff("fresh.toml", nil, "x = 1\n"))
	b, _ := os.ReadFile(filepath.Join(repo, "fresh.toml"))
	if string(b) != "x = 1\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestChangedLinesCountsPayloadOnly(t *testing.T) {
	patch := newFileDiff("f.md", "1\n2\n3\n")
	if n := changedLines(patch); n != 3 {
		t.Fatalf("changedLines = %d, want 3 (headers excluded)", n)
	}
	if !strings.Contains(patch, "+++ b/f.md") {
		t.Fatal(patch)
	}
}

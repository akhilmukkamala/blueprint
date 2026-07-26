package spec

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"blueprint/internal/core"
)

func fixtureRepo(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "repo")
}

// tempRepo copies the fixture repo into a writable temp dir for tests that
// mutate state (Close, Save*).
func tempRepo(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := fixtureRepo(t)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying fixture repo: %v", err)
	}
	return dst
}

func TestLoadSpec(t *testing.T) {
	s, err := LoadSpec(fixtureRepo(t), "auth")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if s.Area != "auth" {
		t.Errorf("Area = %q, want auth", s.Area)
	}
	if s.Status != core.StatusApproved {
		t.Errorf("Status = %q, want approved", s.Status)
	}
	if len(s.Requirements) != 2 {
		t.Fatalf("got %d requirements, want 2", len(s.Requirements))
	}
	r := s.Requirements[0]
	if r.ID != "REQ-auth-001" || r.Pattern != core.PatternEventDriven {
		t.Errorf("req[0] = %s (%s), want REQ-auth-001 (event-driven)", r.ID, r.Pattern)
	}
	if !strings.HasPrefix(r.Text, "When a login request") {
		t.Errorf("req[0] text = %q", r.Text)
	}
	want := []core.VerifyMethod{{Kind: "test", Ref: "TestLoginRejectsInvalid"}}
	if !reflect.DeepEqual(r.Verify, want) {
		t.Errorf("req[0] verify = %+v, want %+v", r.Verify, want)
	}
	if got := len(s.Requirements[1].Verify); got != 2 {
		t.Errorf("req[1] has %d verify methods, want 2", got)
	}
}

func TestListSpecs(t *testing.T) {
	areas, err := ListSpecs(fixtureRepo(t))
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	if !reflect.DeepEqual(areas, []string{"auth"}) {
		t.Errorf("areas = %v, want [auth]", areas)
	}
}

func TestListSpecsMissingDir(t *testing.T) {
	areas, err := ListSpecs(t.TempDir())
	if err != nil || areas != nil {
		t.Errorf("ListSpecs on empty repo = (%v, %v), want (nil, nil)", areas, err)
	}
}

func TestSpecRoundTrip(t *testing.T) {
	repo := tempRepo(t)
	orig, err := loadFullSpec(repo, "auth")
	if err != nil {
		t.Fatalf("loadFullSpec: %v", err)
	}
	if err := saveFullSpec(repo, orig); err != nil {
		t.Fatalf("saveFullSpec: %v", err)
	}
	again, err := loadFullSpec(repo, "auth")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(orig, again) {
		t.Errorf("round trip drifted:\n orig: %+v\nagain: %+v", orig, again)
	}
}

func TestParseSpecErrors(t *testing.T) {
	cases := []struct {
		name, raw, wantSub string
	}{
		{"no frontmatter", "# no fences\n", "frontmatter fence"},
		{"unclosed frontmatter", "---\nid: x\n", "closing"},
		{"bad heading", "---\nid: x\n---\n\n### REQ-x-1x (ubiquitous)\ntext\n", "requirement heading"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSpec("test.md", "x", tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("parseSpec error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestAreaOf(t *testing.T) {
	cases := map[string]string{
		"REQ-auth-001":      "auth",
		"REQ-user-auth-007": "user-auth",
		"REQ--1":            "",
		"not-a-req":         "",
	}
	for id, want := range cases {
		if got := AreaOf(id); got != want {
			t.Errorf("AreaOf(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestSplitFrontmatterCRLF(t *testing.T) {
	front, body, err := splitFrontmatter("---\r\nid: x\r\n---\r\nbody\r\n", "---")
	if err != nil {
		t.Fatalf("splitFrontmatter CRLF: %v", err)
	}
	if !strings.Contains(front, "id: x") || !strings.Contains(body, "body") {
		t.Errorf("front=%q body=%q", front, body)
	}
}

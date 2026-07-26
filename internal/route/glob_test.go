package route

import "testing"

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/guide/intro.md", true},
		{"**/*.md", "docs/guide/intro.go", false},
		{"docs/**", "docs", true},
		{"docs/**", "docs/a/b/c.txt", true},
		{"docs/**", "src/docs.go", false},
		{"auth/**", "auth/login.go", true},
		{"auth/**", "pkg/auth/login.go", false},
		{"**/payments/**", "svc/payments/charge.go", true},
		{"**/payments/**", "payments/charge.go", true},
		{"**/payments/**", "svc/billing/charge.go", false},
		{"**", "anything/at/all", true},
		{"go.sum", "go.sum", true},
		{"go.sum", "sub/go.sum", false},
		{"**/*.sql", "db/migrations/001_init.sql", true},
		{"*.go", "a/b.go", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

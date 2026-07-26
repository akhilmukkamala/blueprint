// Glob matching for registry/safety path patterns. Patterns use forward
// slashes and support `**` (any number of path segments, including zero),
// `*`/`?` within a segment, and character classes via path.Match. Paths are
// normalized with filepath.ToSlash before matching so the same registry works
// on Windows (CONTRACTS rule 6).
package route

import (
	"path"
	"path/filepath"
	"strings"
)

// matchGlob reports whether p matches pattern. Both are slash-normalized
// first. A bare "**" matches everything; "a/**" matches "a" and anything
// under it.
func matchGlob(pattern, p string) bool {
	pat := strings.Split(strings.Trim(filepath.ToSlash(pattern), "/"), "/")
	segs := strings.Split(strings.Trim(filepath.ToSlash(p), "/"), "/")
	return matchSegs(pat, segs)
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// `**` may consume zero or more leading segments.
			for i := 0; i <= len(segs); i++ {
				if matchSegs(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// matchAny reports whether p matches any of patterns, returning the first
// pattern that matched.
func matchAny(patterns []string, p string) (string, bool) {
	for _, pat := range patterns {
		if matchGlob(pat, p) {
			return pat, true
		}
	}
	return "", false
}

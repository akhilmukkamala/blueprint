// scrub.go — the built-in secret scrubber (DESIGN §12: "secret-scrub pre-write").
// Every proposal body and patch passes through Scrub before it can reach disk;
// the branch writer refuses to bypass it. The rule table is deliberately small
// and high-precision: dream output quotes journal evidence and config excerpts,
// so a leaked credential here would land in a PR — redaction beats recall.
package dream

import "regexp"

// scrubRule is one redaction pattern. keepGroups>0 means the leading capture
// groups (key name + separator) are preserved and only the value is redacted,
// so a redacted line stays readable evidence.
type scrubRule struct {
	name       string
	re         *regexp.Regexp
	keepGroups int
}

var scrubRules = []scrubRule{
	// AWS access key IDs (long-term AKIA / temporary ASIA).
	{name: "aws-access-key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	// PEM private-key headers: the header alone is enough to redact — the body
	// that follows is useless without it and base64 blocks are too noisy to
	// pattern-match safely.
	{name: "private-key-pem", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	// Generic credential assignments: api_key/token/password/secret = "value".
	// The key and separator survive so evidence lines remain legible. The
	// replacement value starts with "[", which no value alternative can match,
	// keeping the rule idempotent over already-scrubbed text.
	{
		name:       "credential-assignment",
		re:         regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|access[_-]?key|secret|token|password|passwd)\b(\s*[:=]\s*)("[^"\n]{6,}"|'[^'\n]{6,}'|[A-Za-z0-9_\-+/=.]{8,})`),
		keepGroups: 2,
	},
}

// Scrub redacts every rule match in s and returns the scrubbed text plus the
// (deduplicated, table-ordered) names of the rules that fired. Idempotent:
// scrubbing scrubbed text changes nothing.
func Scrub(s string) (string, []string) {
	var fired []string
	for _, r := range scrubRules {
		if !r.re.MatchString(s) {
			continue
		}
		repl := "[REDACTED:" + r.name + "]"
		if r.keepGroups > 0 {
			repl = "${1}${2}" + repl
		}
		s = r.re.ReplaceAllString(s, repl)
		fired = append(fired, r.name)
	}
	return s, fired
}

// Package lint implements Blueprint's deterministic linters (DESIGN §3, §6):
// spec lint (EARS conformance, banned vague words, verify blocks, REQ ID
// hygiene, relative dates, sentence budget, task quality) and trace lint
// (bidirectional "verifies: REQ-..." coverage, DO-178C core). Pure static —
// no model, no network, no wall clock. Every finding carries a Remediation
// written for agent consumption (linters-that-teach).
package lint

import (
	"regexp"
	"strings"

	"blueprint/internal/core"
)

const (
	SevError   = "error"
	SevWarning = "warning"
)

// DefaultBannedWords seeds the vague-word ban from the INCOSE-R7 / NASA-ARM
// classics. Each entry is matched case-insensitively on word boundaries;
// multi-word phrases allowed.
var DefaultBannedWords = []string{
	"appropriate", "as appropriate", "as required", "if necessary", "if needed",
	"adequate", "quickly", "rapidly", "robust", "user-friendly", "user friendly",
	"efficient", "efficiently", "flexible", "seamless", "seamlessly",
	"minimize", "maximize", "optimize", "optimal", "easy", "easily", "simple",
	"sufficient", "sufficiently", "significant", "reasonable", "timely",
	"best possible", "state of the art", "state-of-the-art", "etc",
	"and/or", "but not limited to", "support", "handle", "process quickly",
	"normal", "typical", "generally", "usually", "ideally",
}

// DefaultRelativeDateWords is the relative-date ban (index and spec rule:
// absolute dates only).
var DefaultRelativeDateWords = []string{
	"yesterday", "today", "tomorrow", "recently", "soon",
	"currently", "last week", "next week", "last month", "next month",
}

// maxSentences is the requirement sentence budget (DESIGN §3).
const maxSentences = 3

// Config parameterizes the linters. Zero value = defaults.
type Config struct {
	BannedWords       []string // nil -> DefaultBannedWords; replaces the list
	ExtraBannedWords  []string // appended to the effective list
	RelativeDateWords []string // nil -> DefaultRelativeDateWords
	HumanVerifyBudget *int     // nil -> DefaultHumanVerifyBudget; max `verify: human` across living specs
}

func (c Config) bannedWords() []string {
	base := c.BannedWords
	if base == nil {
		base = DefaultBannedWords
	}
	return append(append([]string{}, base...), c.ExtraBannedWords...)
}

func (c Config) relativeDateWords() []string {
	if c.RelativeDateWords == nil {
		return DefaultRelativeDateWords
	}
	return c.RelativeDateWords
}

// All runs every linter (spec then trace) and concatenates the findings.
func All(repoRoot string, cfg Config) ([]core.LintFinding, error) {
	s, err := Spec(repoRoot, cfg)
	if err != nil {
		return nil, err
	}
	t, err := Trace(repoRoot, cfg)
	if err != nil {
		return nil, err
	}
	return append(s, t...), nil
}

// HasErrors reports whether any finding is error-severity (the exit-1
// condition for `blueprint lint`).
func HasErrors(findings []core.LintFinding) bool {
	for _, f := range findings {
		if f.Severity == SevError {
			return true
		}
	}
	return false
}

// wordRe compiles one banned word/phrase into a case-insensitive
// word-boundary matcher. Hyphens inside phrases are matched literally.
func wordRe(word string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`)
}

type wordMatcher struct {
	word string
	re   *regexp.Regexp
}

func compileWords(words []string) []wordMatcher {
	out := make([]wordMatcher, 0, len(words))
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, wordMatcher{word: w, re: wordRe(w)})
		}
	}
	return out
}

// sentenceCount approximates the number of sentences in text: terminal
// punctuation followed by whitespace or end-of-text.
var sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)

func sentenceCount(text string) int {
	return len(sentenceEnd.FindAllString(text, -1))
}

// earsConforms checks a requirement's text against its declared EARS pattern
// (the six patterns, ADR-0001). Deterministic shape checks only.
func earsConforms(p core.EARSPattern, text string) (ok bool, want string) {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	shall := strings.Contains(t, " shall ")
	switch p {
	case core.PatternUbiquitous:
		return strings.HasPrefix(t, "the ") && shall,
			`"The <system> shall <response>."`
	case core.PatternEventDriven:
		return strings.HasPrefix(t, "when ") && shall,
			`"When <trigger>, the <system> shall <response>."`
	case core.PatternStateDriven:
		return strings.HasPrefix(t, "while ") && shall,
			`"While <state>, the <system> shall <response>."`
	case core.PatternOptional:
		return strings.HasPrefix(t, "where ") && shall,
			`"Where <feature>, the <system> shall <response>."`
	case core.PatternUnwanted:
		return strings.HasPrefix(t, "if ") && strings.Contains(t, " then ") && shall,
			`"If <unwanted condition>, then the <system> shall <response>."`
	case core.PatternComplex:
		starts := false
		for _, kw := range []string{"when ", "while ", "where ", "if "} {
			if strings.HasPrefix(t, kw) {
				starts = true
				break
			}
		}
		n := 0
		for _, kw := range []string{"when", "while", "where", "if", "then"} {
			n += len(wordRe(kw).FindAllString(t, -1))
		}
		return starts && shall && n >= 2,
			`a combination of two or more triggers/states plus "the <system> shall <response>"`
	default:
		return false, ""
	}
}

var validVerifyKinds = map[string]bool{"test": true, "check": true, "bench": true, "human": true}

var validSpecStatus = map[string]bool{"draft": true, "approved": true, "verified": true, "archived": true}

// lineOf returns the 1-based line number of the first line containing needle,
// or 0 when absent.
func lineOf(content, needle string) int {
	for i, l := range strings.Split(content, "\n") {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	return 0
}

func finding(file string, line int, rule, sev, msg, remedy string) core.LintFinding {
	return core.LintFinding{File: file, Line: line, Rule: rule, Severity: sev, Message: msg, Remediation: remedy}
}

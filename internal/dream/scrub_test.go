package dream

import (
	"strings"
	"testing"
)

func TestScrubTable(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string // "" means unchanged
		rule     string // "" means no rule fires
		mustLose string // substring that must not survive
	}{
		{
			name:     "aws access key",
			in:       "leaked AKIAIOSFODNN7EXAMPLE in a journal line",
			rule:     "aws-access-key",
			mustLose: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:     "aws temporary key",
			in:       "ASIAIOSFODNN7EXAMPLE",
			rule:     "aws-access-key",
			mustLose: "ASIAIOSFODNN7EXAMPLE",
		},
		{
			name:     "pem private key header",
			in:       "-----BEGIN RSA PRIVATE KEY-----\nMIIEow…",
			rule:     "private-key-pem",
			mustLose: "BEGIN RSA PRIVATE KEY",
		},
		{
			name:     "quoted api key assignment keeps the key name",
			in:       `api_key = "sk-live-abcdef123456"`,
			rule:     "credential-assignment",
			mustLose: "sk-live-abcdef123456",
		},
		{
			name:     "yaml password",
			in:       "password: hunter2hunter2",
			rule:     "credential-assignment",
			mustLose: "hunter2hunter2",
		},
		{
			name:     "bare token assignment",
			in:       "TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			rule:     "credential-assignment",
			mustLose: "ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		},
		{
			name: "clean prose untouched",
			in:   "verify failure fingerprint fp-1 recurred 3 times across c1, c2",
		},
		{
			name: "short values are not credentials",
			in:   "token = abc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fired := Scrub(tc.in)
			if tc.rule == "" {
				if got != tc.in || len(fired) != 0 {
					t.Fatalf("clean input mutated: %q fired=%v", got, fired)
				}
				return
			}
			if len(fired) == 0 || fired[0] != tc.rule {
				t.Fatalf("fired = %v, want [%s]", fired, tc.rule)
			}
			if strings.Contains(got, tc.mustLose) {
				t.Fatalf("secret survived: %q", got)
			}
			if !strings.Contains(got, "[REDACTED:"+tc.rule+"]") {
				t.Fatalf("no redaction marker: %q", got)
			}
		})
	}
}

func TestScrubKeepsKeyNamesReadable(t *testing.T) {
	got, _ := Scrub(`api_key = "sk-live-abcdef123456"`)
	if !strings.HasPrefix(got, "api_key = ") {
		t.Fatalf("key name and separator must survive: %q", got)
	}
}

func TestScrubIsIdempotent(t *testing.T) {
	in := "password: hunter2hunter2 and AKIAIOSFODNN7EXAMPLE"
	once, _ := Scrub(in)
	twice, fired := Scrub(once)
	if once != twice {
		t.Fatalf("not idempotent:\nonce:  %q\ntwice: %q (fired %v)", once, twice, fired)
	}
}

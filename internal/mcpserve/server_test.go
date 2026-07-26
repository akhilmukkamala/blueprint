package mcpserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture: a tiny repo with an import edge, one living spec, one change with
// a verdict, and one trace-annotated test file — enough to exercise all five
// tools end to end over the stdio protocol.
var mcpFixture = map[string]string{
	"go.mod": "module example.com/mcpfix\n\ngo 1.22\n",
	"auth/login.go": `package auth

// Login checks credentials.
func Login(user, pass string) bool { return false }
`,
	"cmd/main.go": `package main

import "example.com/mcpfix/auth"

func main() { auth.Login("a", "b") }
`,
	"tests/login_test.go": `package tests

// verifies: REQ-auth-001
func TestLoginRejectsInvalid(t *testing.T) {}
`,
	".blueprint/specs/auth/spec.md": `---
id: auth
status: approved
owner: alice
reviewed: 2026-07-01
---

# auth

### REQ-auth-001 (event-driven)

When a login request carries invalid credentials, the system shall respond 401 without setting a session cookie.

verify:
- test: TestLoginRejectsInvalid
`,
	".blueprint/changes/2026-07-20-login-hardening/change.md": `+++
id = "2026-07-20-login-hardening"
title = "Harden login"
type = "fix"
tier = "light"
status = "verified"
scenario = "bugfix"

[loop]
predicate = "blueprint verify 2026-07-20-login-hardening"
max_iterations = 8
max_minutes = 45
max_usd = 5.0

[loop.breaker]
repeat_action_n = 3
repeat_error_n = 3
no_diff_delta_n = 3
oscillation_n = 2
monologue_tokens = 4000

[loop.boundaries]
writable = ["auth/**", "tests/**"]
readonly = [".blueprint/specs/**"]
+++

# Harden login

## Delta

### MODIFIED REQ-auth-001 (event-driven)

When a login request carries invalid credentials, the system shall respond 401 without setting a session cookie.

verify:
- test: TestLoginRejectsInvalid

## Tasks

- [x] T1: Add the rejection test in tests/login_test.go
`,
	".blueprint/changes/2026-07-20-login-hardening/verdict/verdict.json": `{
  "change_id": "2026-07-20-login-hardening",
  "time": "2026-07-20T12:00:00Z",
  "pass": true,
  "tamper": false,
  "checks": [
    {"name": "test:TestLoginRejectsInvalid", "req_id": "REQ-auth-001", "pass": true, "exit_code": 0}
  ],
  "fingerprint": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
}
`,
}

func writeMCPFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range mcpFixture {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runSession feeds newline-delimited requests through Serve and returns the
// decoded responses in order.
func runSession(t *testing.T, root string, requests ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := New(root).Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not JSON: %q: %v", line, err)
		}
		responses = append(responses, m)
	}
	return responses
}

// toolPayload unpacks a tools/call response's JSON text content.
func toolPayload(t *testing.T, resp map[string]any) (map[string]any, bool) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	isError, _ := result["isError"].(bool)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		// isError content is plain text, not JSON.
		return map[string]any{"_text": text}, isError
	}
	return payload, isError
}

func call(id int, tool, argsJSON string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, argsJSON)
}

func TestHandshakeAndToolsList(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses (notification is silent), got %d: %v", len(resps), resps)
	}
	init := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	if name := init["serverInfo"].(map[string]any)["name"]; name != "blueprint" {
		t.Errorf("serverInfo.name = %v", name)
	}
	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	want := "repo_map,search,symbols,blast_radius,spec_lookup"
	if strings.Join(names, ",") != want {
		t.Errorf("tools = %v, want %s", names, want)
	}
}

func TestUnknownMethodAndParseError(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`this is not json`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	if code := resps[0]["error"].(map[string]any)["code"].(float64); code != codeMethodNotFound {
		t.Errorf("unknown method code = %v, want %d", code, codeMethodNotFound)
	}
	if code := resps[1]["error"].(map[string]any)["code"].(float64); code != codeParseError {
		t.Errorf("parse error code = %v, want %d", code, codeParseError)
	}
}

func TestRepoMapTool(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root, call(1, "repo_map", `{"budget": 500}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("repo_map errored: %v", payload)
	}
	rendered, _ := payload["map"].(string)
	if !strings.Contains(rendered, "auth/login.go: Login") {
		t.Errorf("map should list auth/login.go with its symbol, got:\n%s", rendered)
	}
	if payload["budget"].(float64) != 500 {
		t.Errorf("budget echoed = %v", payload["budget"])
	}
}

func TestSearchLexicalAndSemanticDegraded(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root,
		call(1, "search", `{"query": "credentials"}`),
		call(2, "search", `{"query": "credentials", "mode": "semantic"}`),
	)
	lex, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("lexical search errored: %v", lex)
	}
	if lex["degraded"] != nil {
		t.Error("lexical search must not be degraded")
	}
	matches := lex["matches"].([]any)
	if len(matches) == 0 {
		t.Fatal("lexical search found nothing for 'credentials'")
	}
	sem, isError := toolPayload(t, resps[1])
	if isError {
		t.Fatalf("semantic search must degrade, not error: %v", sem)
	}
	if sem["degraded"] != true {
		t.Error("semantic search must set degraded: true")
	}
	if reason, _ := sem["reason"].(string); !strings.Contains(reason, "Tier-2") {
		t.Errorf("degraded reason must name Tier-2, got %q", reason)
	}
}

func TestSymbolsTool(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root,
		call(1, "symbols", `{"name_or_file": "auth/login.go"}`),
		call(2, "symbols", `{"name_or_file": "Login"}`),
	)
	byFile, _ := toolPayload(t, resps[0])
	if byFile["file"] != "auth/login.go" {
		t.Errorf("file lookup returned %v", byFile["file"])
	}
	byName, _ := toolPayload(t, resps[1])
	hits := byName["matches"].([]any)
	if len(hits) == 0 || hits[0].(map[string]any)["name"] != "Login" {
		t.Errorf("symbol lookup for Login got %v", hits)
	}
}

func TestBlastRadiusTool(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root, call(1, "blast_radius", `{"files": ["auth/login.go"]}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("blast_radius errored: %v", payload)
	}
	if payload["radius"].(float64) != 2 {
		t.Errorf("radius = %v, want 2 (login.go + cmd/main.go)", payload["radius"])
	}
}

func TestSpecLookupTool(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root, call(1, "spec_lookup", `{"req_id": "REQ-auth-001"}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("spec_lookup errored: %v", payload)
	}
	if payload["found"] != true {
		t.Fatalf("REQ-auth-001 should be found, got %v", payload)
	}
	if text, _ := payload["text"].(string); !strings.Contains(text, "invalid credentials") {
		t.Errorf("spec text missing, got %q", text)
	}
	tests := payload["tests"].([]any)
	if len(tests) != 1 || tests[0] != "tests/login_test.go" {
		t.Errorf("traced tests = %v, want [tests/login_test.go]", tests)
	}
	latest, ok := payload["latest_verdict"].(map[string]any)
	if !ok {
		t.Fatalf("latest_verdict missing: %v", payload)
	}
	if latest["change_id"] != "2026-07-20-login-hardening" || latest["pass"] != true {
		t.Errorf("latest_verdict = %v", latest)
	}
}

func TestSpecLookupDegradedNotError(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root, call(1, "spec_lookup", `{"req_id": "REQ-auth-999"}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatal("missing requirement must degrade, not error")
	}
	if payload["found"] != false || payload["degraded"] != true {
		t.Errorf("want found:false degraded:true, got %v", payload)
	}
	if rem, _ := payload["remediation"].(string); rem == "" {
		t.Error("degraded result must carry a remediation")
	}
}

func TestToolBadArgsIsErrorResult(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root,
		call(1, "spec_lookup", `{}`),
		call(2, "nonexistent_tool", `{}`),
	)
	for i, r := range resps {
		if r["error"] != nil {
			t.Errorf("resp %d: bad tool input must be an isError result, not a protocol error: %v", i, r)
		}
		if _, isError := toolPayload(t, r); !isError {
			t.Errorf("resp %d: want isError true", i)
		}
	}
}

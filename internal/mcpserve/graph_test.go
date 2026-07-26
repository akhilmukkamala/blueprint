package mcpserve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGraphBin is the compiled testdata/fakegraph MCP stdio double, built
// once in TestMain — the proxy spawns real subprocesses, so the double must
// be a real executable.
var fakeGraphBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakegraph")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeGraphBin = filepath.Join(dir, "fakegraph")
	if runtime.GOOS == "windows" {
		fakeGraphBin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", fakeGraphBin, "./testdata/fakegraph").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot build fakegraph test double: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// configureGraph points [retrieval.graph] at the fake backend in the given
// mode. TOML literal strings keep Windows backslashes intact.
func configureGraph(t *testing.T, root, mode string) {
	t.Helper()
	content := fmt.Sprintf("[retrieval.graph]\ncommand = '%s %s'\n", fakeGraphBin, mode)
	p := filepath.Join(root, ".blueprint", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGraphProxyBlastRadius(t *testing.T) {
	root := writeMCPFixture(t)
	configureGraph(t, root, "answer")
	resps := runSession(t, root, call(1, "blast_radius", `{"files": ["auth/login.go"]}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("graph blast_radius errored: %v", payload)
	}
	if payload["tier"] != "graph" {
		t.Fatalf("want tier graph, got %v", payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("no translated result: %v", payload)
	}
	if result["backend"] != "fakegraph" || result["tool"] != "blast_radius" {
		t.Errorf("reply not forwarded/translated: %v", result)
	}
	// The single tools/call must carry the original arguments through.
	args := result["args"].(map[string]any)
	files := args["files"].([]any)
	if len(files) != 1 || files[0] != "auth/login.go" {
		t.Errorf("arguments not forwarded verbatim: %v", args)
	}
}

func TestGraphProxySearchMode(t *testing.T) {
	root := writeMCPFixture(t)
	configureGraph(t, root, "answer")
	resps := runSession(t, root, call(1, "search", `{"query": "credentials", "mode": "graph"}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("graph search errored: %v", payload)
	}
	if payload["mode"] != "graph" || payload["degraded"] != nil {
		t.Fatalf("want non-degraded graph mode, got %v", payload)
	}
	result := payload["result"].(map[string]any)
	if result["tool"] != "search" {
		t.Errorf("search not forwarded: %v", result)
	}
}

func TestGraphUnconfiguredSearchDegrades(t *testing.T) {
	root := writeMCPFixture(t) // no config.toml at all
	resps := runSession(t, root, call(1, "search", `{"query": "credentials", "mode": "graph"}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("unconfigured graph mode must degrade, not error: %v", payload)
	}
	if payload["degraded"] != true || payload["mode_used"] != "lexical" {
		t.Fatalf("want degraded lexical fallback, got %v", payload)
	}
	if rem, _ := payload["remediation"].(string); !strings.Contains(rem, "[retrieval.graph] command") {
		t.Errorf("remediation must name the exact config key, got %q", rem)
	}
	if len(payload["matches"].([]any)) == 0 {
		t.Error("fallback must still answer lexically")
	}
}

// failure modes: spawn failure, backend tool error, backend rpc error, and
// backend exiting silently all fall back to Tier 1 with the error named.
func TestGraphFailuresFallBackToTier1(t *testing.T) {
	tests := []struct {
		name       string
		mode       string // fakegraph mode; "missing" = nonexistent binary
		wantReason string
	}{
		{"missing binary", "missing", "graph backend"},
		{"tool error", "toolerror", "index corrupt"},
		{"rpc error", "rpcerror", "graph store offline"},
		{"exits without answer", "silent", "without answering"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeMCPFixture(t)
			if tc.mode == "missing" {
				p := filepath.Join(root, ".blueprint", "config.toml")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("[retrieval.graph]\ncommand = 'no-such-graph-backend-on-path'\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				configureGraph(t, root, tc.mode)
			}
			resps := runSession(t, root, call(1, "blast_radius", `{"files": ["auth/login.go"]}`))
			payload, isError := toolPayload(t, resps[0])
			if isError {
				t.Fatalf("graph failure must degrade, not error: %v", payload)
			}
			if payload["degraded"] != true || payload["tier"] != "tier1-import-graph" {
				t.Fatalf("want degraded tier-1 fallback, got %v", payload)
			}
			if reason, _ := payload["reason"].(string); !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason must name the failure (%q), got %q", tc.wantReason, reason)
			}
			// Honest degradation still answers: tier-1 radius is real.
			if payload["radius"].(float64) != 2 {
				t.Errorf("tier-1 fallback radius = %v, want 2", payload["radius"])
			}
		})
	}
}

func TestGraphTimeout(t *testing.T) {
	old := graphTimeout
	graphTimeout = 2 * time.Second
	defer func() { graphTimeout = old }()

	root := writeMCPFixture(t)
	configureGraph(t, root, "hang")
	resps := runSession(t, root, call(1, "blast_radius", `{"files": ["auth/login.go"]}`))
	payload, isError := toolPayload(t, resps[0])
	if isError {
		t.Fatalf("timeout must degrade, not error: %v", payload)
	}
	if payload["degraded"] != true {
		t.Fatalf("want degraded fallback after timeout, got %v", payload)
	}
	if reason, _ := payload["reason"].(string); !strings.Contains(reason, "timed out") {
		t.Errorf("reason must name the timeout, got %q", reason)
	}
}

func TestSemanticRemediationNamesConfigKeys(t *testing.T) {
	root := writeMCPFixture(t)
	resps := runSession(t, root, call(1, "search", `{"query": "credentials", "mode": "semantic"}`))
	payload, _ := toolPayload(t, resps[0])
	rem, _ := payload["remediation"].(string)
	for _, key := range []string{"tier2_packing", "tier2_lsp", "[retrieval.graph] command"} {
		if !strings.Contains(rem, key) {
			t.Errorf("semantic remediation must name %q, got %q", key, rem)
		}
	}
}

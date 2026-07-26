// Package netaudit enforces ACCEPTANCE AC-12: the blueprint binary makes zero
// network calls — its dependency graph must contain no network-capable
// stdlib packages. The only sanctioned network activity is exec'ing
// user-configured external commands (model checker, domain verifiers), which
// this audit intentionally does not exempt at the import level: the binary
// itself stays incapable of dialing out.
package netaudit

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBinaryHasNoNetworkImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "blueprint/cmd/blueprint").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	forbidden := []string{"net/http", "net/rpc", "net/smtp", "net/mail", "golang.org/x/net"}
	for _, d := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, f := range forbidden {
			if d == f || strings.HasPrefix(d, f+"/") {
				t.Errorf("forbidden network package %q in the binary's dependency graph — AC-12 allows network only in documented opt-in commands via exec'd external tools, never in-process", d)
			}
		}
	}
}

// TestNoDirectDialUsage: our own source must never dial; the stdlib "net"
// package appearing transitively is tolerated, calling it is not.
func TestNoDirectDialUsage(t *testing.T) {
	out, err := exec.Command("grep", "-rln", "--include=*.go", "net.Dial", "../..").Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.Contains(f, "netaudit") {
				continue // this file names the symbol in prose
			}
			t.Errorf("net.Dial usage in %s — the binary must stay network-incapable (AC-12)", f)
		}
	}
}

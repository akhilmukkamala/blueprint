package dream

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a real test: it is the subprocess body for [dream]
// hook tests (classic os/exec helper pattern, same as internal/verify).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "dream-items":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(1)
		}
		if out := os.Getenv("HELPER_OUT"); out != "" {
			if err := os.WriteFile(out, b, 0o644); err != nil {
				os.Exit(1)
			}
		}
		fmt.Print(os.Getenv("HELPER_ITEMS"))
	case "fail":
		fmt.Fprintln(os.Stderr, "model exploded")
		os.Exit(3)
	}
	os.Exit(0)
}

func dreamHookConfig(t *testing.T, root, mode string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("HELPER_MODE", mode)
	writeRel(t, root, ".blueprint/config.toml", fmt.Sprintf(
		"[dream]\ncommand = \"'%s' -test.run=TestHelperProcess\"\nmax_usd = 1.5\n", exe))
}

func TestModelConsolidationPacketAndValidation(t *testing.T) {
	root := t.TempDir()
	overrideClusterFixture(t, root)
	writeRel(t, root, ".blueprint/registry.toml", "[[class]]\nname = \"docs-only\"\n")
	writeRel(t, root, ".blueprint/steering/style.md", "---\nid: style\n---\n\nrule body\n")

	packetOut := filepath.Join(t.TempDir(), "packet.json")
	dreamHookConfig(t, root, "dream-items")
	t.Setenv("HELPER_OUT", packetOut)
	t.Setenv("HELPER_ITEMS", `[
	  {"title":"Model-consolidated router fix","body":"per S-1","signal_ids":["S-1"]},
	  {"title":"Cites nothing real","body":"bogus","signal_ids":["S-999"]}
	]`)

	res, err := Run(root, Options{Branch: false, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ModelUsed {
		t.Fatalf("model hook was configured but unused: %+v", res.Warnings)
	}
	if len(res.Items) != 1 || res.Items[0].Title != "Model-consolidated router fix" {
		t.Fatalf("items = %+v", res.Items)
	}
	if res.Items[0].ID != "D-2026-07-26-1" {
		t.Fatalf("model items still get stable IDs, got %q", res.Items[0].ID)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "S-999") {
			found = true
		}
	}
	if !found {
		t.Fatalf("uncited item must be dropped with a warning: %v", res.Warnings)
	}

	// The stdin packet carries signals + excerpts + rubric + max_usd.
	b, err := os.ReadFile(packetOut)
	if err != nil {
		t.Fatalf("[dream] command never received the stdin packet: %v", err)
	}
	var packet modelPacket
	if err := json.Unmarshal(b, &packet); err != nil {
		t.Fatal(err)
	}
	if len(packet.Signals) == 0 || packet.Signals[0].ID != "S-1" {
		t.Fatalf("packet signals = %+v", packet.Signals)
	}
	if packet.MaxUSD != 1.5 {
		t.Fatalf("max_usd = %v", packet.MaxUSD)
	}
	if !strings.Contains(packet.Rubric, "at most 5") || !strings.Contains(packet.Rubric, "40 lines") {
		t.Fatalf("rubric = %q", packet.Rubric)
	}
	if !strings.Contains(packet.Registry, "docs-only") {
		t.Fatalf("registry excerpt missing: %q", packet.Registry)
	}
	if !strings.Contains(packet.Steering["style.md"], "rule body") {
		t.Fatalf("steering excerpt missing: %+v", packet.Steering)
	}
}

func TestModelFailureFallsBackToDeterministic(t *testing.T) {
	root := t.TempDir()
	overrideClusterFixture(t, root)
	dreamHookConfig(t, root, "fail")

	res, err := Run(root, Options{Branch: false, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.ModelUsed {
		t.Fatal("failed hook must not count as model-used")
	}
	if len(res.Items) == 0 {
		t.Fatal("deterministic fallback produced no items")
	}
	warned := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "falling back to deterministic") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestModelItemPatchOverCapIsWithheld(t *testing.T) {
	root := t.TempDir()
	overrideClusterFixture(t, root)
	dreamHookConfig(t, root, "dream-items")
	big := newFileDiff("huge.md", strings.Repeat("x\n", 41))
	items, _ := json.Marshal([]modelItem{{Title: "Huge", Body: "b", SignalIDs: []string{"S-1"}, Patch: big}})
	t.Setenv("HELPER_ITEMS", string(items))

	res, err := Run(root, Options{Branch: false, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items[0].Patch != "" || !strings.Contains(res.Items[0].Note, "cap of 40") {
		t.Fatalf("model patch over cap must be withheld: %+v", res.Items[0])
	}
}

func TestSecretsScrubbedBeforeWrite(t *testing.T) {
	root := t.TempDir()
	overrideClusterFixture(t, root)
	dreamHookConfig(t, root, "dream-items")
	items, _ := json.Marshal([]modelItem{{
		Title: "Leaky", Body: "found api_key = \"sk-live-abcdef123456\" in config", SignalIDs: []string{"S-1"},
	}})
	t.Setenv("HELPER_ITEMS", string(items))

	res, err := Run(root, Options{Branch: false, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Proposal, "sk-live-abcdef123456") {
		t.Fatal("secret reached the rendered proposal")
	}
	if !strings.Contains(res.Proposal, "[REDACTED:credential-assignment]") {
		t.Fatal("scrub marker missing from proposal")
	}
	has := false
	for _, r := range res.Scrubbed {
		if r == "credential-assignment" {
			has = true
		}
	}
	if !has {
		t.Fatalf("scrubbed rules = %v", res.Scrubbed)
	}
}

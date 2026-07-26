package cli

// Tests for the metrics feature's command registration and flag contract
// (this file's sibling metricscmds.go only — never another feature's
// commands).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetricsCommandsRegistered(t *testing.T) {
	found := map[string]bool{}
	for _, c := range All() {
		found[c.Name()] = true
	}
	for _, name := range []string{"metrics", "bench"} {
		if !found[name] {
			t.Errorf("command %q not registered", name)
		}
	}
}

func TestBenchHasRetrievalSubcommand(t *testing.T) {
	b := newBenchCmd()
	sub, _, err := b.Find([]string{"retrieval"})
	if err != nil || sub.Name() != "retrieval" {
		t.Fatalf("bench retrieval not found: %v", err)
	}
	for _, flag := range []string{"tasks", "init", "generate", "instructions", "tier", "record-task", "record-tier", "tokens", "tool-calls", "seconds", "success", "notes"} {
		if sub.Flags().Lookup(flag) == nil {
			t.Errorf("bench retrieval must expose --%s", flag)
		}
	}
}

func TestMetricsRejectsUnknownFormat(t *testing.T) {
	c := newMetricsCmd()
	c.SetArgs([]string{"--format", "yaml"})
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "prom") {
		t.Fatalf("unknown format should fail naming the supported one, got %v", err)
	}
}

// chdirRepo creates a bare .blueprint repo and moves the test process there so
// the command's repo-root walk finds it.
func chdirRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".blueprint"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	// Resolve symlinks (macOS /var -> /private/var) so paths compare cleanly.
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestMetricsCommandWritesReport(t *testing.T) {
	root := chdirRepo(t)
	c := newMetricsCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "reports", "metrics.json"))
	if err != nil {
		t.Fatalf("metrics.json not written: %v", err)
	}
	var rep struct {
		Metrics map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Metrics) == 0 {
		t.Fatal("report has no metrics")
	}
	if !strings.Contains(out.String(), "metrics generated") {
		t.Errorf("human output missing header:\n%s", out.String())
	}
}

func TestMetricsPromFormat(t *testing.T) {
	chdirRepo(t)
	c := newMetricsCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--format", "prom"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "blueprint_") {
		t.Errorf("prom output missing series/comments:\n%s", out.String())
	}
}

func TestBenchRetrievalInitThenRun(t *testing.T) {
	root := chdirRepo(t)
	run := func(args ...string) (*bytes.Buffer, error) {
		c := newBenchRetrievalCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(args)
		return &out, c.Execute()
	}

	// Without a suite: remediation pointing at --init.
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("missing suite should point at --init, got %v", err)
	}

	if _, err := run("--init"); err != nil {
		t.Fatal(err)
	}
	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pending") {
		t.Errorf("fresh run should list pending runs:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "reports", "bench", "report.json")); err != nil {
		t.Fatalf("report.json not written: %v", err)
	}

	// Recording without the joint columns must fail loudly.
	if _, err := run("--record-task", "T1", "--record-tier", "tier0-grep", "--tokens", "100"); err == nil {
		t.Fatal("recording tokens without success/tool-calls/seconds must fail (joint reporting)")
	}
	if _, err := run("--record-task", "T1", "--record-tier", "tier0-grep",
		"--tokens", "100", "--tool-calls", "3", "--seconds", "60", "--success=true"); err != nil {
		t.Fatal(err)
	}
	out, err = run()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tier0-grep") || !strings.Contains(out.String(), "success 100%") {
		t.Errorf("summary should show tier0 with its success rate:\n%s", out.String())
	}

	// Unknown task/tier get remediation errors.
	if _, err := run("--record-task", "nope", "--record-tier", "tier0-grep",
		"--tokens", "1", "--tool-calls", "1", "--seconds", "1", "--success=false"); err == nil {
		t.Fatal("unknown task must be rejected")
	}
	if _, err := run("--record-task", "T1", "--record-tier", "tier9",
		"--tokens", "1", "--tool-calls", "1", "--seconds", "1", "--success=false"); err == nil {
		t.Fatal("unknown tier must be rejected")
	}
}

func TestBenchRetrievalInstructionsFlag(t *testing.T) {
	chdirRepo(t)
	run := func(args ...string) (*bytes.Buffer, error) {
		c := newBenchRetrievalCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(args)
		return &out, c.Execute()
	}
	if _, err := run("--init"); err != nil {
		t.Fatal(err)
	}

	// --instructions needs --tier.
	if _, err := run("--instructions", "T1"); err == nil || !strings.Contains(err.Error(), "--tier") {
		t.Fatalf("--instructions without --tier must fail naming the flag, got %v", err)
	}
	// Unknown task and unconfigured tier get remediation errors.
	if _, err := run("--instructions", "nope", "--tier", "tier0-grep"); err == nil {
		t.Fatal("unknown task must be rejected")
	}
	if _, err := run("--instructions", "T1", "--tier", "tier2-packing"); err == nil || !strings.Contains(err.Error(), "[retrieval]") {
		t.Fatalf("unconfigured tier must point at the [retrieval] config, got %v", err)
	}

	out, err := run("--instructions", "T1", "--tier", "tier1-map")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"blueprint map", "--record-task T1", "--record-tier tier1-map", "git checkout"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("instructions output missing %q:\n%s", want, out.String())
		}
	}
}

func TestBenchRetrievalConfiguredTierJoinsGrid(t *testing.T) {
	root := chdirRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".blueprint", "config.toml"),
		[]byte("[retrieval]\ntier2_packing = \"repomix\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (*bytes.Buffer, error) {
		c := newBenchRetrievalCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(args)
		return &out, c.Execute()
	}
	if _, err := run("--init"); err != nil {
		t.Fatal(err)
	}
	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tier2-packing") {
		t.Errorf("configured tier2-packing must appear in the pending grid:\n%s", out.String())
	}
	// Recording against the configured tier is accepted.
	if _, err := run("--record-task", "T1", "--record-tier", "tier2-packing",
		"--tokens", "1", "--tool-calls", "1", "--seconds", "1", "--success=true"); err != nil {
		t.Fatal(err)
	}
	// report.json carries the per-class keys.
	raw, err := os.ReadFile(filepath.Join(root, "reports", "bench", "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"class_summaries", "class_margins", "query_class"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("report.json missing %q", key)
		}
	}
}

func TestBenchRetrievalGenerateWithoutHistoryIsRemediation(t *testing.T) {
	chdirRepo(t)
	c := newBenchRetrievalCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--generate"})
	// No archive and no git repo: generation must fail with the manual path.
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("empty-history generate must point at --init, got %v", err)
	}
}

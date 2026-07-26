package cli

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func versionRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "blueprint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(versionCommand())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestVersionJSON(t *testing.T) {
	old := Version
	Version = "9.9.9-test"
	defer func() { Version = old }()

	out, err := versionRun(t, "version", "--json")
	if err != nil {
		t.Fatalf("version --json: %v\n%s", err, out)
	}
	var got struct {
		Version string `json:"version"`
		Go      string `json:"go"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got.Version != "9.9.9-test" {
		t.Errorf("version = %q, want 9.9.9-test", got.Version)
	}
	if got.Go != runtime.Version() || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
		t.Errorf("platform = %s/%s/%s, want %s/%s/%s",
			got.Go, got.OS, got.Arch, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	}
}

func TestVersionHuman(t *testing.T) {
	old := Version
	Version = "9.9.9-test"
	defer func() { Version = old }()

	out, err := versionRun(t, "version")
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	if !strings.Contains(out, "blueprint 9.9.9-test") ||
		!strings.Contains(out, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("human output wrong: %q", out)
	}
}

func TestVersionRejectsArgs(t *testing.T) {
	if _, err := versionRun(t, "version", "extra"); err == nil {
		t.Error("version accepted a positional arg; want error")
	}
}

package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
)

// The full-tier fresh-context model checker (DESIGN §7) is a HOOK, not an API
// client: blueprint execs an external command from .blueprint/config.toml and
// feeds it a JSON packet on stdin. The checker-context rule is load-bearing:
// the packet always carries the FULL approved spec text + the diff + the
// rubric — never diff-only (superpowers' documented double failure: diff-only
// packages let reviewers silently redefine spec requirements, 0/5 caught it),
// and never the maker transcript.

const defaultMaxUSD = 2.0

// defaultRubric includes the explicit verifier-weakening hunt (DESIGN §7).
const defaultRubric = `Grade the diff against the approved spec. For each requirement in the spec, judge whether the diff satisfies, violates, or does not touch it. Explicitly hunt for verifier-weakening edits: monkey-patched or deleted assertions, widened thresholds, skipped or narrowed tests, loosened verifier configs, and error-swallowing that hides failures. Exit 0 only if the diff satisfies every touched requirement and contains no verifier-weakening edit; otherwise exit nonzero and report each violation with the requirement ID.`

type checkerConfig struct {
	Command string  `toml:"command"`
	MaxUSD  float64 `toml:"max_usd"`
}

type configFile struct {
	Checker checkerConfig `toml:"checker"`
	Test    testConfig    `toml:"test"` // [test] command — see verifiers.go resolveTestCmd
}

func loadCheckerConfig(repoRoot string) (checkerConfig, error) {
	cfg := checkerConfig{MaxUSD: defaultMaxUSD}
	b, err := os.ReadFile(configPath(repoRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	var f configFile
	if err := toml.Unmarshal(b, &f); err != nil {
		return cfg, fmt.Errorf(".blueprint/config.toml is not valid TOML (%v); the checker hook is a [checker] table with command and optional max_usd", err)
	}
	cfg.Command = f.Checker.Command
	if f.Checker.MaxUSD > 0 {
		cfg.MaxUSD = f.Checker.MaxUSD
	}
	return cfg, nil
}

// checkerPacket is the stdin contract for external checker commands.
type checkerPacket struct {
	ChangeID string            `json:"change_id"`
	ChangeMD string            `json:"change_md"` // full approved change text
	Specs    map[string]string `json:"specs"`     // area -> full living-spec text
	Diff     string            `json:"diff"`
	Rubric   string            `json:"rubric"`
	MaxUSD   float64           `json:"max_usd"` // cost cap the checker must honor
}

// changeDiff prefers the merge-base diff against main (the change branch's
// whole delta); falls back to uncommitted work against HEAD.
func changeDiff(repoRoot string) string {
	for _, args := range [][]string{
		{"diff", "main...HEAD"},
		{"diff", "HEAD"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		out, err := cmd.Output()
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return string(out)
		}
	}
	return ""
}

// runChecker executes the configured model checker for a full-tier change.
// No checker configured -> a skipped-but-passing CheckResult with the reason,
// so light/exempt-style deterministic-only repos stay green (DESIGN §7:
// substance where risk is highest, friction where it isn't).
func runChecker(repoRoot string, c *core.Change) core.CheckResult {
	res := core.CheckResult{Name: "model-checker"}

	cfg, err := loadCheckerConfig(repoRoot)
	if err != nil {
		res.ExitCode = -1
		res.Detail = err.Error()
		return res
	}
	if cfg.Command == "" {
		res.Pass = true
		res.Detail = "skipped: no [checker] command configured in .blueprint/config.toml; full-tier changes get deterministic checks only until one is set"
		return res
	}

	packet := checkerPacket{
		ChangeID: c.ID,
		Rubric:   defaultRubric,
		MaxUSD:   cfg.MaxUSD,
		Specs:    map[string]string{},
		Diff:     changeDiff(repoRoot),
	}
	if b, err := os.ReadFile(changePath(repoRoot, c.ID)); err == nil {
		packet.ChangeMD = string(b)
	}
	seen := map[string]bool{}
	for _, d := range c.Delta {
		if seen[d.Area] {
			continue
		}
		seen[d.Area] = true
		if b, err := os.ReadFile(specPath(repoRoot, d.Area)); err == nil {
			packet.Specs[d.Area] = string(b)
		}
	}

	in, err := json.Marshal(packet)
	if err != nil {
		res.ExitCode = -1
		res.Detail = err.Error()
		return res
	}

	args := splitCommand(cfg.Command)
	if len(args) == 0 {
		res.ExitCode = -1
		res.Detail = "[checker] command in .blueprint/config.toml is empty; set it to an executable that reads the JSON packet on stdin"
		return res
	}

	if err := os.MkdirAll(verdictDir(repoRoot, c.ID), 0o755); err != nil {
		res.ExitCode = -1
		res.Detail = err.Error()
		return res
	}
	reportPath := filepath.Join(verdictDir(repoRoot, c.ID), "checker.report.json")

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CHANGE="+c.ID)
	cmd.Stdin = bytes.NewReader(in)
	out, runErr := cmd.CombinedOutput()

	if werr := os.WriteFile(reportPath, out, 0o644); werr == nil {
		if rel, rerr := filepath.Rel(repoRoot, reportPath); rerr == nil {
			res.ReportPath = filepath.ToSlash(rel)
		}
	}

	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			res.Detail = fmt.Sprintf("model checker exited %d; its report is at %s — address each cited requirement violation, or fix the checker command in .blueprint/config.toml", res.ExitCode, res.ReportPath)
		} else {
			res.ExitCode = -1
			res.Detail = fmt.Sprintf("model checker could not run (%v); check that %q is installed and on PATH", runErr, args[0])
		}
		return res
	}
	res.Pass = true
	if s := strings.TrimSpace(string(out)); s == "" {
		res.Detail = "model checker passed (empty report)"
	}
	return res
}

package verify

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
	"blueprint/internal/testcmd"
)

// testCmdToken is the placeholder the bundled verifier pack uses for the
// repo test command (DESIGN §5): test commands vary per repo, so the pack
// ships {{TEST_CMD}} and verify expands it at run time — from [test] command
// in .blueprint/config.toml when set, else testcmd.Detect.
const testCmdToken = "{{TEST_CMD}}"

// testConfig is the [test] table of .blueprint/config.toml.
type testConfig struct {
	Command string `toml:"command"`
}

// resolveTestCmd returns the repo test command and where it came from.
// Resolution order: explicit [test] command in config.toml, then build-file
// auto-detection. ok=false means neither resolved — callers must surface a
// remediation failure, never a false pass.
func resolveTestCmd(repoRoot string) (cmd string, source string, ok bool) {
	if b, err := os.ReadFile(configPath(repoRoot)); err == nil {
		var f configFile
		if toml.Unmarshal(b, &f) == nil {
			if c := strings.TrimSpace(f.Test.Command); c != "" {
				return c, ".blueprint/config.toml [test] command", true
			}
		}
	}
	if c, src, detected := testcmd.Detect(repoRoot); detected {
		return c, src + " (auto-detected)", true
	}
	return "", "", false
}

// Verifier is one entry in .blueprint/verifiers.toml (DESIGN §7): one shape,
// every domain — command + repo-versioned config + exit code + report file.
// No SDK: anything with an exit code plugs in.
type Verifier struct {
	Name         string   `toml:"name"`
	Command      string   `toml:"command"`
	ConfigPath   string   `toml:"config_path"`
	ReportFormat string   `toml:"report_format"` // json | sarif | text
	AppliesTo    []string `toml:"applies_to"`    // scenario classes
}

type verifiersFile struct {
	Verifier []Verifier `toml:"verifier"`
}

// loadVerifiers reads verifiers.toml; a missing file means no verifiers are
// configured, which is valid (not every repo has domain verifiers).
func loadVerifiers(repoRoot string) ([]Verifier, error) {
	b, err := os.ReadFile(verifiersPath(repoRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var vf verifiersFile
	if err := toml.Unmarshal(b, &vf); err != nil {
		return nil, fmt.Errorf(".blueprint/verifiers.toml is not valid TOML (%v); each verifier is a [[verifier]] table with name, command, config_path, report_format, applies_to", err)
	}
	for i, v := range vf.Verifier {
		if v.Name == "" || v.Command == "" {
			return nil, fmt.Errorf(".blueprint/verifiers.toml entry %d: name and command are required; see DESIGN §7 for the verifier contract", i+1)
		}
	}
	return vf.Verifier, nil
}

func (v Verifier) appliesTo(scenario string) bool {
	if len(v.AppliesTo) == 0 {
		return false // scope-matched only; empty scope never auto-runs
	}
	for _, s := range v.AppliesTo {
		if s == scenario || s == "*" {
			return true
		}
	}
	return false
}

// splitCommand tokenizes a verifier command honoring double/single quotes.
// Deliberately not a shell: no expansion, no pipes, no redirects — verifiers
// needing shell features wrap themselves in a script (Windows-clean rule).
func splitCommand(s string) []string {
	var (
		args  []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

func reportExt(format string) string {
	switch strings.ToLower(format) {
	case "json":
		return "json"
	case "sarif":
		return "sarif"
	default:
		return "txt"
	}
}

// runVerifier executes one verifier with CHANGE=<id> in the environment,
// captures combined output into .blueprint/changes/<id>/verdict/, and maps
// the exit code to a CheckResult (0 = pass).
func runVerifier(repoRoot, changeID string, v Verifier, reqID string) core.CheckResult {
	res := core.CheckResult{Name: v.Name, ReqID: reqID}

	command := v.Command
	var cmdSource string
	if strings.Contains(command, testCmdToken) {
		tc, src, ok := resolveTestCmd(repoRoot)
		if !ok {
			// Remediation-style skip that FAILS: an unresolvable test command
			// must never read as a green check (false pass).
			res.ExitCode = -1
			res.Detail = fmt.Sprintf("verifier %q needs the repo test command ({{TEST_CMD}}) but none is configured or detectable; set it in .blueprint/config.toml as [test] command = \"<your test command>\" — auto-detection covers package.json (npm test), go.mod (go test ./...), pyproject.toml/pytest.ini (pytest), Cargo.toml (cargo test), and a Makefile test target (make test)", v.Name)
			return res
		}
		command = strings.ReplaceAll(command, testCmdToken, tc)
		cmdSource = src
	}

	args := splitCommand(command)
	if len(args) == 0 {
		res.Detail = fmt.Sprintf("verifier %q has an empty command; fix .blueprint/verifiers.toml", v.Name)
		res.ExitCode = -1
		return res
	}

	if err := os.MkdirAll(verdictDir(repoRoot, changeID), 0o755); err != nil {
		res.Detail = err.Error()
		res.ExitCode = -1
		return res
	}
	reportPath := filepath.Join(verdictDir(repoRoot, changeID), v.Name+".report."+reportExt(v.ReportFormat))

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CHANGE="+changeID)
	out, err := cmd.CombinedOutput()

	// The report is written even on failure — the machine-readable trail is
	// the point of the verdict/ directory.
	if werr := os.WriteFile(reportPath, out, 0o644); werr == nil {
		rel, rerr := filepath.Rel(repoRoot, reportPath)
		if rerr == nil {
			res.ReportPath = filepath.ToSlash(rel)
		}
	}

	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			res.Detail = fmt.Sprintf("verifier %q exited %d; read its report at %s and fix the findings, or adjust %s if the check is misconfigured", v.Name, res.ExitCode, res.ReportPath, v.ConfigPath)
		} else {
			res.ExitCode = -1
			res.Detail = fmt.Sprintf("verifier %q could not run (%v); check that the command %q is installed and on PATH", v.Name, err, args[0])
		}
		return res
	}
	res.Pass = true
	if cmdSource != "" {
		res.Detail = fmt.Sprintf("test command %q resolved from %s", strings.Join(args, " "), cmdSource)
	}
	return res
}

// knowledgecmds.go — the knowledge feature's CLI surface. The knowledge lint
// extends `blueprint lint` (registered in speccmds.go, spec-feature-owned);
// this file keeps the knowledge-specific config plumbing out of that file.
package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"blueprint/internal/core"
	"blueprint/internal/lint"
)

// knowledgeLintConfig extends the shared lint config with the [lint]
// human_verify_budget key. Best-effort like specLoadLintConfig: a missing or
// partially-foreign config.toml never blocks linting.
func knowledgeLintConfig(repoRoot string) lint.Config {
	cfg := specLoadLintConfig(repoRoot)
	var fileCfg struct {
		Lint struct {
			HumanVerifyBudget *int `toml:"human_verify_budget"`
		} `toml:"lint"`
	}
	path := filepath.Join(repoRoot, ".blueprint", "config.toml")
	if _, err := os.Stat(path); err == nil {
		_, _ = toml.DecodeFile(path, &fileCfg)
	}
	cfg.HumanVerifyBudget = fileCfg.Lint.HumanVerifyBudget
	return cfg
}

// knowledgeLintFindings runs the knowledge lint with the wall clock as the
// explicit freshness input (CONTRACTS rule 5).
func knowledgeLintFindings(repoRoot string) ([]core.LintFinding, error) {
	return lint.Knowledge(repoRoot, time.Now().UTC(), knowledgeLintConfig(repoRoot))
}

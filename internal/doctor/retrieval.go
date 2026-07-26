// retrieval.go — Tier 2/3 environment check (DESIGN §9). The tiers are
// exec'd external tools, so a configured-but-missing launcher is the same
// scar as an inert hook: everyone believes scaled retrieval is on while
// every call silently degrades. Nothing configured passes vacuously —
// Tier 0/1 need no environment.
package doctor

import (
	"fmt"
	"sort"
	"strings"

	"blueprint/internal/retrieval"
)

func checkRetrievalTiers(repoRoot string, opts Options) Check {
	const name = "retrieval-tiers"
	cfg, err := retrieval.Load(repoRoot)
	if err != nil {
		return Check{Name: name, Pass: false,
			Detail:      err.Error(),
			Remediation: "fix .blueprint/config.toml so the [retrieval] table parses — until it does, no retrieval tier past 0/1 can be wired"}
	}
	tools, err := retrieval.Tier2Tools(cfg)
	if err != nil {
		return Check{Name: name, Pass: false,
			Detail:      err.Error(),
			Remediation: fmt.Sprintf("set tier2_* in .blueprint/config.toml [retrieval] to a supported tool (%s) or clear the value", strings.Join(retrieval.KnownToolNames(), ", "))}
	}
	if len(tools) == 0 && !cfg.GraphEnabled() {
		return Check{Name: name, Pass: true,
			Detail: "no Tier 2/3 backends configured — Tier 0/1 retrieval only, nothing to validate"}
	}

	var toolNames []string
	for n := range tools {
		toolNames = append(toolNames, n)
	}
	sort.Strings(toolNames)

	var present, missing, remedies []string
	for _, n := range toolNames {
		tool := tools[n]
		if _, err := opts.lookPath(tool.Command); err != nil {
			missing = append(missing, fmt.Sprintf("Tier-2 %s (%s) needs %q on PATH", tool.Kind, tool.Name, tool.Command))
			remedies = append(remedies, tool.Install)
		} else {
			present = append(present, fmt.Sprintf("%s via %s", tool.Name, tool.Command))
		}
	}
	if cfg.GraphEnabled() {
		argv := retrieval.SplitCommand(cfg.Graph.Command)
		if !resolves(repoRoot, argv[0], opts) {
			missing = append(missing, fmt.Sprintf("Tier-3 graph backend %q does not resolve (PATH or repo-relative)", argv[0]))
			remedies = append(remedies, fmt.Sprintf("install the graph backend so %q resolves, or clear [retrieval.graph] command in .blueprint/config.toml", argv[0]))
		} else {
			present = append(present, fmt.Sprintf("graph via %s", argv[0]))
		}
	}
	if len(missing) > 0 {
		return Check{Name: name, Pass: false,
			Detail:      strings.Join(missing, "; "),
			Remediation: strings.Join(remedies, "; ")}
	}
	return Check{Name: name, Pass: true,
		Detail: fmt.Sprintf("configured retrieval backends resolve: %s", strings.Join(present, ", "))}
}

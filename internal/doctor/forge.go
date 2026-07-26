// forge.go — forge detection from the origin remote URL (DESIGN §8 host
// enforcement mapping). Heuristics over the URL string ONLY: doctor never
// probes the network (CONTRACTS rule 4), so every profile carries notes
// saying what must be verified by hand on the forge itself.
package doctor

import (
	"strings"

	"blueprint/internal/core"
)

// DetectForge maps an origin remote URL to an enforcement profile. Only
// GitHub and Gitea earn Enforced=true (protected branches + merge-blocking
// required checks exist on their free tiers); everything else is advisory and
// the notes name the manual verification.
func DetectForge(remoteURL string) core.EnforcementProfile {
	u := strings.ToLower(remoteURL)
	switch {
	case strings.Contains(u, "github.com"):
		return core.EnforcementProfile{Forge: "github", Enforced: true,
			Notes: "URL heuristic only (no network probe): confirm in repo settings that main is protected, agent/** is pattern-protected, the `blueprint verify` check is required, and CODEOWNERS covers .blueprint/{specs,safety.toml}"}
	case strings.Contains(u, "gitea"):
		return core.EnforcementProfile{Forge: "gitea", Enforced: true,
			Notes: "URL heuristic only (no network probe): confirm branch protection on main with the `blueprint verify` status check required before merge"}
	case strings.Contains(u, "gitlab"):
		return core.EnforcementProfile{Forge: "gitlab", Enforced: false,
			Notes: "advisory: GitLab Free cannot make pipeline checks merge-blocking — verify manually whether your tier supports required pipelines + protected branches; until then the `blueprint verify` CI job is the gate and L3 is unavailable"}
	case strings.Contains(u, "bitbucket"):
		return core.EnforcementProfile{Forge: "bitbucket", Enforced: false,
			Notes: "advisory: Bitbucket below Premium cannot enforce merge checks — verify your plan's branch-permission + required-builds settings manually; until then L3 is unavailable"}
	case strings.Contains(u, "dev.azure.com"), strings.Contains(u, "visualstudio.com"):
		return core.EnforcementProfile{Forge: "azure", Enforced: false,
			Notes: "advisory: verify manually that a branch policy makes the `blueprint verify` build a required check on main; doctor does not probe Azure DevOps"}
	case u == "":
		return core.EnforcementProfile{Forge: "unknown", Enforced: false,
			Notes: "no origin remote — add one (`git remote add origin <url>`) and re-run `blueprint doctor`; autonomy stays advisory until a forge is known"}
	default:
		return core.EnforcementProfile{Forge: "unknown", Enforced: false,
			Notes: "unrecognized forge URL — verify manually that your host can make `blueprint verify` a merge-blocking required check; autonomy stays advisory (L3 unavailable) until enforcement is confirmed"}
	}
}

// Package route is the two-axis ceremony router (DESIGN §4): change type
// (Conventional Commits) × risk tier (exempt/light/full). Routing is a pure
// function of its inputs — same registry, safety config, thresholds, and diff
// always yield the same tier (CONTRACTS rule 5). Every decision carries the
// per-input reasons and the projected ceremony cost, because unrouted ceremony
// burns tokens without moving outcomes.
package route

import (
	"fmt"
	"sort"
	"strings"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// ChangeTypes is the Conventional-Commits-compatible enum for --type.
var ChangeTypes = []string{
	"feat", "fix", "docs", "style", "refactor", "perf", "test",
	"build", "ci", "chore", "revert",
}

// ValidType reports whether t is a known conventional-commits type.
func ValidType(t string) bool {
	for _, k := range ChangeTypes {
		if k == t {
			return true
		}
	}
	return false
}

// Reason records one input's contribution to the decision, human-readable.
// Tier is the tier this input argued for ("" when it argued for nothing).
type Reason struct {
	Axis   string            `json:"axis"` // registry | sensitive | blast-radius | loc | reversibility | default | override | sev1 | reevaluate
	Detail string            `json:"detail"`
	Tier   core.CeremonyTier `json:"tier,omitempty"`
}

// Cost is the projected ceremony for a tier: the artifacts the tier requires
// and a rough token estimate for producing them.
type Cost struct {
	Tier      core.CeremonyTier `json:"tier"`
	Artifacts []string          `json:"artifacts"`
	Tokens    int               `json:"est_tokens"`
}

// Line renders the one-line projected-ceremony-cost the router prints with
// every decision.
func (c Cost) Line() string {
	return fmt.Sprintf("projected ceremony (%s): %s — est. ~%dk tokens",
		c.Tier, strings.Join(c.Artifacts, " + "), c.Tokens/1000)
}

// CostFor returns the DESIGN §4 per-tier artifact list with rough token
// estimates (light ≈ small change.md + one regression loop; full ≈ design +
// approval + model-checker ceremony; the superpowers null benchmark's +625k
// uniform-ceremony overhead is what tiering avoids).
func CostFor(tier core.CeremonyTier) Cost {
	switch tier {
	case core.TierExempt:
		return Cost{Tier: tier, Artifacts: []string{"one worklog line (existing verifiers still run)"}, Tokens: 1000}
	case core.TierFull:
		return Cost{Tier: tier, Artifacts: []string{
			"change.md with Design section",
			"human approval gate (draft→approved)",
			"model checker pass",
			"REQ-traced tests",
		}, Tokens: 150000}
	default:
		return Cost{Tier: core.TierLight, Artifacts: []string{
			"change.md (1–3 EARS deltas + loop contract)",
			"regression test red→green",
		}, Tokens: 25000}
	}
}

// Inputs is everything the router looks at for one decision.
type Inputs struct {
	ChangeType string   `json:"change_type"`
	Paths      []string `json:"paths"`
	ChangedLOC int      `json:"changed_loc"` // 0 = unknown (declared paths only)
}

// Decision is the router output: tier + each input's contribution + cost.
type Decision struct {
	Tier    core.CeremonyTier `json:"tier"`
	Reasons []Reason          `json:"reasons"`
	Cost    Cost              `json:"cost"`
}

// Router bundles the loaded inputs. Zero-value fields are safe: nil registry
// and safety act as empty, nil Blast falls back to PathCount.
type Router struct {
	Config   Config
	Registry *Registry
	Safety   *Safety
	Blast    BlastRadius
}

// Load builds a Router from a repo's .blueprint config files, with the
// path-count blast-radius fallback (repomap plugs in later).
func Load(repoRoot string) (*Router, error) {
	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		return nil, err
	}
	reg, err := LoadRegistry(repoRoot)
	if err != nil {
		return nil, err
	}
	saf, err := LoadSafety(repoRoot)
	if err != nil {
		return nil, err
	}
	return &Router{Config: cfg, Registry: reg, Safety: saf, Blast: PathCount{}}, nil
}

func tierRank(t core.CeremonyTier) int {
	switch t {
	case core.TierExempt:
		return 0
	case core.TierFull:
		return 2
	default: // light, and the safe default for unknown values
		return 1
	}
}

func maxTier(a, b core.CeremonyTier) core.CeremonyTier {
	if tierRank(b) > tierRank(a) {
		return b
	}
	return a
}

func bumpTier(t core.CeremonyTier) core.CeremonyTier {
	switch t {
	case core.TierExempt:
		return core.TierLight
	default:
		return core.TierFull
	}
}

// Decide routes one change. Default is light ("start light and escalate");
// registry match lowers to exempt; blast radius / LOC / reversibility raise;
// sensitive paths floor at full and are absolute — they override a registry
// exemption.
func (r *Router) Decide(in Inputs) Decision {
	reg, saf, blast := r.Registry, r.Safety, r.Blast
	if reg == nil {
		reg = &Registry{}
	}
	if saf == nil {
		saf = &Safety{}
	}
	if blast == nil {
		blast = PathCount{}
	}
	paths := normalizePaths(in.Paths)
	tier := core.TierLight
	reasons := []Reason{{Axis: "default", Detail: "start light and escalate on evidence", Tier: core.TierLight}}

	// Axis 1: registry match → exempt.
	if class := reg.Match(in.ChangeType, paths, in.ChangedLOC); class != nil {
		tier = core.TierExempt
		reasons = append(reasons, Reason{
			Axis: "registry",
			Detail: fmt.Sprintf("all paths match pre-approved class %q (type=%s, max_loc=%d, required checks: %s)",
				class.Name, orAny(class.Type), class.MaxLOC, orNone(class.Checks)),
			Tier: core.TierExempt,
		})
	} else {
		reasons = append(reasons, Reason{Axis: "registry", Detail: "no standard-change class matches"})
	}

	// Axis 2: blast radius (fallback: path count) and changed LOC.
	radius, err := blast.Radius(paths)
	switch {
	case err != nil:
		reasons = append(reasons, Reason{Axis: "blast-radius", Detail: fmt.Sprintf("estimate unavailable (%v); path count used elsewhere", err)})
	case radius >= r.Config.EscalateRadius && r.Config.EscalateRadius > 0:
		tier = maxTier(tier, core.TierFull)
		reasons = append(reasons, Reason{
			Axis:   "blast-radius",
			Detail: fmt.Sprintf("radius %d ≥ threshold %d", radius, r.Config.EscalateRadius),
			Tier:   core.TierFull,
		})
	default:
		reasons = append(reasons, Reason{Axis: "blast-radius", Detail: fmt.Sprintf("radius %d < threshold %d", radius, r.Config.EscalateRadius)})
	}
	if r.Config.EscalateLOC > 0 && in.ChangedLOC >= r.Config.EscalateLOC {
		tier = maxTier(tier, core.TierFull)
		reasons = append(reasons, Reason{
			Axis:   "loc",
			Detail: fmt.Sprintf("changed LOC %d ≥ escalate_loc %d (review-collapse threshold)", in.ChangedLOC, r.Config.EscalateLOC),
			Tier:   core.TierFull,
		})
	} else {
		reasons = append(reasons, Reason{Axis: "loc", Detail: fmt.Sprintf("changed LOC %d < escalate_loc %d", in.ChangedLOC, r.Config.EscalateLOC)})
	}

	// Axis 3: reversibility — one-way touch bumps one step.
	if hits := saf.OneWayHits(paths); len(hits) > 0 {
		tier = bumpTier(tier)
		reasons = append(reasons, Reason{
			Axis:   "reversibility",
			Detail: "one-way paths touched: " + strings.Join(hits, ", "),
			Tier:   tier,
		})
	} else {
		reasons = append(reasons, Reason{Axis: "reversibility", Detail: "no one-way (schema/data/API/money) paths touched"})
	}

	// Axis 4: sensitive paths — floor of full, absolute (overrides registry).
	if hits := saf.SensitiveHits(paths); len(hits) > 0 {
		if r.Config.EscalateOnSensitiveTouch {
			tier = maxTier(tier, core.TierFull)
			reasons = append(reasons, Reason{
				Axis:   "sensitive",
				Detail: "sensitive paths touched (floor of full, overrides registry): " + strings.Join(hits, ", "),
				Tier:   core.TierFull,
			})
		} else {
			reasons = append(reasons, Reason{
				Axis:   "sensitive",
				Detail: "sensitive paths touched but escalate_on_sensitive_touch = false: " + strings.Join(hits, ", "),
			})
		}
	} else {
		reasons = append(reasons, Reason{Axis: "sensitive", Detail: "no sensitive paths touched"})
	}

	return Decision{Tier: tier, Reasons: reasons, Cost: CostFor(tier)}
}

// Reevaluate re-runs routing mid-flight as the diff grows (DESIGN §4). It may
// only escalate — a change never gets less ceremony after work started. An
// escalation is appended to the worklog; staying put logs nothing.
func (r *Router) Reevaluate(repoRoot string, c *core.Change, stats DiffStats) (Decision, bool, error) {
	fresh := r.Decide(Inputs{ChangeType: c.Type, Paths: stats.Paths, ChangedLOC: stats.ChangedLOC})
	if tierRank(fresh.Tier) <= tierRank(c.Tier) {
		kept := Decision{Tier: c.Tier, Cost: CostFor(c.Tier)}
		kept.Reasons = append([]Reason{{
			Axis:   "reevaluate",
			Detail: fmt.Sprintf("re-evaluation argued %s; tier stays %s (never de-escalates)", fresh.Tier, c.Tier),
			Tier:   c.Tier,
		}}, fresh.Reasons...)
		return kept, false, nil
	}
	from := c.Tier
	c.Tier = fresh.Tier
	fresh.Reasons = append([]Reason{{
		Axis:   "reevaluate",
		Detail: fmt.Sprintf("mid-flight escalation %s → %s at %d changed LOC", from, fresh.Tier, stats.ChangedLOC),
		Tier:   fresh.Tier,
	}}, fresh.Reasons...)
	err := worklog.Append(repoRoot, core.JournalEvent{
		Kind:     "route-escalate",
		ChangeID: c.ID,
		Data: map[string]any{
			"from":        string(from),
			"to":          string(fresh.Tier),
			"changed_loc": stats.ChangedLOC,
			"reasons":     reasonStrings(fresh.Reasons),
		},
	})
	return fresh, true, err
}

// reasonStrings flattens reasons for journal data.
func reasonStrings(rs []Reason) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.Tier != "" {
			out = append(out, fmt.Sprintf("[%s→%s] %s", r.Axis, r.Tier, r.Detail))
		} else {
			out = append(out, fmt.Sprintf("[%s] %s", r.Axis, r.Detail))
		}
	}
	return out
}

func normalizePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

func orNone(ss []string) string {
	if len(ss) == 0 {
		return "none"
	}
	return strings.Join(ss, ",")
}

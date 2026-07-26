// Output renderers: Prometheus text exposition (--format prom) and the human
// summary. JSON output is the Report itself. Both iterate Keys so ordering is
// deterministic (CONTRACTS rule 5).
package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// promHelp is the HELP line per metric key.
var promHelp = map[string]string{
	"time_to_first_verified_change": "Seconds from adoption stage-0/init to the first green verdict",
	"time_to_install":               "Seconds the last blueprint init/adopt run took",
	"upgrade_success":               "Upgrade success rate (measured in CI)",
	"time_to_onboard":               "Seconds across recorded adoption stage exits",
	"context_retrieval_efficiency":  "Cross-tier retrieval margin from the bench report (joint with success)",
	"index_freshness":               "Seconds the repo map lags the last commit (positive = stale)",
	"rework_rate":                   "Fraction of archived changes reworked by revert/fix commits within 30 days",
	"ceremony_fit":                  "1 minus the human-override rate on router decisions",
	"cost_per_verified_change":      "Journal-folded cost of verified changes, grouped by ceremony tier",
	"escaped_defect_rate":           "Incident events per verified change",
	"self_improvement_velocity":     "Failure-class recurrence across dream cycles (declining = improving)",
	"knowledge_health":              "Knowledge artifacts: lint pass rate, freshness buckets, orphan count (garden net-lines joins when automation ships)",
	"portability":                   "Adapter round-trip health (measured in CI)",
	"supervision_ratio":             "Concurrent active loops per human approver",
}

// FormatProm renders the report as Prometheus text exposition. Scalar rows
// become gauges; the per-tier cost row becomes labeled series; null rows are
// emitted as comments naming the reason — never dropped.
func FormatProm(r *Report) string {
	var b strings.Builder
	for _, key := range Keys {
		v, ok := r.Metrics[key]
		if !ok {
			continue
		}
		name := "blueprint_" + key
		if v.Value == nil {
			fmt.Fprintf(&b, "# %s unavailable: %s\n", name, v.Reason)
			continue
		}
		switch val := v.Value.(type) {
		case float64:
			writeGauge(&b, name, key, fmt.Sprintf("%g", val))
		case int:
			writeGauge(&b, name, key, fmt.Sprintf("%d", val))
		default:
			if key == "cost_per_verified_change" {
				writeCostSeries(&b, name, key, v)
				continue
			}
			// Structured values (e.g. bench margin) do not fit a gauge.
			fmt.Fprintf(&b, "# %s is structured — see reports/metrics.json\n", name)
		}
	}
	return b.String()
}

func writeGauge(b *strings.Builder, name, key, val string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", name, promHelp[key], name, name, val)
}

func writeCostSeries(b *strings.Builder, name, key string, v Value) {
	buckets, ok := anyToCostBuckets(v.Value)
	if !ok {
		fmt.Fprintf(b, "# %s is structured — see reports/metrics.json\n", name)
		return
	}
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n", name+"_usd", promHelp[key], name+"_usd")
	tiers := make([]string, 0, len(buckets))
	for t := range buckets {
		tiers = append(tiers, t)
	}
	sort.Strings(tiers)
	for _, t := range tiers {
		fmt.Fprintf(b, "%s_usd{tier=%q} %g\n", name, t, buckets[t].USDPer)
	}
	for _, t := range tiers {
		fmt.Fprintf(b, "%s_tokens{tier=%q} %g\n", name, t, buckets[t].TokensPer)
	}
}

// anyToCostBuckets accepts both the in-process fold type and the decoded JSON
// shape so prom output works on freshly computed and re-read reports alike.
func anyToCostBuckets(v any) (map[string]costBucket, bool) {
	switch m := v.(type) {
	case map[string]costBucket:
		return m, true
	case map[string]any:
		out := map[string]costBucket{}
		for tier, raw := range m {
			fields, ok := raw.(map[string]any)
			if !ok {
				return nil, false
			}
			var bk costBucket
			if f, ok := fields["usd_per_change"].(float64); ok {
				bk.USDPer = f
			}
			if f, ok := fields["tokens_per_change"].(float64); ok {
				bk.TokensPer = f
			}
			out[tier] = bk
		}
		return out, true
	}
	return nil, false
}

// FormatHuman renders the aligned terminal summary: value or the null reason.
func FormatHuman(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "metrics generated %s → reports/metrics.json\n\n", r.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	width := 0
	for _, k := range Keys {
		if len(k) > width {
			width = len(k)
		}
	}
	for _, key := range Keys {
		v, ok := r.Metrics[key]
		if !ok {
			continue
		}
		if v.Value == nil {
			fmt.Fprintf(&b, "  %-*s  — %s\n", width, key, v.Reason)
			continue
		}
		switch val := v.Value.(type) {
		case float64:
			fmt.Fprintf(&b, "  %-*s  %g %s", width, key, val, v.Unit)
		case int:
			fmt.Fprintf(&b, "  %-*s  %d %s", width, key, val, v.Unit)
		default:
			fmt.Fprintf(&b, "  %-*s  (structured — see reports/metrics.json)", width, key)
		}
		if v.Baseline != nil {
			fmt.Fprintf(&b, "  [baseline %v]", v.Baseline)
		}
		b.WriteString("\n")
	}
	return b.String()
}

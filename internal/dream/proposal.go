// proposal.go — turns signals into the AC-10 proposal shape: ≤5 itemized,
// stable-ID'd (D-<date>-N), absolute-dated, evidence-cited items, each ≤40
// changed lines. [user]-tier files (steering/registry/config) are NEVER edited
// directly — every file change ships as a git-apply-able patch the human
// applies after merge review. Quarantined signals render as comments only.
package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxItems      = 5
	maxPatchLines = 40
)

// Item is one proposal entry.
type Item struct {
	ID          string   `json:"id"` // D-<date>-N
	Title       string   `json:"title"`
	Body        string   `json:"body"` // markdown, absolute dates only
	SignalIDs   []string `json:"signal_ids"`
	Patch       string   `json:"patch,omitempty"`      // unified diff, "" = advisory only
	PatchPath   string   `json:"patch_path,omitempty"` // patches/D-<date>-N.patch
	Quarantined bool     `json:"quarantined,omitempty"`
	Note        string   `json:"note,omitempty"` // cap-enforcement / validation notes
}

// buildDeterministicItems maps signals to proposal items without a model:
// severity order, registry candidates merged into one item (their patches
// would collide on registry.toml's tail), then the ≤5-item cap.
func buildDeterministicItems(repoRoot, date string, signals []Signal) ([]Item, []string) {
	var items []Item
	var warnings []string
	steeringSeq := 0

	var registrySignals []Signal
	for _, s := range signals {
		switch s.Kind {
		case SigTamper:
			items = append(items, Item{
				Title:     "Investigate tamper evidence before any re-approval",
				SignalIDs: []string{s.ID},
				Body: s.Summary + "\n\nReview each cited event, decide whether the drift was a legitimate spec/test " +
					"evolution or verifier weakening, and only then re-stamp with `blueprint approve <id> --amend` " +
					"(the amendment is journaled). Do not loosen the tamper check itself.",
				Quarantined: s.Quarantined,
			})
		case SigRepeatedFailure:
			steeringSeq++
			fp, _ := s.Detail["fingerprint"].(string)
			name := fmt.Sprintf("dream-%s-%d.md", date, steeringSeq)
			rel := ".blueprint/steering/" + name
			content := fmt.Sprintf(`---
id: dream-%s-%d
description: Recurring verify failure fingerprint %.12s (%d occurrences)
globs: []
activation: manual
---

%s

Root-cause this failure class once, then encode the fix here as a real
steering rule (set globs + activation) or add a deterministic verifier so the
class cannot re-enter changes. Evidence: see proposal item in
.blueprint/dream/%s/proposal.md.
`, date, steeringSeq, fp, s.Count, s.Summary, date)
			items = append(items, Item{
				Title:     fmt.Sprintf("Steering stub for recurring failure fingerprint %.12s…", fp),
				SignalIDs: []string{s.ID},
				Body: s.Summary + "\n\nThe patch adds a draft steering rule (manual activation — inert until " +
					"a human edits and enables it). Replace the placeholder body with the actual rule once root-caused.",
				Patch:       newFileDiff(rel, content),
				Quarantined: s.Quarantined,
			})
		case SigRegistryCandidate:
			registrySignals = append(registrySignals, s)
		case SigOverrideCluster:
			steeringSeq++
			ctype, _ := s.Detail["type"].(string)
			dir, _ := s.Detail["direction"].(string)
			name := fmt.Sprintf("dream-%s-%d.md", date, steeringSeq)
			rel := ".blueprint/steering/" + name
			content := fmt.Sprintf(`---
id: dream-%s-%d
description: Router mis-tiering — type %q repeatedly overridden %sward
globs: []
activation: manual
---

%s

Humans keep correcting the router the same way. Adjust the [router] thresholds
or sensitive paths in .blueprint/config.toml (or add a registry class) so the
routed tier matches what reviewers actually enforce, then delete this stub.
`, date, steeringSeq, ctype, dir, s.Summary)
			items = append(items, Item{
				Title:     fmt.Sprintf("Router correction: type %q keeps being overridden %sward", ctype, dir),
				SignalIDs: []string{s.ID},
				Body: s.Summary + "\n\nThe patch adds a draft steering note; the durable fix belongs in " +
					".blueprint/config.toml router thresholds, which stays human-edited.",
				Patch:       newFileDiff(rel, content),
				Quarantined: s.Quarantined,
			})
		case SigBreakerPattern:
			pattern, _ := s.Detail["pattern"].(string)
			items = append(items, Item{
				Title:     fmt.Sprintf("Tighten loop caps for recurring breaker pattern %q", pattern),
				SignalIDs: []string{s.ID},
				Body: s.Summary + "\n\nConsider lowering the matching breaker threshold or the max_iterations/" +
					"max_usd caps in the affected change contracts so stalls park sooner. Contract frontmatter " +
					"is [user]-tier — apply by hand.",
				Quarantined: s.Quarantined,
			})
		case SigUngatedHuman:
			items = append(items, Item{
				Title:     "Ungated `verify: human` escape hatches",
				SignalIDs: []string{s.ID},
				Body: s.Summary + "\n\nEach `human:` method must trigger a real gate. Convert these to " +
					"test/check/bench where possible; where a human question is genuinely required, wire the " +
					"gate so it journals a human-gate event.",
				Quarantined: s.Quarantined,
			})
		}
	}

	if len(registrySignals) > 0 {
		items = append(items, registryItem(repoRoot, date, registrySignals))
	}

	if len(items) > maxItems {
		warnings = append(warnings, fmt.Sprintf(
			"dream: %d proposal items exceeded the AC-10 cap of %d; lowest-severity items were dropped — they will resurface next run if the signal persists", len(items), maxItems))
		items = items[:maxItems]
	}
	return items, warnings
}

// registryItem merges every registry-candidate signal into one commented
// registry.toml append: the classes are real proposals, but globs are unknown
// to the machine, so the block ships commented for the human to complete —
// uncommenting is the act of approval.
func registryItem(repoRoot, date string, signals []Signal) Item {
	var b strings.Builder
	var ids []string
	quarantined := false
	for _, s := range signals {
		scenario, _ := s.Detail["scenario"].(string)
		ids = append(ids, s.ID)
		quarantined = quarantined || s.Quarantined
		fmt.Fprintf(&b, `
# blueprint dream %s (%s): scenario %q had %d consecutive clean verified runs.
# Registry candidate — review, fill globs, set max_loc, then uncomment.
# [[class]]
# name = %q
# type = ""
# globs = []
# max_loc = 150
# checks = []
`, date, s.ID, scenario, s.Count, scenario)
	}
	rel := ".blueprint/registry.toml"
	existing, _ := os.ReadFile(filepath.Join(repoRoot, ".blueprint", "registry.toml"))
	summaries := make([]string, 0, len(signals))
	for _, s := range signals {
		summaries = append(summaries, "- "+s.Summary)
	}
	return Item{
		Title:     "Standard-change registry candidates",
		SignalIDs: ids,
		Body: strings.Join(summaries, "\n") + "\n\nThe patch appends commented class blocks to " +
			".blueprint/registry.toml ([user]-tier — never machine-edited). Registry expansion is a " +
			"human gate: fill in the globs each class actually touches before uncommenting.",
		Patch:       appendDiff(rel, existing, b.String()),
		Quarantined: quarantined,
	}
}

// enforceItems applies the safety envelope every item passes regardless of
// origin (deterministic or model): stable IDs, quarantine strips patches, the
// 40-changed-line patch cap, and patch paths for what survives.
func enforceItems(items []Item, date string) []Item {
	for i := range items {
		it := &items[i]
		it.ID = fmt.Sprintf("D-%s-%d", date, i+1)
		if it.Quarantined && it.Patch != "" {
			it.Patch = ""
			it.Note = appendNote(it.Note, "patch withheld: derived from untrusted content (quarantined)")
		}
		if it.Patch != "" {
			if n := changedLines(it.Patch); n > maxPatchLines {
				it.Patch = ""
				it.Note = appendNote(it.Note, fmt.Sprintf("patch withheld: %d changed lines exceeds the AC-10 per-item cap of %d — split the proposal", n, maxPatchLines))
			}
		}
		if it.Patch != "" {
			it.PatchPath = "patches/" + it.ID + ".patch"
		}
	}
	return items
}

func appendNote(existing, note string) string {
	if existing == "" {
		return note
	}
	return existing + "; " + note
}

// renderProposal produces proposal.md. Quarantined items render inside HTML
// comments with a provenance tag so they can never be mechanically applied.
func renderProposal(res *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Blueprint dream proposal — %s\n\n", res.Date)
	since := "repository start"
	if !res.Since.IsZero() {
		since = res.Since.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "Generated by `blueprint dream` on %s (UTC) from journal signals since %s.\n", res.Date, since)
	fmt.Fprintf(&b, "Branch: `%s`. Human merge only (AC-10): review each item; apply patches with\n`git apply .blueprint/dream/%s/patches/<item>.patch` after merging.\n\n", res.Branch, res.Date)

	fmt.Fprintf(&b, "## Signals\n\n")
	for _, s := range res.Signals {
		tag := ""
		if s.Quarantined {
			tag = " [QUARANTINED: untrusted-content provenance]"
		}
		fmt.Fprintf(&b, "- %s (%s)%s: %s\n", s.ID, s.Kind, tag, s.Summary)
	}
	b.WriteString("\n")

	for _, it := range res.Items {
		if it.Quarantined {
			fmt.Fprintf(&b, "## %s — [QUARANTINED] %s\n\n", it.ID, it.Title)
			b.WriteString("<!-- quarantined: derived from untrusted content (journal events tagged data.source=web or data.tainted=true).\nRendered as a comment only — never as an applicable patch. Verify independently before acting.\n")
			writeItemBody(&b, it, res)
			b.WriteString("-->\n\n")
			continue
		}
		fmt.Fprintf(&b, "## %s — %s\n\n", it.ID, it.Title)
		writeItemBody(&b, it, res)
		b.WriteString("\n")
	}
	return b.String()
}

func writeItemBody(b *strings.Builder, it Item, res *Result) {
	fmt.Fprintf(b, "Signals: %s\n\nEvidence:\n", strings.Join(it.SignalIDs, ", "))
	for _, sid := range it.SignalIDs {
		for _, s := range res.Signals {
			if s.ID != sid {
				continue
			}
			for _, e := range capEvidence(s.Evidence) {
				fmt.Fprintf(b, "- %s\n", e.String())
			}
		}
	}
	b.WriteString("\n" + it.Body + "\n")
	if it.Note != "" {
		fmt.Fprintf(b, "\nNote: %s\n", it.Note)
	}
	if it.PatchPath != "" {
		fmt.Fprintf(b, "\nPatch: %s\n", it.PatchPath)
	}
}

// capEvidence keeps citations readable: at most 8 per signal.
func capEvidence(ev []Evidence) []Evidence {
	if len(ev) <= 8 {
		return ev
	}
	return ev[:8]
}

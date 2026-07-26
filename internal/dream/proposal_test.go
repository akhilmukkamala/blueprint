package dream

import (
	"strings"
	"testing"
)

func TestEnforceItemsFortyLineCap(t *testing.T) {
	big := strings.Repeat("line\n", 41)
	items := enforceItems([]Item{
		{Title: "too big", SignalIDs: []string{"S-1"}, Patch: newFileDiff("f.md", big)},
		{Title: "fits", SignalIDs: []string{"S-1"}, Patch: newFileDiff("g.md", "one\n")},
	}, "2026-07-26")

	if items[0].Patch != "" || items[0].PatchPath != "" {
		t.Fatalf("oversized patch must be withheld: %+v", items[0])
	}
	if !strings.Contains(items[0].Note, "cap of 40") {
		t.Fatalf("note = %q", items[0].Note)
	}
	if items[1].Patch == "" || items[1].PatchPath != "patches/D-2026-07-26-2.patch" {
		t.Fatalf("compliant patch mangled: %+v", items[1])
	}
	if items[0].ID != "D-2026-07-26-1" || items[1].ID != "D-2026-07-26-2" {
		t.Fatalf("stable IDs wrong: %q %q", items[0].ID, items[1].ID)
	}
}

func TestEnforceItemsQuarantineStripsPatch(t *testing.T) {
	items := enforceItems([]Item{
		{Title: "tainted", SignalIDs: []string{"S-1"}, Quarantined: true, Patch: newFileDiff("f.md", "x\n")},
	}, "2026-07-26")
	if items[0].Patch != "" {
		t.Fatalf("quarantined patch survived: %+v", items[0])
	}
	if !strings.Contains(items[0].Note, "untrusted") {
		t.Fatalf("note = %q", items[0].Note)
	}
}

func TestDeterministicItemsCapAtFive(t *testing.T) {
	// Seven distinct repeated-failure signals -> seven candidate items.
	var signals []Signal
	for i := 0; i < 7; i++ {
		signals = append(signals, Signal{
			ID: "S-" + string(rune('1'+i)), Kind: SigRepeatedFailure, Count: 3,
			Summary: "recurring failure",
			Detail:  map[string]any{"fingerprint": strings.Repeat("f", i+1)},
		})
	}
	items, warns := buildDeterministicItems(t.TempDir(), "2026-07-26", signals)
	if len(items) != maxItems {
		t.Fatalf("items = %d, want %d", len(items), maxItems)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "cap") {
		t.Fatalf("warnings = %v", warns)
	}
}

package autonomy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

var testNow = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func noReverts(string, time.Time) (map[string]time.Time, error) {
	return map[string]time.Time{}, nil
}

// writeChange lays down a change dir (under changes/ or archive/) with a
// scenario-carrying change.md and a journal of the given events.
func writeChange(t *testing.T, root, sub, id, scenario string, events []core.JournalEvent) {
	t.Helper()
	dir := filepath.Join(root, ".blueprint", sub, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := fmt.Sprintf("+++\nid = %q\ntitle = \"t\"\ntype = \"fix\"\ntier = \"light\"\nstatus = \"verified\"\nscenario = %q\n+++\n\n# t\n", id, scenario)
	if err := os.WriteFile(filepath.Join(dir, "change.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ev := range events {
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.ndjson"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func greenVerdict(id string, at time.Time) core.JournalEvent {
	return core.JournalEvent{Time: at, Kind: "verdict", ChangeID: id,
		Data: map[string]any{"pass": true, "tamper": false, "checks": 3}}
}

// seedRuns writes n green archived runs for one scenario, one hour apart.
func seedRuns(t *testing.T, root, scenario string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%03d", scenario, i)
		at := testNow.Add(time.Duration(i-n) * time.Hour)
		writeChange(t, root, "archive", id, scenario, []core.JournalEvent{greenVerdict(id, at)})
	}
}

func seedProfile(t *testing.T, root string, enforced bool, classes map[string]ClassState) {
	t.Helper()
	forge := "gitlab"
	if enforced {
		forge = "github"
	}
	if classes == nil {
		classes = map[string]ClassState{}
	}
	if err := Save(root, &File{
		Profile: core.EnforcementProfile{Forge: forge, Enforced: enforced},
		Classes: classes,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecomputePromoteEligible(t *testing.T) {
	root := t.TempDir()
	seedProfile(t, root, true, nil)
	seedRuns(t, root, "bugfix", 12)

	res, err := Recompute(root, Options{Now: testNow, Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Promotions) != 1 || res.Promotions[0].Scenario != "bugfix" ||
		res.Promotions[0].From != core.L1Propose || res.Promotions[0].To != core.L2Branch {
		t.Fatalf("want bugfix L1->L2 promotion, got %+v", res.Promotions)
	}
	if res.ProposalPath == "" {
		t.Fatal("promote-eligible class must emit a proposal path")
	}
	diff, err := os.ReadFile(ProposalPath(root))
	if err != nil {
		t.Fatalf("proposal diff not written: %v", err)
	}
	for _, want := range []string{
		"PROMOTION PROPOSAL",
		"--- a/.blueprint/autonomy.json",
		"+++ b/.blueprint/autonomy.json",
		`-      "level": 1,`,
		`+      "level": 2,`,
		"git apply",
	} {
		if !strings.Contains(string(diff), want) {
			t.Errorf("proposal diff missing %q\n%s", want, diff)
		}
	}
	// Never auto-applied: the file on disk must still say L1.
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Level("bugfix"); got != core.L1Propose {
		t.Fatalf("promotion was auto-applied: level = L%d, want L1", int(got))
	}
	if len(f.Classes["bugfix"].Window) != 12 {
		t.Fatalf("window not persisted: %d records", len(f.Classes["bugfix"].Window))
	}
}

func TestRecomputeNotEnoughRuns(t *testing.T) {
	root := t.TempDir()
	seedProfile(t, root, true, nil)
	seedRuns(t, root, "bugfix", MinRuns-1)
	// A stale proposal from an earlier recompute must be cleared.
	if err := os.WriteFile(ProposalPath(root), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Recompute(root, Options{Now: testNow, Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Promotions) != 0 || res.ProposalPath != "" {
		t.Fatalf("under-minimum window must not propose: %+v", res)
	}
	if len(res.Classes) != 1 || res.Classes[0].Eligible {
		t.Fatalf("class must be ineligible: %+v", res.Classes)
	}
	if !strings.Contains(res.Classes[0].Reason, "need at least") {
		t.Errorf("reason should teach the minimum-run bar, got %q", res.Classes[0].Reason)
	}
	if _, err := os.Stat(ProposalPath(root)); !os.IsNotExist(err) {
		t.Error("stale proposal diff must be removed when nothing is eligible")
	}
}

func TestRecomputeDemoteOnTamper(t *testing.T) {
	root := t.TempDir()
	seedProfile(t, root, true, map[string]ClassState{"bugfix": {Level: core.L2Branch}})
	id := "bad-change"
	writeChange(t, root, "changes", id, "bugfix", []core.JournalEvent{
		greenVerdict(id, testNow.Add(-2*time.Hour)),
		{Time: testNow.Add(-1 * time.Hour), Kind: "tamper", ChangeID: id,
			Data: map[string]any{"detail": "approved.lock drift"}},
	})

	res, err := Recompute(root, Options{Now: testNow, Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Demotions) != 1 || res.Demotions[0].From != core.L2Branch || res.Demotions[0].To != core.L1Propose {
		t.Fatalf("want L2->L1 demotion, got %+v", res.Demotions)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Level("bugfix"); got != core.L1Propose {
		t.Fatalf("demotion must be auto-applied, level = L%d", int(got))
	}
	// Demotion is journaled with a reason.
	events, _, err := worklog.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == "autonomy" {
			if a, _ := ev.Data["action"].(string); a == "demote" {
				found = true
				if r, _ := ev.Data["reason"].(string); !strings.Contains(r, "tamper") {
					t.Errorf("journaled reason should cite the tamper evidence, got %q", r)
				}
			}
		}
	}
	if !found {
		t.Fatal("demotion must append a worklog event")
	}

	// Same evidence must not demote twice: recompute again, expect no change.
	res2, err := Recompute(root, Options{Now: testNow.Add(time.Hour), Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Demotions) != 0 {
		t.Fatalf("spent tamper evidence re-demoted the class: %+v", res2.Demotions)
	}
}

func TestRecomputeDemoteOnRevert(t *testing.T) {
	root := t.TempDir()
	seedProfile(t, root, true, map[string]ClassState{"bugfix": {Level: core.L2Branch}})
	id := "reverted-change"
	writeChange(t, root, "archive", id, "bugfix", []core.JournalEvent{greenVerdict(id, testNow.Add(-72*time.Hour))})

	res, err := Recompute(root, Options{Now: testNow, Reverts: func(string, time.Time) (map[string]time.Time, error) {
		return map[string]time.Time{id: testNow.Add(-24 * time.Hour)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Demotions) != 1 || !strings.Contains(res.Demotions[0].Reason, "revert") {
		t.Fatalf("revert within 30d must demote with a revert reason, got %+v", res.Demotions)
	}
}

func TestRecomputeAdvisoryCapsL3(t *testing.T) {
	root := t.TempDir()
	// Perfect window at L2, but the forge profile is advisory.
	seedProfile(t, root, false, map[string]ClassState{"chore": {Level: core.L2Branch}})
	seedRuns(t, root, "chore", 15)

	res, err := Recompute(root, Options{Now: testNow, Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Promotions) != 0 || res.ProposalPath != "" {
		t.Fatalf("advisory profile must refuse L3 promotion: %+v", res)
	}
	cs := res.Classes[0]
	if cs.Eligible || !strings.Contains(cs.Reason, "advisory") {
		t.Fatalf("reason must name the advisory refusal, got %+v", cs)
	}
}

func TestRecomputeAdvisoryWithdrawsHeldL3(t *testing.T) {
	root := t.TempDir()
	seedProfile(t, root, false, map[string]ClassState{"chore": {Level: core.L3Automerge}})

	res, err := Recompute(root, Options{Now: testNow, Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Demotions) != 1 || res.Demotions[0].From != core.L3Automerge || res.Demotions[0].To != core.L2Branch {
		t.Fatalf("held L3 under advisory profile must drop to L2, got %+v", res.Demotions)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Level("chore") != core.L2Branch {
		t.Fatalf("withdrawal not persisted: L%d", int(f.Level("chore")))
	}
}

func TestRecomputeDirtyWindowBlocksPromotion(t *testing.T) {
	root := t.TempDir()
	seedProfile(t, root, true, nil)
	// 12 runs but two failed on first verdict: clean rate 10/12 < 0.95.
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("c-%03d", i)
		at := testNow.Add(time.Duration(i-12) * time.Hour)
		pass := i >= 2
		writeChange(t, root, "archive", id, "bugfix", []core.JournalEvent{
			{Time: at, Kind: "verdict", ChangeID: id, Data: map[string]any{"pass": pass, "tamper": false}},
		})
	}
	res, err := Recompute(root, Options{Now: testNow, Reverts: noReverts})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Promotions) != 0 {
		t.Fatalf("clean rate below bar must not promote: %+v", res.Promotions)
	}
	if !strings.Contains(res.Classes[0].Reason, "clean rate") {
		t.Errorf("reason should cite the clean rate, got %q", res.Classes[0].Reason)
	}
}

func TestUnifiedDiffShape(t *testing.T) {
	a := "l1\nl2\nl3\nl4\nl5\nl6\nl7\n"
	b := "l1\nl2\nl3\nl4x\nl5\nl6\nl7\n"
	d := unifiedDiff("f", a, b)
	for _, want := range []string{"--- a/f\n", "+++ b/f\n", "-l4\n", "+l4x\n", " l3\n", " l5\n"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "-l1") || strings.Contains(d, "+l1") {
		t.Errorf("common prefix must be context, not change:\n%s", d)
	}
}

func TestLoadMissingFileDefaults(t *testing.T) {
	f, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if f.Profile.Forge != "unknown" || f.Profile.Enforced {
		t.Fatalf("missing file must default to unknown advisory, got %+v", f.Profile)
	}
	if f.Level("anything") != core.L1Propose {
		t.Fatal("unrecorded classes start at L1")
	}
}

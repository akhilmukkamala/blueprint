package dream

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

func ts(day, hour int) time.Time {
	return time.Date(2026, 7, day, hour, 0, 0, 0, time.UTC)
}

func writeRel(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendLine(t *testing.T, root, rel string, ev core.JournalEvent) {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// addChange scaffolds a change folder (sub is "changes" or "archive") with a
// scenario-carrying change.md and journal events.
func addChange(t *testing.T, root, sub, id, scenario string, events ...core.JournalEvent) {
	t.Helper()
	writeRel(t, root, ".blueprint/"+sub+"/"+id+"/change.md",
		"+++\nscenario = \""+scenario+"\"\n+++\n\n# "+id+"\n")
	for _, ev := range events {
		appendLine(t, root, ".blueprint/"+sub+"/"+id+"/journal.ndjson", ev)
	}
}

func verdictEv(at time.Time, pass bool, fp string) core.JournalEvent {
	return core.JournalEvent{Time: at, Kind: "verdict",
		Data: map[string]any{"pass": pass, "tamper": false, "fingerprint": fp}}
}

func wlAppend(t *testing.T, root string, ev core.JournalEvent) {
	t.Helper()
	if err := worklog.Append(root, ev); err != nil {
		t.Fatal(err)
	}
}

func signalKinds(sigs []Signal) []string {
	var kinds []string
	for _, s := range sigs {
		kinds = append(kinds, s.Kind)
	}
	return kinds
}

func findSignal(t *testing.T, sigs []Signal, kind string) Signal {
	t.Helper()
	for _, s := range sigs {
		if s.Kind == kind {
			return s
		}
	}
	t.Fatalf("no %s signal in %v", kind, signalKinds(sigs))
	return Signal{}
}

// --- no-op -----------------------------------------------------------------

func TestNoSignalIsAHardNoOp(t *testing.T) {
	root := t.TempDir()
	res, err := Run(root, Options{Branch: true, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoSignal {
		t.Fatalf("empty repo must be a no-op, got %+v", res)
	}
	// AC-10: writing NOTHING — not even a .blueprint directory or worklog event.
	if _, err := os.Stat(filepath.Join(root, ".blueprint")); !os.IsNotExist(err) {
		t.Fatalf(".blueprint was created on a no-op run: %v", err)
	}
}

func TestPassingHistoryBelowThresholdsIsNoSignal(t *testing.T) {
	root := t.TempDir()
	addChange(t, root, "changes", "c1", "feature-slice", verdictEv(ts(2, 10), true, "abc"))
	addChange(t, root, "changes", "c2", "feature-slice", verdictEv(ts(3, 10), false, "fp-1"))
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 0 {
		t.Fatalf("expected no signals, got %v", signalKinds(sigs))
	}
}

// --- extraction fixtures ---------------------------------------------------

func TestRepeatedFailureFingerprint(t *testing.T) {
	root := t.TempDir()
	addChange(t, root, "changes", "c1", "s", verdictEv(ts(2, 10), false, "fp-repeat"))
	addChange(t, root, "changes", "c2", "s", verdictEv(ts(3, 10), false, "fp-repeat"))
	addChange(t, root, "archive", "c3", "s", verdictEv(ts(4, 10), false, "fp-repeat"))

	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	s := findSignal(t, sigs, SigRepeatedFailure)
	if s.Count != 3 {
		t.Fatalf("count = %d, want 3", s.Count)
	}
	if fp := s.Detail["fingerprint"]; fp != "fp-repeat" {
		t.Fatalf("fingerprint = %v", fp)
	}
	if len(s.Evidence) != 3 || s.Evidence[2].Source != "archive/c3" {
		t.Fatalf("evidence = %+v", s.Evidence)
	}
}

func TestOverrideCluster(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("c%d", i)
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 9), Kind: "route", ChangeID: id,
			Data: map[string]any{"tier": "light", "type": "feat"}})
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 10), Kind: "override", ChangeID: id,
			Data: map[string]any{"tier": "full", "reason": "touches auth"}})
	}
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	s := findSignal(t, sigs, SigOverrideCluster)
	if s.Count != 3 || s.Detail["type"] != "feat" || s.Detail["direction"] != "up" {
		t.Fatalf("signal = %+v", s)
	}
}

func TestOverrideClusterBelowThreshold(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("c%d", i)
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 9), Kind: "route", ChangeID: id,
			Data: map[string]any{"tier": "light", "type": "feat"}})
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 10), Kind: "override", ChangeID: id,
			Data: map[string]any{"tier": "full", "reason": "r"}})
	}
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 0 {
		t.Fatalf("2 overrides must not cluster, got %v", signalKinds(sigs))
	}
}

func TestBreakerPatternCluster(t *testing.T) {
	root := t.TempDir()
	br := func(at time.Time) core.JournalEvent {
		return core.JournalEvent{Time: at, Kind: "breaker", Data: map[string]any{"pattern": "repeat-error"}}
	}
	addChange(t, root, "changes", "c1", "s", br(ts(2, 1)), br(ts(2, 2)))
	addChange(t, root, "archive", "c2", "s", br(ts(3, 1)))
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	s := findSignal(t, sigs, SigBreakerPattern)
	if s.Count != 3 || s.Detail["pattern"] != "repeat-error" {
		t.Fatalf("signal = %+v", s)
	}
}

func TestTamperEventSignals(t *testing.T) {
	root := t.TempDir()
	addChange(t, root, "changes", "c1", "s",
		core.JournalEvent{Time: ts(5, 1), Kind: "tamper", Data: map[string]any{"detail": "approved.lock drift"}})
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	s := findSignal(t, sigs, SigTamper)
	if s.Count != 1 || s.Evidence[0].ChangeID != "c1" {
		t.Fatalf("signal = %+v", s)
	}
}

func TestRegistryCandidate(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= RegistryCleanRuns; i++ {
		addChange(t, root, "archive", fmt.Sprintf("r%d", i), "deps-bump",
			verdictEv(ts(i, 12), true, ""))
	}
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	s := findSignal(t, sigs, SigRegistryCandidate)
	if s.Count != RegistryCleanRuns || s.Detail["scenario"] != "deps-bump" {
		t.Fatalf("signal = %+v", s)
	}
}

func TestRegistryCandidateStreakBrokenByFailure(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= RegistryCleanRuns; i++ {
		addChange(t, root, "archive", fmt.Sprintf("r%d", i), "deps-bump",
			verdictEv(ts(i, 12), true, ""))
	}
	// A newer unclean run breaks the trailing streak.
	addChange(t, root, "changes", "r-new", "deps-bump", verdictEv(ts(20, 12), false, "fp-x"))
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sigs {
		if s.Kind == SigRegistryCandidate {
			t.Fatalf("broken streak must not surface a candidate: %+v", s)
		}
	}
}

func TestSinceLastDreamBoundary(t *testing.T) {
	root := t.TempDir()
	addChange(t, root, "changes", "c1", "s",
		verdictEv(ts(2, 1), false, "fp-old"), verdictEv(ts(2, 2), false, "fp-old"), verdictEv(ts(2, 3), false, "fp-old"))
	wlAppend(t, root, core.JournalEvent{Time: ts(10, 0), Kind: "dream",
		Data: map[string]any{"date": "2026-07-10"}})

	sigs, since, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	if !since.Equal(ts(10, 0)) {
		t.Fatalf("since = %v", since)
	}
	if len(sigs) != 0 {
		t.Fatalf("pre-dream events must be spent, got %v", signalKinds(sigs))
	}
}

func TestUngatedHumanVerify(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".blueprint/specs/pay/spec.md", `---
id: pay
status: approved
owner: a
reviewed: 2026-07-01
---

# pay

### REQ-pay-001 (ubiquitous)

The checkout page shall look correct on mobile.

verify:
- human: does checkout render correctly on a phone?
`)
	addChange(t, root, "changes", "c1", "s", verdictEv(ts(2, 1), true, ""))

	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	s := findSignal(t, sigs, SigUngatedHuman)
	if !strings.Contains(s.Summary, "REQ-pay-001") {
		t.Fatalf("summary = %q", s.Summary)
	}

	// A journaled human-gate event clears the signal.
	wlAppend(t, root, core.JournalEvent{Time: ts(3, 0), Kind: "human-gate",
		Data: map[string]any{"req": "REQ-pay-001"}})
	sigs, _, err = ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sigs {
		if s.Kind == SigUngatedHuman {
			t.Fatalf("gated human method must not signal: %+v", s)
		}
	}
}

// --- quarantine ------------------------------------------------------------

func TestQuarantinedSignalsRenderAsCommentsNeverPatches(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("c%d", i)
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 9), Kind: "route", ChangeID: id,
			Data: map[string]any{"tier": "light", "type": "feat"}})
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 10), Kind: "override", ChangeID: id,
			Data: map[string]any{"tier": "full", "reason": "seen on a blog", "source": "web"}})
	}
	res, err := Run(root, Options{Branch: false, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.NoSignal || len(res.Items) == 0 {
		t.Fatalf("result = %+v", res)
	}
	it := res.Items[0]
	if !it.Quarantined {
		t.Fatalf("web-derived item must be quarantined: %+v", it)
	}
	if it.Patch != "" || it.PatchPath != "" {
		t.Fatalf("quarantined item must never carry a patch: %+v", it)
	}
	if !strings.Contains(res.Proposal, "<!-- quarantined:") {
		t.Fatalf("proposal must render quarantined items as comments:\n%s", res.Proposal)
	}
	if !strings.Contains(res.Proposal, "[QUARANTINED]") {
		t.Fatalf("proposal must tag quarantined items:\n%s", res.Proposal)
	}
}

func TestTaintedFlagQuarantines(t *testing.T) {
	root := t.TempDir()
	addChange(t, root, "changes", "c1", "s",
		core.JournalEvent{Time: ts(5, 1), Kind: "tamper", Data: map[string]any{"tainted": true}})
	sigs, _, err := ExtractSignals(root)
	if err != nil {
		t.Fatal(err)
	}
	if s := findSignal(t, sigs, SigTamper); !s.Quarantined {
		t.Fatalf("tainted event must quarantine its signal: %+v", s)
	}
}

// --- dry run ---------------------------------------------------------------

func TestDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	addChange(t, root, "changes", "c1", "s",
		verdictEv(ts(2, 1), false, "fp"), verdictEv(ts(2, 2), false, "fp"), verdictEv(ts(2, 3), false, "fp"))
	before, _, err := worklog.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(root, Options{Branch: false, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Proposal == "" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, ".blueprint", "dream")); !os.IsNotExist(err) {
		t.Fatal("dry run must not write the dream dir")
	}
	after, _, err := worklog.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("dry run must not journal a dream event")
	}
	// Stable IDs + absolute dates in the rendered proposal.
	if !strings.Contains(res.Proposal, "D-2026-07-26-1") {
		t.Fatalf("proposal lacks stable item IDs:\n%s", res.Proposal)
	}
}

// --- git branch output -----------------------------------------------------

func gitDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitDo(t, root, "init", "-q", "-b", "main")
	writeRel(t, root, "README.md", "fixture\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	return root
}

func overrideClusterFixture(t *testing.T, root string) {
	t.Helper()
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("c%d", i)
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 9), Kind: "route", ChangeID: id,
			Data: map[string]any{"tier": "light", "type": "feat"}})
		wlAppend(t, root, core.JournalEvent{Time: ts(i, 10), Kind: "override", ChangeID: id,
			Data: map[string]any{"tier": "full", "reason": "touches auth"}})
	}
}

func TestBranchProposalPatchShapeAndGitApply(t *testing.T) {
	root := gitRepo(t)
	overrideClusterFixture(t, root)
	// Registry candidate too, so the run emits a registry.toml append patch.
	writeRel(t, root, ".blueprint/registry.toml", "[[class]]\nname = \"docs-only\"\ntype = \"docs\"\nglobs = [\"**/*.md\"]\nmax_loc = 200\nchecks = []\n")
	gitDo(t, root, "add", ".blueprint/registry.toml")
	gitDo(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "registry")
	for i := 1; i <= RegistryCleanRuns; i++ {
		addChange(t, root, "archive", fmt.Sprintf("r%d", i), "deps-bump", verdictEv(ts(i, 12), true, ""))
	}

	res, err := Run(root, Options{Branch: true, Now: func() time.Time { return ts(26, 12) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "agent/dream/2026-07-26" {
		t.Fatalf("branch = %q", res.Branch)
	}
	gitDo(t, root, "rev-parse", "--verify", "refs/heads/"+res.Branch)

	// Current checkout untouched; proposal lives on the branch only.
	if _, err := os.Stat(filepath.Join(root, ".blueprint", "dream")); !os.IsNotExist(err) {
		t.Fatal("dream output must live on the branch, not in the working tree")
	}
	proposal := gitDo(t, root, "show", res.Branch+":"+res.ProposalPath)
	for _, want := range []string{"D-2026-07-26-1", "agent/dream/2026-07-26", "worklog 2026-07-0", "Human merge only"} {
		if !strings.Contains(proposal, want) {
			t.Fatalf("proposal missing %q:\n%s", want, proposal)
		}
	}

	// The commit carries the machine-generation trailer.
	msg := gitDo(t, root, "log", "-1", "--format=%B", res.Branch)
	if !strings.Contains(msg, "Co-Authored-By: blueprint dream") {
		t.Fatalf("commit message lacks trailer:\n%s", msg)
	}

	// Every emitted patch is ≤40 changed lines and git-apply-able at the fork
	// point; [user]-tier files were not touched directly.
	if len(res.PatchPaths) == 0 {
		t.Fatal("expected patch files for user-tier proposals")
	}
	for _, pp := range res.PatchPaths {
		patch := gitDo(t, root, "show", res.Branch+":"+pp)
		if n := changedLines(patch); n > 40 {
			t.Fatalf("patch %s has %d changed lines (cap 40)", pp, n)
		}
		pf := filepath.Join(t.TempDir(), "p.patch")
		if err := os.WriteFile(pf, []byte(patch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitDo(t, root, "apply", "--check", pf)
		gitDo(t, root, "apply", pf)
	}
	// The registry append patch produced the commented candidate block.
	reg, err := os.ReadFile(filepath.Join(root, ".blueprint", "registry.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reg), "# name = \"deps-bump\"") {
		t.Fatalf("registry patch did not append the commented candidate:\n%s", reg)
	}

	// The run journaled a dream worklog event.
	evs, _, err := worklog.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Kind != "dream" || last.Data["branch"] != res.Branch {
		t.Fatalf("last worklog event = %+v", last)
	}
}

func TestRefusesExistingDreamBranch(t *testing.T) {
	root := gitRepo(t)
	overrideClusterFixture(t, root)
	gitDo(t, root, "branch", "agent/dream/2026-07-26")

	_, err := Run(root, Options{Branch: true, Now: func() time.Time { return ts(26, 12) }})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want refusal on existing branch, got %v", err)
	}
}

func TestNonGitRepoErrorsWithRemediation(t *testing.T) {
	root := t.TempDir()
	overrideClusterFixture(t, root)
	_, err := Run(root, Options{Branch: true, Now: func() time.Time { return ts(26, 12) }})
	if err == nil || !strings.Contains(err.Error(), "--branch=false") {
		t.Fatalf("want git remediation error, got %v", err)
	}
}

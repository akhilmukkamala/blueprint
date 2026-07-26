package loop

import (
	"strings"
	"testing"

	"blueprint/internal/core"
)

var defaultBreaker = core.Breaker{
	RepeatActionN: 3, RepeatErrorN: 3, NoDiffDeltaN: 3, OscillationN: 2, MonologueTokens: 4000,
}

func errIter(sec int, fingerprint string) core.JournalEvent {
	ev := iter(sec, sec, "run tests", 1)
	ev.Data["error_fingerprint"] = fingerprint
	return ev
}

func TestEvaluateBreaker(t *testing.T) {
	cases := []struct {
		name    string
		events  []core.JournalEvent
		breaker core.Breaker
		pattern string // "" = no trip
	}{
		{
			name:    "empty journal never trips",
			breaker: defaultBreaker,
		},
		{
			name: "repeat identical action trips",
			events: []core.JournalEvent{
				iter(0, 1, "edit src/a.go", 5),
				iter(1, 2, "run go test", 1),
				iter(2, 3, "run go test", 1),
				iter(3, 4, "run go test", 2),
			},
			breaker: defaultBreaker,
			pattern: PatternRepeatAction,
		},
		{
			name: "varied actions do not trip",
			events: []core.JournalEvent{
				iter(0, 1, "edit a", 5), iter(1, 2, "test", 1), iter(2, 3, "edit b", 3),
			},
			breaker: defaultBreaker,
		},
		{
			name: "repeat error fingerprint trips",
			events: []core.JournalEvent{
				errIter(0, "TestFoo panics"),
				errIter(1, "TestFoo panics"),
				errIter(2, "TestFoo panics"),
			},
			breaker: core.Breaker{RepeatErrorN: 3},
			pattern: PatternRepeatError,
		},
		{
			name: "error streak broken by different fingerprint",
			events: []core.JournalEvent{
				errIter(0, "TestFoo panics"), errIter(1, "TestBar fails"), errIter(2, "TestFoo panics"),
			},
			breaker: core.Breaker{RepeatErrorN: 3},
		},
		{
			name: "failed verdict fingerprints count",
			events: []core.JournalEvent{
				{Time: at(0), Kind: "verdict", Data: map[string]any{"pass": false, "fingerprint": "abc"}},
				{Time: at(1), Kind: "verdict", Data: map[string]any{"pass": false, "fingerprint": "abc"}},
			},
			breaker: core.Breaker{RepeatErrorN: 2},
			pattern: PatternRepeatError,
		},
		{
			name: "passing verdicts are ignored",
			events: []core.JournalEvent{
				{Time: at(0), Kind: "verdict", Data: map[string]any{"pass": true, "fingerprint": "abc"}},
				{Time: at(1), Kind: "verdict", Data: map[string]any{"pass": true, "fingerprint": "abc"}},
			},
			breaker: core.Breaker{RepeatErrorN: 2},
		},
		{
			name: "no diff delta trips",
			events: []core.JournalEvent{
				iter(0, 1, "a", 5), iter(1, 2, "b", 0), iter(2, 3, "c", 0), iter(3, 4, "d", 0),
			},
			breaker: core.Breaker{NoDiffDeltaN: 3},
			pattern: PatternNoDiffDelta,
		},
		{
			name: "missing diff delta is unknown not zero",
			events: []core.JournalEvent{
				{Time: at(0), Kind: KindIteration, Data: map[string]any{"action": "a"}},
				{Time: at(1), Kind: KindIteration, Data: map[string]any{"action": "b"}},
				{Time: at(2), Kind: KindIteration, Data: map[string]any{"action": "c"}},
			},
			breaker: core.Breaker{NoDiffDeltaN: 3},
		},
		{
			name: "A-B-A oscillation trips",
			events: []core.JournalEvent{
				iter(0, 1, "e1", 3, "src/a.go"),
				iter(1, 2, "e2", 3, "src/b.go"),
				iter(2, 3, "e3", 3, "src/a.go"),
				iter(3, 4, "e4", 3, "src/b.go"),
				iter(4, 5, "e5", 3, "src/a.go"),
			},
			breaker: core.Breaker{OscillationN: 2},
			pattern: PatternOscillation,
		},
		{
			name: "forward progress on files does not trip oscillation",
			events: []core.JournalEvent{
				iter(0, 1, "e1", 3, "src/a.go"),
				iter(1, 2, "e2", 3, "src/b.go"),
				iter(2, 3, "e3", 3, "src/c.go"),
			},
			breaker: core.Breaker{OscillationN: 2},
		},
		{
			name: "monologue token cap trips",
			events: []core.JournalEvent{func() core.JournalEvent {
				ev := iter(0, 1, "think", 1)
				ev.Data["monologue_tokens"] = 5000
				return ev
			}()},
			breaker: core.Breaker{MonologueTokens: 4000},
			pattern: PatternMonologue,
		},
		{
			name: "zero thresholds disable all patterns",
			events: []core.JournalEvent{
				iter(0, 1, "x", 0), iter(1, 2, "x", 0), iter(2, 3, "x", 0),
			},
			breaker: core.Breaker{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := EvaluateBreaker(tc.events, tc.breaker)
			if v.Trip != (tc.pattern != "") || v.Pattern != tc.pattern {
				t.Errorf("verdict = %+v, want pattern %q", v, tc.pattern)
			}
			if v.Trip && len(v.Evidence) == 0 {
				t.Error("tripped verdict must carry evidence")
			}
		})
	}
}

func TestEvaluateCaps(t *testing.T) {
	contract := core.LoopContract{MaxIterations: 5, MaxMinutes: 90, MaxUSD: 10}
	cases := []struct {
		name    string
		c       Consumption
		pattern string
	}{
		{"under all caps", Consumption{Iterations: 2, Minutes: 10, USD: 1}, ""},
		{"iteration cap", Consumption{Iterations: 5}, PatternCapIteration},
		{"minutes cap", Consumption{Minutes: 90}, PatternCapMinutes},
		{"usd cap", Consumption{USD: 10}, PatternCapUSD},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := EvaluateCaps(tc.c, contract)
			if v.Pattern != tc.pattern {
				t.Errorf("pattern = %q, want %q (%+v)", v.Pattern, tc.pattern, v)
			}
		})
	}
	if v := EvaluateCaps(Consumption{Iterations: 1000, USD: 1e6}, core.LoopContract{}); v.Trip {
		t.Errorf("zero caps must mean unlimited, got %+v", v)
	}
}

func TestEnforceParksOnTrip(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := AppendEvent(root, "c1", iter(i, i+1, "run go test", 0)); err != nil {
			t.Fatal(err)
		}
	}
	contract := core.LoopContract{Breaker: defaultBreaker}
	v, err := Enforce(root, "c1", contract)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Trip || v.Pattern != PatternRepeatAction {
		t.Fatalf("verdict = %+v, want repeat-action trip", v)
	}
	s, _ := LoadState(root, "c1")
	if s.Phase != PhaseParked {
		t.Errorf("Enforce must park on trip, phase = %q", s.Phase)
	}
	if !strings.Contains(s.Summary, PatternRepeatAction) {
		t.Errorf("park summary must name the pattern: %q", s.Summary)
	}
}

func TestEnforceCleanJournalStaysUnparked(t *testing.T) {
	root := t.TempDir()
	if err := AppendEvent(root, "c1", iter(0, 1, "edit", 5)); err != nil {
		t.Fatal(err)
	}
	v, err := Enforce(root, "c1", core.LoopContract{Breaker: defaultBreaker, MaxIterations: 10})
	if err != nil {
		t.Fatal(err)
	}
	if v.Trip {
		t.Fatalf("unexpected trip: %+v", v)
	}
	s, _ := LoadState(root, "c1")
	if s.Phase == PhaseParked {
		t.Error("clean journal must not park")
	}
}

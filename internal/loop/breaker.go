package loop

import (
	"fmt"
	"sort"
	"strings"

	"blueprint/internal/core"
)

// Breaker pattern names (the OpenHands five, DESIGN §6). A threshold of zero
// or below disables that pattern — the contract owns the tuning.
const (
	PatternRepeatAction = "repeat-action"
	PatternRepeatError  = "repeat-error"
	PatternNoDiffDelta  = "no-diff-delta"
	PatternOscillation  = "oscillation"
	PatternMonologue    = "monologue"
	PatternCapIteration = "cap-iterations"
	PatternCapMinutes   = "cap-minutes"
	PatternCapUSD       = "cap-usd"
)

// BreakerVerdict is the result of evaluating the journal against a breaker
// config: whether to trip, which pattern fired first, and human-readable
// evidence (the events that matched).
type BreakerVerdict struct {
	Trip     bool     `json:"trip"`
	Pattern  string   `json:"pattern,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// EvaluateBreaker runs all five no-progress patterns over the journal and
// returns the first that trips (patterns are checked in the DESIGN §6 order:
// repeat action, repeat error, no diff delta, oscillation, monologue). Pure
// function of the journal — no clock, no filesystem.
func EvaluateBreaker(events []core.JournalEvent, b core.Breaker) BreakerVerdict {
	iters := iterationEvents(events)
	if v := repeatAction(iters, b.RepeatActionN); v.Trip {
		return v
	}
	if v := repeatError(events, b.RepeatErrorN); v.Trip {
		return v
	}
	if v := noDiffDelta(iters, b.NoDiffDeltaN); v.Trip {
		return v
	}
	if v := oscillation(iters, b.OscillationN); v.Trip {
		return v
	}
	if v := monologue(iters, b.MonologueTokens); v.Trip {
		return v
	}
	return BreakerVerdict{}
}

func iterationEvents(events []core.JournalEvent) []core.JournalEvent {
	var out []core.JournalEvent
	for _, ev := range events {
		if ev.Kind == KindIteration {
			out = append(out, ev)
		}
	}
	return out
}

// repeatAction trips when the trailing n iteration events carry the same
// non-empty action string.
func repeatAction(iters []core.JournalEvent, n int) BreakerVerdict {
	if n <= 0 || len(iters) < n {
		return BreakerVerdict{}
	}
	tail := iters[len(iters)-n:]
	action := dataString(tail[0], "action")
	if action == "" {
		return BreakerVerdict{}
	}
	for _, ev := range tail[1:] {
		if dataString(ev, "action") != action {
			return BreakerVerdict{}
		}
	}
	return BreakerVerdict{
		Trip:    true,
		Pattern: PatternRepeatAction,
		Evidence: []string{fmt.Sprintf("last %d iterations repeated the identical action %q (%s .. %s)",
			n, action, stamp(tail[0]), stamp(tail[len(tail)-1]))},
	}
}

// repeatError trips when the trailing n error fingerprints are identical.
// Fingerprints come from iteration events (error_fingerprint) and verdict
// events (fingerprint, when pass=false).
func repeatError(events []core.JournalEvent, n int) BreakerVerdict {
	if n <= 0 {
		return BreakerVerdict{}
	}
	type fp struct {
		v  string
		ev core.JournalEvent
	}
	var fps []fp
	for _, ev := range events {
		switch ev.Kind {
		case KindIteration:
			if f := dataString(ev, "error_fingerprint"); f != "" {
				fps = append(fps, fp{f, ev})
			}
		case "verdict":
			if pass, ok := ev.Data["pass"].(bool); ok && pass {
				continue
			}
			if f := dataString(ev, "fingerprint"); f != "" {
				fps = append(fps, fp{f, ev})
			}
		}
	}
	if len(fps) < n {
		return BreakerVerdict{}
	}
	tail := fps[len(fps)-n:]
	for _, f := range tail[1:] {
		if f.v != tail[0].v {
			return BreakerVerdict{}
		}
	}
	return BreakerVerdict{
		Trip:    true,
		Pattern: PatternRepeatError,
		Evidence: []string{fmt.Sprintf("last %d failures share error fingerprint %q (%s .. %s)",
			n, tail[0].v, stamp(tail[0].ev), stamp(tail[n-1].ev))},
	}
}

// noDiffDelta trips when the trailing n iterations each report a zero diff
// delta (no lines changed — motion without progress).
func noDiffDelta(iters []core.JournalEvent, n int) BreakerVerdict {
	if n <= 0 || len(iters) < n {
		return BreakerVerdict{}
	}
	tail := iters[len(iters)-n:]
	for _, ev := range tail {
		if _, present := ev.Data["diff_delta"]; !present {
			return BreakerVerdict{} // unreported delta is unknown, not zero
		}
		if dataFloat(ev, "diff_delta") != 0 {
			return BreakerVerdict{}
		}
	}
	return BreakerVerdict{
		Trip:    true,
		Pattern: PatternNoDiffDelta,
		Evidence: []string{fmt.Sprintf("last %d iterations produced no diff delta (%s .. %s)",
			n, stamp(tail[0]), stamp(tail[len(tail)-1]))},
	}
}

// oscillation trips on A->B->A file-set flips: an oscillation is an
// iteration whose touched-file set differs from the previous one but equals
// the one before that. n such trailing consecutive flips trip the breaker.
func oscillation(iters []core.JournalEvent, n int) BreakerVerdict {
	if n <= 0 || len(iters) < 3 {
		return BreakerVerdict{}
	}
	sigs := make([]string, len(iters))
	for i, ev := range iters {
		sigs[i] = fileSignature(ev)
	}
	flips := 0
	var evidence []string
	for i := len(sigs) - 1; i >= 2; i-- {
		if sigs[i] != "" && sigs[i] != sigs[i-1] && sigs[i] == sigs[i-2] {
			flips++
			evidence = append(evidence, fmt.Sprintf("iteration at %s returned to file set [%s] after touching [%s]",
				stamp(iters[i]), sigs[i], sigs[i-1]))
			if flips >= n {
				return BreakerVerdict{Trip: true, Pattern: PatternOscillation, Evidence: evidence}
			}
		} else {
			break // flips must be consecutive at the tail
		}
	}
	return BreakerVerdict{}
}

// monologue trips when the newest iteration's monologue token count reaches
// the cap — the model is talking instead of acting.
func monologue(iters []core.JournalEvent, cap int) BreakerVerdict {
	if cap <= 0 || len(iters) == 0 {
		return BreakerVerdict{}
	}
	last := iters[len(iters)-1]
	tokens := dataFloat(last, "monologue_tokens")
	if tokens < float64(cap) {
		return BreakerVerdict{}
	}
	return BreakerVerdict{
		Trip:    true,
		Pattern: PatternMonologue,
		Evidence: []string{fmt.Sprintf("iteration at %s produced %.0f monologue tokens (cap %d)",
			stamp(last), tokens, cap)},
	}
}

// EvaluateCaps checks derived consumption against the contract caps. Zero
// caps are unlimited. Returned as a BreakerVerdict so trip handling (park,
// summarize) is uniform for patterns and caps.
func EvaluateCaps(c Consumption, contract core.LoopContract) BreakerVerdict {
	if contract.MaxIterations > 0 && c.Iterations >= contract.MaxIterations {
		return BreakerVerdict{Trip: true, Pattern: PatternCapIteration,
			Evidence: []string{fmt.Sprintf("%d iterations consumed of max %d", c.Iterations, contract.MaxIterations)}}
	}
	if contract.MaxMinutes > 0 && c.Minutes >= float64(contract.MaxMinutes) {
		return BreakerVerdict{Trip: true, Pattern: PatternCapMinutes,
			Evidence: []string{fmt.Sprintf("%.1f minutes elapsed of max %d (journal timestamps)", c.Minutes, contract.MaxMinutes)}}
	}
	if contract.MaxUSD > 0 && c.USD >= contract.MaxUSD {
		return BreakerVerdict{Trip: true, Pattern: PatternCapUSD,
			Evidence: []string{fmt.Sprintf("$%.2f consumed of max $%.2f", c.USD, contract.MaxUSD)}}
	}
	return BreakerVerdict{}
}

// Evaluate combines breaker patterns and caps over the current journal.
// Breaker patterns are checked first: a loop that is both stuck and over
// budget is parked for being stuck — the more actionable diagnosis.
func Evaluate(events []core.JournalEvent, contract core.LoopContract) BreakerVerdict {
	if v := EvaluateBreaker(events, contract.Breaker); v.Trip {
		return v
	}
	return EvaluateCaps(Consumed(events), contract)
}

// Enforce evaluates the journal and, on a trip, parks the loop with the
// verdict as summary (never a silent retry). Returns the verdict either way.
func Enforce(repoRoot, id string, contract core.LoopContract) (BreakerVerdict, error) {
	events, err := ReadJournal(repoRoot, id)
	if err != nil {
		return BreakerVerdict{}, err
	}
	v := Evaluate(events, contract)
	if !v.Trip {
		return v, nil
	}
	summary := fmt.Sprintf("breaker %s: %s", v.Pattern, strings.Join(v.Evidence, "; "))
	if err := Park(repoRoot, id, v.Pattern, summary); err != nil {
		return v, err
	}
	return v, nil
}

func fileSignature(ev core.JournalEvent) string {
	raw, ok := ev.Data["files"]
	if !ok {
		return ""
	}
	var files []string
	switch v := raw.(type) {
	case []string:
		files = append(files, v...)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				files = append(files, s)
			}
		}
	}
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	return strings.Join(files, ",")
}

func stamp(ev core.JournalEvent) string {
	return ev.Time.UTC().Format("2006-01-02T15:04:05Z")
}

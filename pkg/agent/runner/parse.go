// parse.go — exported parse helpers for testing and offline analysis.
//
// These functions parse NDJSON bytes from each CLI driver into a slice of
// RunEvents. maxTurns is the run's RunOptions.MaxTurns turn cap; like Run, the
// helpers refuse a non-positive cap (#904 FR-004, no hidden default) — they
// return a single fatal error event instead of parsing uncapped. They are the same logic used by the streaming drivers, exposed
// for unit tests that use recorded fixtures (TDD #8, FR-5.2, FR-5.6).
//
// Tests MUST use recorded fixtures (no real CLI invocations) per CLAUDE.md
// CI-authority rule.

package runner

import (
	"bytes"
	"context"
	"time"
)

// ParseClaudeStreamJSON parses the full output of `claude --output-format stream-json`
// from a byte slice and returns the resulting RunEvents.
// This is the fixture-based parse path used by tests.
func ParseClaudeStreamJSON(data []byte, runID string, maxTurns int) []RunEvent {
	if ev, bad := invalidMaxTurnsEvent(runID, maxTurns); bad {
		return []RunEvent{ev}
	}
	d := NewClaudeDriver(nil)
	var events []RunEvent
	turnCount := 0

	ctx := context.Background()
	out := make(chan RunEvent, 256)
	streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return d.parseLine(raw, runID, &turnCount, maxTurns)
	}, out)
	close(out)
	for ev := range out {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		events = append(events, ev)
	}
	return events
}

// ParseCodexStreamJSON parses the full output of `codex exec --json`
// from a byte slice and returns the resulting RunEvents.
func ParseCodexStreamJSON(data []byte, runID string, maxTurns int) []RunEvent {
	if ev, bad := invalidMaxTurnsEvent(runID, maxTurns); bad {
		return []RunEvent{ev}
	}
	d := NewCodexDriver(nil)
	var events []RunEvent
	turnCount := 0

	ctx := context.Background()
	out := make(chan RunEvent, 256)
	emittedFatal := streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return d.parseLine(raw, runID, &turnCount, maxTurns)
	}, out)
	close(out)
	for ev := range out {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		events = append(events, ev)
	}
	// M4: turn.completed no longer emits a terminal End (it fires once per turn).
	// The single End is synthesized when the codex stream drains cleanly — mirror
	// the live driver's behavior so the offline parse path produces exactly one
	// End at true completion.
	if !emittedFatal {
		events = append(events, RunEvent{Kind: EventKindEnd, RunID: runID, Timestamp: time.Now().UTC()})
	}
	return events
}

// ParseOpencodeStreamJSON parses the full output of `opencode run --format json`
// from a byte slice and returns the resulting RunEvents.
func ParseOpencodeStreamJSON(data []byte, runID string, maxTurns int) []RunEvent {
	if ev, bad := invalidMaxTurnsEvent(runID, maxTurns); bad {
		return []RunEvent{ev}
	}
	d := NewOpencodeDriver(nil)
	var events []RunEvent
	turnCount := 0

	ctx := context.Background()
	out := make(chan RunEvent, 256)
	streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return d.parseLine(raw, runID, &turnCount, maxTurns)
	}, out)
	close(out)
	for ev := range out {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		events = append(events, ev)
	}
	return events
}

// invalidMaxTurnsEvent reports a non-positive turn cap as one fatal error event
// wrapping ErrMaxTurnsRequired, mirroring Run's refusal for the offline path.
func invalidMaxTurnsEvent(runID string, maxTurns int) (RunEvent, bool) {
	if err := validateMaxTurns("parse", maxTurns); err != nil {
		return RunEvent{
			Kind:      EventKindError,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
			Err:       &ErrorEvent{Message: err.Error(), Fatal: true},
		}, true
	}
	return RunEvent{}, false
}

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
	// Drain concurrently: the parser now delivers a detected stream-read
	// failure with priority over a full channel, so this helper must keep
	// consuming while parsing — a synchronous drain-after-return shape would
	// deadlock that delivery against this fixed buffer.
	go func() {
		streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
			return d.parseLine(raw, runID, &turnCount, maxTurns)
		}, out)
		close(out)
	}()
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
	// Drain concurrently (see ParseClaudeStreamJSON): fatal delivery needs a
	// live consumer while parsing runs.
	emittedFatalCh := make(chan bool, 1)
	go func() {
		emittedFatalCh <- streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
			return d.parseLine(raw, runID, &turnCount, maxTurns)
		}, out)
		close(out)
	}()
	for ev := range out {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		events = append(events, ev)
	}
	emittedFatal := <-emittedFatalCh
	// M4: turn.completed no longer emits a terminal End (it fires once per turn).
	// The single End is synthesized when the codex stream drains cleanly — mirror
	// the live driver's behavior so the offline parse path produces exactly one
	// End at true completion. A detected stream-read failure is fatal and must
	// never be masked by a success End.
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
	// Drain concurrently (see ParseClaudeStreamJSON): fatal delivery needs a
	// live consumer while parsing runs — a full buffer must not deadlock the
	// delivery of a detected stream-read failure.
	emittedFatalCh := make(chan bool, 1)
	go func() {
		emittedFatalCh <- streamParserMulti(ctx, bytes.NewReader(data), runID, func(raw []byte) ([]RunEvent, bool) {
			return d.parseLine(raw, runID, &turnCount, maxTurns)
		}, out)
		close(out)
	}()
	sawEnd := false
	for ev := range out {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		if ev.Kind == EventKindEnd {
			sawEnd = true
		}
		events = append(events, ev)
	}
	emittedFatal := <-emittedFatalCh
	// The ≥ 2.x vocabulary has no session.complete: when the recorded stream
	// drains cleanly with no inline End and no fatal error, synthesize the
	// single terminal End here — the offline mirror of the live driver's
	// run-end End (same pattern as the codex M4 synthesis). An inline End
	// (≥ 0.1.0 session.complete) or a fatal error suppresses it.
	if !emittedFatal && !sawEnd {
		events = append(events, RunEvent{Kind: EventKindEnd, RunID: runID, Timestamp: time.Now().UTC()})
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

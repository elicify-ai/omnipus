// parse.go — exported parse helpers for testing and offline analysis.
//
// These functions parse NDJSON bytes from each CLI driver into a slice of
// RunEvents. They are the same logic used by the streaming drivers, exposed
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
func ParseClaudeStreamJSON(data []byte, runID string) []RunEvent {
	d := NewClaudeDriver(nil)
	var events []RunEvent
	turnCount := 0

	ctx := context.Background()
	out := make(chan RunEvent, 256)
	streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return d.parseLine(raw, runID, &turnCount, defaultMaxTurns)
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
func ParseCodexStreamJSON(data []byte, runID string) []RunEvent {
	d := NewCodexDriver(nil)
	var events []RunEvent
	turnCount := 0

	ctx := context.Background()
	out := make(chan RunEvent, 256)
	streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return d.parseLine(raw, runID, &turnCount, defaultMaxTurns)
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

// ParseOpencodeStreamJSON parses the full output of `opencode run --format json`
// from a byte slice and returns the resulting RunEvents.
func ParseOpencodeStreamJSON(data []byte, runID string) []RunEvent {
	d := NewOpencodeDriver(nil)
	var events []RunEvent
	turnCount := 0

	ctx := context.Background()
	out := make(chan RunEvent, 256)
	streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return d.parseLine(raw, runID, &turnCount, defaultMaxTurns)
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

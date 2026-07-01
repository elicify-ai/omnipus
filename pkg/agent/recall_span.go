// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"sync/atomic"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// recallSpanDropCounters tracks recall_span_dropped_total{reason} (FR-018).
// Each key is a reason string ("replaced" | "pressure" | "aged" | "other");
// values are *atomic.Int64 so the map is written once at init and read-only
// afterward — no lock required on the hot path.
var recallSpanDropCounters = map[string]*atomic.Int64{
	"replaced": new(atomic.Int64),
	"pressure": new(atomic.Int64),
	"aged":     new(atomic.Int64),
	"other":    new(atomic.Int64),
}

// RecallSpanDropCount returns the total count for a given reason (for tests
// and observability). Unknown reasons return 0.
func RecallSpanDropCount(reason string) int64 {
	if c, ok := recallSpanDropCounters[reason]; ok {
		return c.Load()
	}
	return 0
}

// RecallSpan is the transient, in-memory recall span (FR-019): whole
// conversation Turns paged back by the recall_conversation tool, re-injected
// into the next context assembly as native provider messages with rewritten
// tool_call_ids. It is NEVER persisted to context.jsonl (the archive stays the
// single source of truth) and is dropped-first under budget pressure so it can
// never cause eviction of real window Turns.
//
// Construct exclusively via newRecallSpan so Tokens is always consistent with
// Msgs. Read the span via Messages() and the Tokens field.
type RecallSpan struct {
	// FromTurn and ToTurn record the 1-based Turn ordinals (as grouped by
	// parseTurnBoundaries over the full archive) this span covers. They are
	// written at construction time for the demarcation marker
	// "Recalled earlier turns {FromTurn}-{ToTurn}" and for observability.
	FromTurn int
	ToTurn   int

	// Msgs are the reconstructed, provider-valid messages (marker + whole
	// Turns with rewritten recall_* tool_call_ids), ready to splice between
	// the breadcrumb and the sliding window.
	Msgs []providers.Message

	// Tokens is the estimated token cost of Msgs (the recallResultTokens term
	// of the FR-009 fit invariant), computed from Msgs at construction time by
	// newRecallSpan and guaranteed to equal Σ estimateMessageTokens(Msgs).
	Tokens int
}

// newRecallSpan constructs a RecallSpan, computing Tokens from msgs so the
// field is always consistent with Msgs (M7). fromTurn and toTurn are 1-based
// Turn ordinals. Panics if fromTurn > toTurn or msgs is empty — callers must
// guarantee non-empty selection and valid range before calling.
func newRecallSpan(fromTurn, toTurn int, msgs []providers.Message) *RecallSpan {
	if fromTurn > toTurn {
		panic(fmt.Sprintf("newRecallSpan: fromTurn %d > toTurn %d", fromTurn, toTurn))
	}
	if len(msgs) == 0 {
		panic("newRecallSpan: msgs must not be empty")
	}
	tokens := 0
	for _, m := range msgs {
		tokens += estimateMessageTokens(m)
	}
	return &RecallSpan{
		FromTurn: fromTurn,
		ToTurn:   toTurn,
		Msgs:     msgs,
		Tokens:   tokens,
	}
}

// Messages returns the span's reconstructed provider messages for assembly.
// Returns nil for a nil span so callers can inline `span.Messages()` safely.
func (s *RecallSpan) Messages() []providers.Message {
	if s == nil {
		return nil
	}
	return s.Msgs
}

// setRecallSpan stores (replacing any prior) the active recall span for a
// session. Called by recall_conversation (FR-019 lifecycle: replaced on next
// recall).
func (al *AgentLoop) setRecallSpan(sessionKey string, span *RecallSpan) {
	if span == nil {
		al.recallSpans.Delete(sessionKey)
		return
	}
	al.recallSpans.Store(sessionKey, span)
}

// activeRecallSpan returns the current recall span for a session, or nil.
func (al *AgentLoop) activeRecallSpan(sessionKey string) *RecallSpan {
	v, ok := al.recallSpans.Load(sessionKey)
	if !ok {
		return nil
	}
	span, _ := v.(*RecallSpan)
	return span
}

// dropRecallSpan clears a session's recall span (FR-019: dropped-first under
// budget pressure, or replaced/aged). reason is for the
// recall_span_dropped_total{reason} counter (FR-018).
func (al *AgentLoop) dropRecallSpan(sessionKey, reason string) {
	if _, ok := al.recallSpans.LoadAndDelete(sessionKey); ok {
		recordRecallSpanDropped(reason)
	}
}

// recordRecallSpanDropped increments recall_span_dropped_total{reason} (FR-018).
// Known reasons: "replaced" (next recall replaced it), "pressure" (dropped
// first under budget), "aged" (future turn-age expiry, placeholder).
// Unknown reasons are counted under the dedicated "other" bucket so they are
// observable without misattributing them to a known cause.
func recordRecallSpanDropped(reason string) {
	if c, ok := recallSpanDropCounters[reason]; ok {
		c.Add(1)
		return
	}
	// Unknown reason — count under "other" rather than silently drop or
	// misattribute to a known bucket.
	recallSpanDropCounters["other"].Add(1)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync/atomic"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// recallSpanDropCounters tracks recall_span_dropped_total{reason} (FR-018).
// Each key is a reason string ("replaced" | "pressure" | "aged"); values are
// *atomic.Int64 so the map is written once at init and read-only afterward —
// no lock required on the hot path.
var recallSpanDropCounters = map[string]*atomic.Int64{
	"replaced": new(atomic.Int64),
	"pressure": new(atomic.Int64),
	"aged":     new(atomic.Int64),
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
// SCAFFOLD: the field set and the Messages() body are the minimum needed for
// windowTrim (eviction) and BuildMessages (assembly) to compile against a
// stable contract. The recall_conversation agent fills in the real construction
// (BM25/turn_range/time selection, whole-Turn grouping, id rewriting, bounds).
type RecallSpan struct {
	// FromTurn/ToTurn are the archive Turn indices this span covers (for the
	// demarcation marker "Recalled earlier turns {FromTurn}-{ToTurn}").
	FromTurn int
	ToTurn   int

	// Msgs are the reconstructed, provider-valid messages (marker + whole
	// Turns with rewritten recall_* tool_call_ids), ready to splice between
	// the breadcrumb and the sliding window.
	Msgs []providers.Message

	// Tokens is the estimated token cost of Msgs (the recallResultTokens term
	// of the FR-009 fit invariant), measured once at build time.
	Tokens int
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
// Unknown reasons are accepted and counted under the "replaced" bucket to
// avoid panics from future callers — the same convention as log/slog's
// catch-all handling.
func recordRecallSpanDropped(reason string) {
	if c, ok := recallSpanDropCounters[reason]; ok {
		c.Add(1)
		return
	}
	// Unknown reason — count under "replaced" rather than silently drop.
	recallSpanDropCounters["replaced"].Add(1)
}

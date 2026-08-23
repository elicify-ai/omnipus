// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// recall_injection.go — ADR-066 D5.4 (FR-041…FR-044): a recall made
// mid-turn is spliced into the turn's in-memory message slice at the
// tool-result site, so the provider's VERY NEXT request carries the
// recalled text — or the tool result says it did not fit.
//
// Before this, the span was read only at assembly/trim sites while every
// mid-turn request was built from the in-memory slice; a recall reached the
// model as a receipt string and nothing else (operator report 2026-08-22).
//
// The three pieces, in call order inside loop.go::runTurn's tool loop:
//
//  1. decideRecallInjection — BEFORE the result enters the choke point, so
//     the text the archive, transcript and events carry is the truthful
//     outcome: the budget-first fit check (the same inequality windowTrim
//     uses, against the one budget B) either keeps the tool's "now in your
//     context" receipt or rewrites it to the non-fit message and drops the
//     span.
//  2. admitToolResult + the append of the result message (unchanged).
//  3. spliceRecallSpan — AFTER the append: removes a span replaced in this
//     same turn (E20), then splices the new span at the BuildMessages
//     position (after the pinned core, before the window), sanitised with
//     the window exactly as BuildMessages would.
//
// assembleMessages records whatever span a from-scratch assembly included
// (recordAssembledRecallSpan), so the tool-result site never doubles a span
// that is already in the slice (FR-043, identity comparison).

// recallConversationToolName is the registered name of the recall tool
// (pkg/agent/recall_conversation.go). The tool loop keys D5.4 on it.
const recallConversationToolName = "recall_conversation"

// recallSpanFits is the D5.4 fit check: the span fits when the live window,
// the tool surface the turn actually sends, and the span together are at
// most B — the same inequality windowTrim's suffix walk uses (<=, so a
// total landing exactly on B fits; DS-10 #3).
func recallSpanFits(budget, windowTokens, toolSurfaceTokens, spanTokens int) bool {
	return windowTokens+toolSurfaceTokens+spanTokens <= budget
}

// recallNonFitText is the FR-042 tool-result text for a span that does not
// fit B. N is the number of turns found, X the span's estimator tokens.
func recallNonFitText(span *RecallSpan) string {
	return fmt.Sprintf(
		"%d turn(s) found (%d estimator tokens) but they do not fit the current window; narrow with turn_range or query",
		len(span.Ordinals), span.Tokens,
	)
}

// recallInjectionDecision is decideRecallInjection's verdict for one recall
// tool result: whether to splice, and the text to admit for the result.
type recallInjectionDecision struct {
	span    *RecallSpan
	inject  bool
	content string
}

// decideRecallInjection runs the budget-first check for a just-executed
// recall_conversation call. messages is the turn's in-memory slice BEFORE
// the tool result is appended; content is the tool's result text after the
// prompt-guard pass. For any other tool, or when no new span was set, the
// content is returned unchanged with inject=false.
//
// Not fitting is never silent: the span is dropped (it must not resurface
// at the next assembly claiming to be in context) and the result text is
// rewritten to the FR-042 message so the model learns the real outcome.
func (al *AgentLoop) decideRecallInjection(
	ts *turnState, toolName string, messages []providers.Message, content string,
) recallInjectionDecision {
	unchanged := recallInjectionDecision{content: content}
	if toolName != recallConversationToolName || ts == nil || ts.agent == nil {
		return unchanged
	}
	span := al.activeRecallSpan(ts.sessionKey)
	if span == nil {
		if ts.depth > 0 {
			// FR-044 (ADR-066 §6.4 known limitation, pinned by
			// TestSubTurn_RecallReadsParentStore_KnownLimitation, not fixed
			// here): the recall tool was constructed with the parent's
			// agent.Sessions, but the child runs under its own ephemeral
			// session key, so its recall reads the parent store under a key
			// that holds nothing.
			logger.InfoCF("agent",
				"recall_conversation in a delegated sub-turn found no archive — known limitation: "+
					"the child reads the parent store under its ephemeral session key (ADR-066 §6.4, FR-044)",
				map[string]any{
					"agent_id":    ts.agent.ID,
					"session_key": ts.sessionKey,
					"depth":       ts.depth,
				})
		}
		return unchanged
	}
	if span == ts.injectedRecallSpan {
		// Already in the slice (identity) — nothing new to splice.
		return unchanged
	}

	inject := true
	windowTokens, toolTokens, budget := 0, 0, 0
	if !ts.agent.budgetChecksExempt() {
		// The window the next request will carry: the current slice minus
		// the pinned core at [0] and minus the block a replaced span
		// occupies (it is removed before the new one is spliced), plus the
		// recall tool result itself.
		tail := removeInjectedRecallBlock(ts, messages, false)
		if len(tail) > 0 && tail[0].Role == "system" {
			tail = tail[1:]
		}
		// The result's own cost is estimated from its text alone: the real
		// role:"tool" message is built by the choke point (admitToolResult),
		// never here (FR-009, TestChokePoint_ProducerListByGrep).
		windowTokens = sumMessageTokens(tail) + estimateMessageTokens(providers.Message{Content: content})
		toolTokens = al.sentToolSurfaceTokens(ts.agent, ts.sessionKey)
		budget = agentContextBudget(ts.agent)
		inject = recallSpanFits(budget, windowTokens, toolTokens, span.Tokens)
	}

	if !inject {
		al.dropRecallSpan(ts.sessionKey, "pressure")
		logger.InfoCF("agent", "recall span refused: does not fit the window budget; not injected",
			map[string]any{
				"agent_id":      ts.agent.ID,
				"session_key":   ts.sessionKey,
				"turns":         len(span.Ordinals),
				"from_turn":     span.FromTurn,
				"to_turn":       span.ToTurn,
				"span_tokens":   span.Tokens,
				"window_tokens": windowTokens,
				"tool_tokens":   toolTokens,
				"budget":        budget,
			})
		return recallInjectionDecision{span: span, inject: false, content: recallNonFitText(span)}
	}
	return recallInjectionDecision{span: span, inject: true, content: content}
}

// spliceRecallSpan inserts span's messages into the turn's in-memory slice
// at the BuildMessages position — right after the pinned system message at
// [0], before the window — and records the block on ts so a later recall in
// the same turn can remove it (E20) and a later reassembly does not double
// it (FR-043).
//
// Sanitisation reuses the one sanitiser BuildMessages uses, over the
// combined (span + window) sequence, so cross-boundary orphans are caught
// identically. Only the span's surviving messages are taken from that pass;
// the window tail is kept byte-for-byte. The tail was sanitised at assembly
// already, and re-filtering it mid-batch would be wrong: while a parallel
// tool batch is still being executed, the current assistant message's
// remaining results are not yet in the slice, and the sanitiser's pairing
// pass would drop that whole step as "incomplete".
func (al *AgentLoop) spliceRecallSpan(ts *turnState, messages []providers.Message, span *RecallSpan) []providers.Message {
	if span == nil || len(span.Msgs) == 0 {
		return messages
	}
	messages = removeInjectedRecallBlock(ts, messages, true)

	at := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		at = 1
	}
	tail := messages[at:]
	combined := make([]providers.Message, 0, len(span.Msgs)+len(tail))
	combined = append(combined, span.Msgs...)
	combined = append(combined, tail...)
	_, kept := sanitizeHistoryIndexed(combined)
	keptSpan := make([]providers.Message, 0, len(span.Msgs))
	for _, idx := range kept {
		if idx < len(span.Msgs) {
			keptSpan = append(keptSpan, span.Msgs[idx])
		}
	}

	out := make([]providers.Message, 0, len(messages)+len(keptSpan))
	out = append(out, messages[:at]...)
	out = append(out, keptSpan...)
	out = append(out, tail...)

	ts.injectedRecallSpan = span
	ts.injectedRecallAt = at
	ts.injectedRecallLen = len(keptSpan)

	logger.InfoCF("agent", "recall span injected into the next request at the tool-result site",
		map[string]any{
			"agent_id":          ts.agent.ID,
			"session_key":       ts.sessionKey,
			"turns":             len(span.Ordinals),
			"from_turn":         span.FromTurn,
			"to_turn":           span.ToTurn,
			"span_tokens":       span.Tokens,
			"span_messages":     len(span.Msgs),
			"injected_messages": len(keptSpan),
			"position":          at,
			"request_messages":  len(out),
		})
	return out
}

// removeInjectedRecallBlock returns messages without the block the
// currently recorded injected span occupies, as a fresh slice (never
// aliasing the input). When reset is true the record on ts is cleared —
// the caller is replacing the span (E20). With reset=false it is a pure
// what-if used by the fit check. A record that no longer fits the slice
// (defensive: it should never happen, every rebuild resets it) is ignored.
func removeInjectedRecallBlock(ts *turnState, messages []providers.Message, reset bool) []providers.Message {
	if ts.injectedRecallSpan == nil || ts.injectedRecallLen == 0 ||
		ts.injectedRecallAt < 0 || ts.injectedRecallAt+ts.injectedRecallLen > len(messages) {
		if reset {
			ts.injectedRecallSpan, ts.injectedRecallAt, ts.injectedRecallLen = nil, 0, 0
		}
		return messages
	}
	at, n := ts.injectedRecallAt, ts.injectedRecallLen
	out := make([]providers.Message, 0, len(messages)-n)
	out = append(out, messages[:at]...)
	out = append(out, messages[at+n:]...)
	if reset {
		logger.InfoCF("agent", "recall span removed from the in-memory slice: replaced by a newer recall in the same turn",
			map[string]any{
				"agent_id":    ts.agent.ID,
				"session_key": ts.sessionKey,
				"from_turn":   ts.injectedRecallSpan.FromTurn,
				"to_turn":     ts.injectedRecallSpan.ToTurn,
				"span_tokens": ts.injectedRecallSpan.Tokens,
				"messages":    n,
			})
		ts.injectedRecallSpan, ts.injectedRecallAt, ts.injectedRecallLen = nil, 0, 0
	}
	return out
}

// recordAssembledRecallSpan is called by assembleMessages after every
// from-scratch assembly: BuildMessages has included the active span (if
// any) once, right after the system message, sanitised together with
// history. Recording it here is what makes the tool-result site skip a
// span that is already present (FR-043) and lets a same-turn replacement
// find the block to remove. history must be the exact slice BuildMessages
// received (post-projection) so the survivor count matches its pass.
func recordAssembledRecallSpan(ts *turnState, span *RecallSpan, history []providers.Message) {
	if ts == nil {
		return
	}
	if span == nil || len(span.Msgs) == 0 {
		ts.injectedRecallSpan, ts.injectedRecallAt, ts.injectedRecallLen = nil, 0, 0
		return
	}
	combined := make([]providers.Message, 0, len(span.Msgs)+len(history))
	combined = append(combined, span.Msgs...)
	combined = append(combined, history...)
	_, kept := sanitizeHistoryIndexed(combined)
	n := 0
	for _, idx := range kept {
		if idx < len(span.Msgs) {
			n++
		}
	}
	ts.injectedRecallSpan = span
	ts.injectedRecallAt = 1 // BuildMessages: [0] is the single system message
	ts.injectedRecallLen = n
}

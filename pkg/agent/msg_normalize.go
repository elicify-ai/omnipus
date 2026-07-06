// Omnipus — outgoing message normalization for LLM providers (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// normalizeMessagesForProvider removes provider-illegal message patterns from
// outgoing request arrays. Applied just before every LLM call (inside callLLM).
//
// Rules enforced:
//
//	Rule A: An assistant message with blank Content AND no ToolCalls is dropped
//	        (empty/no-op assistant turn that confuses strict providers).
//
//	Rule B: Two CONSECUTIVE same-role messages are merged ONLY when BOTH roles
//	        are user, assistant, or system AND NEITHER carries ToolCalls AND
//	        NEITHER is role "tool". When merging, Content is joined with "\n"
//	        and Media, ReasoningContent, SystemParts are preserved.
//
//	Rule C: An assistant message carrying ToolCalls passes through verbatim —
//	        never merged, never dropped.
//
//	Rule D: A role="tool" message is a TRUE ORPHAN when its ToolCallID is not
//	        declared by any preceding assistant's ToolCalls[].ID in the same
//	        slice. True orphans are dropped. A role="tool" message with an empty
//	        ToolCallID is kept only when the immediately preceding kept message
//	        is an assistant carrying ToolCalls (the single-call omitted-id case);
//	        otherwise it is treated as an orphan and dropped. A role="tool"
//	        message whose ToolCallID IS declared is ALWAYS kept, regardless of
//	        how many tool results follow the same assistant.
//
// Design rationale:
//   - Rule D fixes the transcript-less-path orphan gap: repairHistory
//     (repair.go) only runs when TranscriptStore != nil && TranscriptSessionID != ""
//     (loop.go ~L5269). The background-task path (processSystemMessage, loop.go
//     ~L4597) has no TranscriptStore → repair is skipped. Even when repair runs,
//     it returns orphans unchanged when the transcript is unreachable. A true
//     orphan role:"tool" reaching the Anthropic/OpenAI serializer causes HTTP 400.
//   - Rule D must NOT drop paired tool results: 3 parallel tool results that
//     are all paired to one assistant's 3 ToolCalls are all kept (F1 fix still
//     holds, now generalised: the declared set is accumulated across all preceding
//     assistant messages, not just the immediately preceding one).
//   - The allocation-free fast path avoids any heap work on valid histories.

package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// normalizeMessagesForProvider cleans an outgoing message slice so that strict
// providers (Z.AI/GLM, Gemini, Anthropic) do not reject the request with a 400
// error.
//
// The function is allocation-free when no normalization is needed (fast path):
// it detects whether any change is required before allocating a new slice.
func normalizeMessagesForProvider(msgs []providers.Message) []providers.Message {
	if len(msgs) == 0 {
		return msgs
	}

	// Fast path: check whether any normalization is actually needed.
	if !needsNormalization(msgs) {
		return msgs
	}

	// Full pass: build a clean output slice.
	// Track the running set of declared tool_call IDs (accumulated from every
	// assistant message seen so far) for Rule D orphan detection.
	declaredToolIDs := map[string]bool{}
	out := make([]providers.Message, 0, len(msgs))

	// Counters for the observability log emitted after the pass (FIX 2).
	var droppedEmptyAssistant, droppedOrphanTool, merged int

	for _, m := range msgs {
		switch {
		case m.Role == "assistant":
			// Rule A: drop an empty/no-op assistant (blank Content, no ToolCalls).
			if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
				droppedEmptyAssistant++
				continue
			}
			// Rule C: assistant carrying ToolCalls passes through verbatim.
			// Accumulate its declared IDs for Rule D before appending.
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					if tc.ID != "" {
						declaredToolIDs[tc.ID] = true
					}
				}
				out = append(out, m)
				continue
			}
			// Plain-text assistant: apply Rule B merge.
			prevLen := len(out)
			out = appendOrMergePlainText(out, m)
			if len(out) == prevLen {
				merged++ // message was merged into the last element rather than appended
			}

		case m.Role == "tool":
			// Rule D: drop true orphans; keep paired tool results.
			if m.ToolCallID != "" {
				// Non-empty ToolCallID: keep iff declared by a preceding assistant.
				if !declaredToolIDs[m.ToolCallID] {
					// True orphan — drop.
					droppedOrphanTool++
					continue
				}
				out = append(out, m)
			} else {
				// Empty ToolCallID: keep only when the last kept message is an
				// assistant carrying ToolCalls (single-call omitted-id case).
				if len(out) > 0 && out[len(out)-1].Role == "assistant" && len(out[len(out)-1].ToolCalls) > 0 {
					out = append(out, m)
				} else {
					// Orphan (no declared call to attach to) — drop.
					droppedOrphanTool++
				}
			}

		default:
			// All other roles (user, system): apply Rule B merge.
			prevLen := len(out)
			out = appendOrMergePlainText(out, m)
			if len(out) == prevLen {
				merged++
			}
		}
	}

	if droppedEmptyAssistant > 0 || droppedOrphanTool > 0 || merged > 0 {
		logger.DebugCF("agent", "normalizeMessagesForProvider: messages adjusted before LLM call",
			map[string]any{
				"dropped_empty_assistant": droppedEmptyAssistant,
				"dropped_orphan_tool":     droppedOrphanTool,
				"merged":                  merged,
			})
	}

	return out
}

// appendOrMergePlainText appends m to out. If the last element of out has the
// same role as m AND neither carries ToolCalls AND neither is role "tool", the
// two are merged in place (Rule B). All fields — Content, Media,
// ReasoningContent, SystemParts — are preserved in the merge.
//
// Pre-condition: m.Role != "tool" && len(m.ToolCalls) == 0 (callers enforce this).
func appendOrMergePlainText(out []providers.Message, m providers.Message) []providers.Message {
	if len(out) == 0 {
		return append(out, m)
	}
	last := &out[len(out)-1]

	// Only merge when roles match, the predecessor is also a plain-text message
	// (no ToolCalls), and neither is a role="tool" message.
	if last.Role != m.Role || last.Role == "tool" || len(last.ToolCalls) > 0 {
		return append(out, m)
	}

	// Same plain-text role — merge in place.
	if m.Content != "" {
		if last.Content == "" {
			last.Content = m.Content
		} else {
			last.Content = last.Content + "\n" + m.Content
		}
	}
	if len(m.Media) > 0 {
		// Clone before append to avoid writing into the source message's backing
		// array when cap(last.Media) > len(last.Media) (FIX 3 — matches repair.go:157-159).
		last.Media = append(append([]string(nil), last.Media...), m.Media...)
	}
	if m.ReasoningContent != "" && last.ReasoningContent == "" {
		last.ReasoningContent = m.ReasoningContent
	}
	if len(m.SystemParts) > 0 && len(last.SystemParts) == 0 {
		// Clone before append for the same aliasing reason as Media above.
		last.SystemParts = append(append([]providers.ContentBlock(nil), last.SystemParts...), m.SystemParts...)
	}
	return out
}

// needsNormalization performs a single O(n) scan to detect whether any
// normalization rule would fire, avoiding allocation on the happy path.
func needsNormalization(msgs []providers.Message) bool {
	// Track declared tool_call IDs for Rule D detection.
	declaredToolIDs := map[string]bool{}
	var lastKeptRole string
	var lastKeptHasToolCalls bool

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			// Rule A: empty assistant.
			if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
				return true
			}
			// Rule B: consecutive plain-text assistants.
			if len(m.ToolCalls) == 0 && lastKeptRole == "assistant" && !lastKeptHasToolCalls {
				return true
			}
			// Accumulate declared IDs.
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					declaredToolIDs[tc.ID] = true
				}
			}
			lastKeptRole = "assistant"
			lastKeptHasToolCalls = len(m.ToolCalls) > 0

		case "tool":
			// Rule D: true orphan.
			if m.ToolCallID != "" {
				if !declaredToolIDs[m.ToolCallID] {
					return true
				}
			} else {
				// Empty ToolCallID: orphan unless last kept is assistant-with-toolcalls.
				if !(lastKeptRole == "assistant" && lastKeptHasToolCalls) {
					return true
				}
			}
			lastKeptRole = "tool"
			lastKeptHasToolCalls = false

		default:
			// Rule B: consecutive same-role plain-text (user, system, etc.).
			if lastKeptRole == m.Role && m.Role != "tool" {
				return true
			}
			lastKeptRole = m.Role
			lastKeptHasToolCalls = false
		}
	}
	return false
}

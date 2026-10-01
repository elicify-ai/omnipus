// Omnipus — Session recovery after ungraceful shutdown (SIGKILL / OOM)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — RecoverOrphanedToolCalls implements FR-069 (SIGKILL / OOM
// recovery) and FR-088 (LLM context hygiene on resume).
//
// On every session resume / next gateway boot the loop inspects the tail of the
// session's message history. If the last entry is an "assistant" message
// containing tool_calls without a matching "tool" result, the orphaned call
// is considered left-over from a gateway kill while awaiting approval.
//
// Recovery:
//  1. A synthetic entry {role: "system", content: "..."} is appended to the
//     session transcript as a turn_canceled_restart record (FR-069).
//  2. The history presented to the LLM on resume is rebuilt WITHOUT the orphaned
//     assistant tool_call message (FR-088), preventing hallucinated results.
//  3. An audit event tool.policy.ask.denied with reason="restart" is emitted
//     for each orphaned tool call (FR-069).
//
// The function is safe to call on every session load; it is a no-op when the
// transcript is clean.

package agent

import (
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// orphanedToolCall describes a tool call that has no matching tool result.
type orphanedToolCall struct {
	ToolCallID     string
	ToolName       string
	assistantIndex int
}

// RecoverOrphanedToolCalls inspects the tail of the session's message history.
// If orphaned tool calls are found (assistant message with tool_calls but no
// subsequent tool result), it:
//  1. Appends a synthetic system message to the transcript documenting the
//     ungraceful shutdown recovery (FR-069).
//  2. Returns the rebuilt history with the orphaned assistant message removed
//     (FR-088), so the next LLM call does not observe the half-completed turn.
//  3. Emits audit events for each orphaned tool call.
//
// Parameters:
//   - store: the session store for the agent.
//   - sessionKey: the session whose history to inspect and repair.
//   - auditLog: optional audit logger; if nil, events are skipped.
//
// Returns the cleaned history slice (all messages except the orphaned assistant
// turn). If no orphaned calls are found, returns the original history unchanged.
//
// Safe to call on every session load — it is idempotent: orphans that already
// have a synthetic turn_canceled_restart system message are skipped.
func RecoverOrphanedToolCalls(
	store session.SessionStore,
	sessionKey string,
	auditLog *audit.Logger,
) []providers.Message {
	history := store.GetHistory(sessionKey)
	if len(history) == 0 {
		return history
	}

	orphans := findOrphanedToolCalls(history)
	if len(orphans) == 0 {
		return history
	}

	// Use the same position-scoped marker binding as snapshot reconstruction.
	// A canceled call in an earlier assistant cannot suppress a later reused id.
	_, alreadyCancelled := restartCancellations(len(history), func(i int) providers.Message {
		return history[i]
	})

	// Append a synthetic system message to the transcript for audit/replay.
	// We do NOT remove the orphaned entry from the on-disk transcript — the
	// transcript is an immutable audit record. We only strip it from the
	// reconstructed history we pass to the LLM (FR-088).
	for _, o := range orphans {
		// Idempotency belongs to this assistant position and declared call only.
		identity := restartCallIdentity{assistantIndex: o.assistantIndex, toolCallID: o.ToolCallID}
		if alreadyCancelled[identity] {
			slog.Debug("agent: SIGKILL recovery — synthetic entry already present, skipping",
				"session_key", sessionKey, "tool_call_id", o.ToolCallID)
			continue
		}
		syntheticContent := fmt.Sprintf(
			`{"type":"turn_canceled_restart","tool_call_id":%q,"reason":"ungraceful_shutdown_recovery"}`,
			o.ToolCallID,
		)
		store.AddMessage(sessionKey, "system", syntheticContent)
		if err := store.Save(sessionKey); err != nil {
			slog.Error("agent: failed to persist turn_canceled_restart entry",
				"session_key", sessionKey, "tool_call_id", o.ToolCallID, "error", err)
		}

		// Emit audit event (FR-069).
		// CRIT-6 + typed-Decision/Event migration: route through audit.EmitEntry
		// so Log failure bumps the audit-skipped counter (/health audit_degraded),
		// and use typed EventToolPolicyAskDenied + DecisionDeny constants in
		// place of raw string literals.
		audit.EmitEntry(auditLog, &audit.Entry{
			Event:    audit.EventToolPolicyAskDenied,
			Decision: audit.DecisionDeny,
			Tool:     o.ToolName,
			Details: map[string]any{
				"tool_call_id": o.ToolCallID,
				"reason":       "restart",
				"recovery":     "ungraceful_shutdown_recovery",
				"session_key":  sessionKey,
			},
		})

		slog.Warn("agent: SIGKILL recovery — orphaned tool call detected and synthetic deny appended",
			"session_key", sessionKey, "tool_call_id", o.ToolCallID, "tool_name", o.ToolName)
	}

	// FR-088: return a rebuilt history that omits the orphaned assistant turn.
	// The orphaned message is the last assistant message with tool_calls that
	// has no matching tool result. Remove it from the context window so the
	// LLM does not see a dangling unanswered tool call.
	cleanedHistory := stripOrphanedAssistantTurn(history)
	return cleanedHistory
}

// findOrphanedToolCalls scans the message history from the end and returns any
// tool calls in the last assistant message that lack a corresponding tool result.
// Only the last assistant turn is inspected — earlier turns are assumed to have
// been resolved normally.
func findOrphanedToolCalls(history []providers.Message) []orphanedToolCall {
	if len(history) == 0 {
		return nil
	}

	// Walk backward to find the last assistant message.
	var lastAssistantIdx = -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			lastAssistantIdx = i
			break
		}
	}

	if lastAssistantIdx < 0 {
		return nil
	}

	lastAssistant := history[lastAssistantIdx]
	if len(lastAssistant.ToolCalls) == 0 {
		return nil
	}

	// Build a set of tool_call_ids that have a corresponding tool result in
	// messages after the assistant turn.
	resolvedIDs := make(map[string]struct{})
	for i := lastAssistantIdx + 1; i < len(history); i++ {
		msg := history[i]
		if msg.Role == "tool" && msg.ToolCallID != "" {
			resolvedIDs[msg.ToolCallID] = struct{}{}
		}
	}

	// Collect tool calls without a result.
	var orphans []orphanedToolCall
	for _, tc := range lastAssistant.ToolCalls {
		if _, resolved := resolvedIDs[tc.ID]; !resolved {
			name := tc.Name
			if name == "" && tc.Function != nil {
				name = tc.Function.Name
			}
			orphans = append(orphans, orphanedToolCall{
				ToolCallID:     tc.ID,
				ToolName:       name,
				assistantIndex: lastAssistantIdx,
			})
		}
	}
	return orphans
}

// stripOrphanedAssistantTurn returns a copy of history with the last assistant
// message removed when it contains unresolved tool calls. This is the FR-088
// context-hygiene step — the message stays in the on-disk transcript but is
// excluded from the next LLM prompt rebuild.
func stripOrphanedAssistantTurn(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	// Find the last assistant message (same logic as findOrphanedToolCalls).
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" && len(history[i].ToolCalls) > 0 {
			// Remove this entry and everything after it (any partial tool results).
			cleaned := make([]providers.Message, i)
			copy(cleaned, history[:i])
			return cleaned
		}
		// If the last assistant message has no tool_calls, nothing to strip.
		if history[i].Role == "assistant" {
			break
		}
	}
	return history
}

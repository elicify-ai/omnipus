package agent

import (
	"context"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type orphanedToolCall struct {
	ToolCallID     string
	ToolName       string
	assistantIndex int
}

// RecoverOrphanedToolCalls preserves completed siblings and records only missing
// occurrences as recovery observations. An absent return is never policy Deny.
// Native callers supply checked current-state reconciliation and an error fence.
func RecoverOrphanedToolCalls(store session.SessionStore, sessionKey string, auditLog *audit.Logger, options ...orphanRecoveryOptions) []providers.Message {
	history := store.GetHistory(sessionKey)
	var opt orphanRecoveryOptions
	if len(options) > 0 {
		opt = options[0]
	}
	failed := false
	report := func(err error) {
		failed = true
		if opt.OnError != nil {
			opt.OnError(err)
		}
		slog.Error("tool recovery: observation was not saved; provider continuation is refused", "session_key", sessionKey, "error", err)
	}
	for _, o := range findOrphanedToolCalls(history) {
		ob := recoveryObservation{Disposition: tools.FileEffectUnknown, Reason: "the missing result has no checked recovery adapter"}
		if opt.Resolve != nil {
			var err error
			ob, err = opt.Resolve(o)
			if err != nil {
				report(err)
				break
			}
		}
		message, err := ob.message(o.ToolCallID)
		if err != nil {
			report(err)
			break
		}
		if checked, ok := store.(session.ContextWindowStore); ok && ob.Assistant.EntryID != "" {
			_, _, err = checked.AppendModelMessage(context.Background(), sessionKey, session.ModelAppend{
				Message: message, ViewMembership: session.ViewMembershipModel,
				Source: session.EntrySource{Kind: "tool"}, ToolResultFor: &ob.Assistant,
			})
		} else {
			err = store.AddFullMessage(sessionKey, message)
		}
		if err == nil {
			err = store.Save(sessionKey)
		}
		if err != nil {
			report(recoveryObservationError(o.ToolCallID, err))
			break
		}
		event := audit.EventToolRecoveryObservation
		decision := ""
		if ob.Disposition == tools.FileEffectUnknown {
			event = audit.EventToolRecoveryInterrupted
			decision = audit.DecisionError
		}
		audit.EmitEntry(auditLog, &audit.Entry{Event: event, Decision: decision, Tool: o.ToolName, Details: map[string]any{
			"tool_call_id": o.ToolCallID, "assistant": ob.Assistant, "disposition": ob.Disposition,
			"reason": ob.Reason, "session_key": sessionKey, "original_result_recovered": false, "current_image": ob.Image,
		}})
	}
	if failed {
		return history
	}
	history = store.GetHistory(sessionKey)
	cleaned := make([]providers.Message, 0, len(history))
	for _, m := range history {
		if restartCancellationID(m) == "" {
			cleaned = append(cleaned, m)
		}
	}
	return cleaned
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

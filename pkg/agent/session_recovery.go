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
	var history []providers.Message
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
	if opt.View == nil {
		if checked, ok := store.(session.ContextWindowStore); ok {
			view, err := checked.WindowView(context.Background(), sessionKey)
			if err != nil {
				report(err)
				return nil
			}
			opt.View = &view
		}
	}
	var err error
	history, err = recoveryHistory(store, sessionKey, opt.View)
	if err != nil {
		report(err)
		return nil
	}
	for _, o := range findOrphanedToolCalls(history) {
		var ob recoveryObservation
		if opt.Resolve != nil {
			var err error
			ob, err = opt.Resolve(o)
			if err != nil {
				report(err)
				break
			}
		} else {
			ob = unprovedRecoveryObservation(opt.View, o)
		}
		message, err := ob.message(o.ToolCallID)
		if err != nil {
			report(err)
			break
		}
		if checked, ok := store.(session.ContextWindowStore); ok && ob.Assistant.EntryID != "" {
			_, _, err = checked.AppendModelMessage(context.Background(), sessionKey, session.ModelAppend{
				Message: message, ViewMembership: session.ViewMembershipModel,
				Source: session.EntrySource{Kind: "recovery_observation"}, ToolResultFor: &ob.Assistant,
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
			"tool_call_id": o.ToolCallID, "assistant": ob.Assistant, "execution": ob.Execution, "disposition": ob.Disposition,
			"reason": ob.Reason, "session_key": sessionKey, "original_result_recovered": false, "current_image": ob.Image,
		}})
	}
	if failed {
		return history
	}
	history, err = recoveryHistory(store, sessionKey, nil)
	if err != nil {
		report(err)
		return nil
	}
	// One projection, occurrence-scoped on both surfaces: a cancellation record
	// is excluded from the model-facing view only when it binds to an
	// occurrence — a declared call in a valid group. A recognised record that
	// binds to no occurrence authorises no removal, no observation and no
	// re-indexing, so it stays, exactly as it stays in the assembled view.
	omitted, _ := restartCancellations(len(history), func(i int) providers.Message {
		return history[i]
	})
	cleaned := make([]providers.Message, 0, len(history))
	for i, m := range history {
		if !omitted[i] {
			cleaned = append(cleaned, m)
		}
	}
	return cleaned
}

// findOrphanedToolCalls examines each declaration independently. A later reuse
// of a provider call id never settles an earlier occurrence.
func findOrphanedToolCalls(history []providers.Message) []orphanedToolCall {
	var orphans []orphanedToolCall
	for i, msg := range history {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		resolved := map[string]bool{}
		for j := i + 1; j < len(history); j++ {
			next := history[j]
			if next.Role == "assistant" || next.Role == "user" {
				break
			}
			if next.Role == "tool" {
				resolved[next.ToolCallID] = true
			}
		}
		for _, call := range msg.ToolCalls {
			if resolved[call.ID] {
				continue
			}
			name := call.Name
			if name == "" && call.Function != nil {
				name = call.Function.Name
			}
			orphans = append(orphans, orphanedToolCall{ToolCallID: call.ID, ToolName: name, assistantIndex: i})
		}
	}
	return orphans
}

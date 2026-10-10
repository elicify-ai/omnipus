package agent

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// admitResultWindow persists full admitted bytes, obtains their actual archive
// identity, and commits the exact first cut before any producer gets a message.
func admitResultWindow(ts *turnState, adm toolResultAdmission, archived providers.Message, capChars int) (providers.Message, int, memory.ProjectionState, error) {
	ctx := context.Background()
	if ts != nil {
		if err := ts.contextWindowError(); err != nil {
			return providers.Message{}, -1, "", err
		}
		if ts.ctx != nil {
			ctx = ts.ctx
		}
	}
	var store session.ContextWindowStore
	var view session.WindowView
	line := -1
	if ts != nil && ts.agent != nil && ts.agent.Sessions != nil && !ts.opts.NoHistory {
		var ok bool
		store, ok = ts.agent.Sessions.(session.ContextWindowStore)
		if !ok {
			return providers.Message{}, -1, "", fmt.Errorf("context admission: session store does not support atomic context checkpoints")
		}
		// The producer names the exact assistant occurrence that issued this call
		// (kept from that record's own checked append); the archive returns the
		// exact slot it assigned. No lookup by tool_call_id, no last-line guess.
		issuer, known := ts.callIssuer(adm.ToolCallID)
		if !known {
			return providers.Message{}, -1, "", fmt.Errorf("context admission: tool result %q has no recorded issuing assistant", adm.ToolCallID)
		}
		slot, appended, err := store.AppendModelMessage(ctx, ts.sessionKey, session.ModelAppend{
			Message: archived, ViewMembership: session.ViewMembershipModel,
			Source: session.EntrySource{Kind: "tool"}, ToolResultFor: &issuer, AgentID: ts.agent.ID,
		})
		if err != nil {
			return providers.Message{}, -1, "", fmt.Errorf("context admission: append tool result: %w", err)
		}
		line, view = slot.Ordinal, appended
	}
	window := archived
	if utf8.RuneCountInString(archived.Content) <= capChars {
		return window, line, "", nil
	}
	mark, err := buildRecallMark("capped", adm.Tool, adm.ToolCallID, line, archived.Content, turnNumberForArchiveLine(viewArchive{view: view}, line))
	if err != nil {
		return providers.Message{}, line, "", err
	}
	kept := max(0, capChars-utf8.RuneCountInString(mark)-2)
	state := memory.ProjectionCapped
	if adm.IsError {
		state = memory.ProjectionCappedFailure
	}
	if kept == 0 {
		state = memory.ProjectionEmptied
	}
	if store == nil {
		if kept == 0 {
			mark, err = buildRecallMark("emptied", adm.Tool, adm.ToolCallID, line, archived.Content, turnNumberForArchiveLine(nil, line))
			if err != nil {
				return providers.Message{}, line, "", err
			}
		}
		window.Content = mark
		if kept > 0 {
			r := []rune(archived.Content)
			window.Content = string(r[:(kept+1)/2]) + "\n" + mark + "\n" + string(r[len(r)-kept/2:])
		}
		return window, line, state, nil
	}
	window.Content, err = projectSource(viewArchive{view: view}, line, kept, adm.Tool, adm.ToolCallID)
	if err != nil {
		return providers.Message{}, line, "", err
	}
	after := view.State.Clone()
	key := memory.ProjectionKey{ToolCallID: adm.ToolCallID, ArchiveLine: line}
	after.Projection.Entries[key] = state
	after.Projection.SourceRunes[key] = kept
	if err := store.CommitWindow(ctx, ts.sessionKey, view.State, after); err != nil {
		return providers.Message{}, line, "", fmt.Errorf("context admission: persist exact tool-result projection: %w", err)
	}
	return window, line, state, nil
}

func failedResultAdmission(ts *turnState, err error) admittedToolResult {
	if ts != nil {
		ts.setContextWindowError(err)
	}
	return admittedToolResult{Err: err, ArchiveLine: -1}
}

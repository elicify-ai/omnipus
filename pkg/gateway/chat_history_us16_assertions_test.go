package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func historyUS16LiveOnly(t *testing.T, frames [][]byte, sid, pendingID, turnID string) generated.SessionStateFrame {
	t.Helper()
	var state generated.SessionStateFrame
	var pendingWire []map[string]json.RawMessage
	states, completes := 0, 0
	for _, raw := range frames {
		var envelope wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &envelope))
		switch envelope.Type {
		case "session_state":
			states++
			require.NoError(t, json.Unmarshal(raw, &state))
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &fields))
			require.NoError(t, json.Unmarshal(fields["pending_approvals"], &pendingWire))
			require.NotNil(t, state.SessionId)
			require.Equal(t, sid, *state.SessionId)
			require.NotNil(t, state.HistoryState)
			require.Equal(t, "unavailable", state.HistoryState.Status)
			require.NotNil(t, state.HistoryState.Reason)
			require.Equal(t, "index_missing", *state.HistoryState.Reason)
			require.NotNil(t, state.HistoryState.Message)
			require.Equal(t, pagingUnavailable, *state.HistoryState.Message)
		case "catch_up_complete":
			completes++
			var complete generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(raw, &complete))
			require.Equal(t, sid, complete.SessionId)
			require.Equal(t, "live_only", complete.Mode, "history error completes controls, never history reconstruction")
			require.Greater(t, states, 0, "current controls precede their completion")
		case "session_snapshot", "replay_message", "done", "error":
			t.Fatalf("US1.6 unavailable-history attach invented history success/wipe or a turn error: %s", raw)
		}
	}
	require.Equal(t, 1, states)
	require.Equal(t, 1, completes)
	if pendingID == "" {
		require.Empty(t, state.PendingApprovals)
	} else {
		require.Len(t, state.PendingApprovals, 1)
		pending := state.PendingApprovals[0]
		require.Equal(t, pendingID, pending.ApprovalId)
		require.Equal(t, sid, pending.SessionId)
		require.Equal(t, "mia", pending.AgentId)
		require.Equal(t, "write_file", pending.ToolName)
		// Current contract: these fields are optional until WU-8, but a supplied
		// value must be exact. Raw presence keeps explicit null from masquerading
		// as omission after generated pointer/map decoding.
		require.Len(t, pendingWire, 1)
		if _, present := pendingWire[0]["tool_call_id"]; present {
			require.NotNil(t, pending.ToolCallId)
			require.Equal(t, historyUS16CallID, *pending.ToolCallId)
		}
		if _, present := pendingWire[0]["args"]; present {
			require.NotNil(t, pending.Args)
			require.Equal(t, map[string]any{"path": historyUS16Filename, "content": historyUS16Proof}, pending.Args)
		}
		if _, present := pendingWire[0]["turn_id"]; present {
			require.NotNil(t, pending.TurnId)
			require.Equal(t, turnID, *pending.TurnId)
		}
	}
	if turnID != "" {
		require.NotNil(t, state.ActiveTurn, "actual active work remains reachable beside history failure")
		require.Equal(t, turnID, state.ActiveTurn.TurnId)
		require.Equal(t, "mia", state.ActiveTurn.AgentId)
	}
	return state
}

func (h *historyUS16Harness) assertNoPartialIndex(t *testing.T) {
	t.Helper()
	_, statErr := os.Stat(filepath.Join(h.loop.GetSessionStore().BaseDir(), h.sid, session.ChatHistoryIndexDirectory))
	require.ErrorIs(t, statErr, os.ErrNotExist, "ordinary send and history Retry must not create a suffix-only index")
}

func (h *historyUS16Harness) assertSavedOnce(t *testing.T) {
	t.Helper()
	entries, readErr := h.loop.GetSessionStore().ReadTranscript(h.sid)
	require.NoError(t, readErr)
	count := 0
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == historyUS16Instruction {
			count++
			require.NotEmpty(t, entry.ID)
			require.Equal(t, "mia", entry.AgentID)
		}
	}
	require.Equal(t, 1, count, "one ordinary canonical input; Retry/reconnect never resend")
	h.assertNoPartialIndex(t)
}

func (h *historyUS16Harness) assertHistoryRefuses(t *testing.T) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+h.sid+"/messages", nil)
	request.Header.Set("Authorization", "Bearer "+historyUS16Token)
	response := httptest.NewRecorder()
	h.api.withAuth(h.api.HandleSessions)(response, request)
	pagingRefusal(t, response, http.StatusInternalServerError, pagingUnavailable)
	h.assertNoPartialIndex(t)
}

func (h *historyUS16Harness) assertOneRealDispatch(t *testing.T, turnID string) {
	t.Helper()
	// Caller joined the loop and all owned active work first. Capture the
	// remaining events without a guessed quiet period, retaining awaitTurnEnd's
	// earlier observations too; do not count two sightings of one event twice.
collection:
	for {
		select {
		case event, ok := <-h.events.C:
			require.True(t, ok, "observation subscription stays open until assertions finish")
			h.observedEvents = append(h.observedEvents, event)
		default:
			break collection
		}
	}
	_, toolMessages := h.provider.snapshot()
	require.Len(t, toolMessages, 1)
	starts, ends, toolStarts, toolEnds := 0, 0, 0, 0
	for _, event := range h.observedEvents {
		if event.Meta.AgentID != "mia" {
			continue
		}
		switch event.Kind {
		case agent.EventKindTurnStart:
			payload, valid := event.Payload.(agent.TurnStartPayload)
			require.True(t, valid)
			require.Equal(t, historyUS16Instruction, payload.UserMessage)
			require.Equal(t, turnID, event.Meta.TurnID)
			starts++
		case agent.EventKindTurnEnd:
			payload, valid := event.Payload.(agent.TurnEndPayload)
			require.True(t, valid)
			require.Equal(t, h.sid, payload.SessionID)
			require.Equal(t, turnID, event.Meta.TurnID)
			ends++
		case agent.EventKindToolExecStart:
			payload, valid := event.Payload.(agent.ToolExecStartPayload)
			require.True(t, valid)
			require.Equal(t, h.sid, payload.SessionID)
			require.Equal(t, "write_file", payload.Tool)
			require.Equal(t, session.ToolCallID(historyUS16CallID), payload.ToolCallID)
			require.Equal(t, map[string]any{"path": historyUS16Filename, "content": historyUS16Proof}, payload.Arguments)
			toolStarts++
		case agent.EventKindToolExecEnd:
			payload, valid := event.Payload.(agent.ToolExecEndPayload)
			require.True(t, valid)
			require.Equal(t, h.sid, payload.SessionID)
			require.Equal(t, "write_file", payload.Tool)
			require.Equal(t, session.ToolCallID(historyUS16CallID), payload.ToolCallID)
			require.False(t, payload.IsError, "actual builtin succeeded, not a fabricated tool result")
			require.Equal(t, payload.Result, toolMessages[0].Content, "real builtin output reaches the resumed model unchanged")
			toolEnds++
		}
	}
	require.Equal(t, 1, starts, "exactly one genuine ordinary turn dispatch")
	require.Equal(t, 1, ends, "Stop actually ended that turn before teardown")
	require.Equal(t, 1, toolStarts, "exactly one genuine approved builtin execution")
	require.Equal(t, 1, toolEnds)
}

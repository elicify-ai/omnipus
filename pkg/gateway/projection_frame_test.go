// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// projection_frame_test.go — ADR-066 D5 / FR-022 (T066-12, spec test 43,
// B-26): emptying reaches the SPA two ways — live, as a typed
// tool_result_projection frame built from EventKindToolResultProjection;
// and on reload, as ToolCall.content_state (plus the projected result) on
// the transcript read.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestGateway_ProjectionFrameAndContentState(t *testing.T) {
	t.Run("frame: EventKindToolResultProjection → tool_result_projection, session-scoped", func(t *testing.T) {
		bus := agent.NewEventBus()
		defer bus.Close()

		h := makeMinimalHandler()
		wc, ch := makeForwarderTestConn(64)
		done := runForwarder(h, wc, "chat-1", bus)

		const mark = `{"error":"tool_result_recall_mark","content_state":"emptied","tool":"bash","tool_call_id":"call-9","archive_line":12,"size_chars":1178522,"turn":6,"hint":"recall"}`
		bus.Emit(agent.Event{
			Kind: agent.EventKindToolResultProjection,
			Payload: agent.ToolResultProjectionPayload{
				ChatID:             "chat-1",
				SessionID:          "sess-1",
				ProducingSessionID: "child-sess",
				ToolCallID:         session.ToolCallID("call-9"),
				ArchiveLine:        12,
				ContentState:       "emptied",
				Mark:               mark,
			},
		})
		// Another chat's projection must not reach this connection.
		bus.Emit(agent.Event{
			Kind: agent.EventKindToolResultProjection,
			Payload: agent.ToolResultProjectionPayload{
				ChatID: "chat-other", SessionID: "sess-other",
				ToolCallID: "call-x", ArchiveLine: 1, ContentState: "emptied",
			},
		})
		bus.Close()
		<-done

		require.Len(t, ch, 1, "exactly one frame: the other chat's projection is filtered")
		raw := <-ch
		var f generated.ToolResultProjectionFrame
		require.NoError(t, json.Unmarshal(raw, &f), "frame must decode as the generated wire type: %s", raw)
		assert.Equal(t, string(generated.WsFrameTypeToolResultProjection), f.Type)
		assert.Equal(t, "sess-1", f.SessionId)
		assert.Equal(t, "call-9", f.ToolCallId)
		assert.Equal(t, 12, f.ArchiveLine)
		assert.Equal(t, "emptied", f.ContentState)
		require.NotNil(t, f.Mark)
		assert.Equal(t, mark, *f.Mark)
		require.NotNil(t, f.ProducingSessionId, "present iff it differs from session_id (ADR-057 FR-013)")
		assert.Equal(t, "child-sess", *f.ProducingSessionId)

		// Contract: the bytes on the wire validate against the schema.
		var generic map[string]any
		require.NoError(t, json.Unmarshal(raw, &generic))
		for _, key := range []string{"type", "session_id", "tool_call_id", "archive_line", "content_state", "mark", "producing_session_id"} {
			assert.Contains(t, generic, key)
		}
	})

	t.Run("transcript read returns content_state and the projected result", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()

		sessionID := createTestSession(t, api)
		store := api.agentLoop.GetSessionStore()
		require.NotNil(t, store)

		require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
			ID: "tc-entry", Type: session.EntryTypeToolCall, AgentID: "mia",
			ToolCalls: []session.ToolCall{
				{ID: "call-1", Tool: "bash", Status: "success", Result: map[string]any{"text": strings.Repeat("full ", 10)}},
				{ID: "call-2", Tool: "bash", Status: "success", Result: map[string]any{"text": "kept"}},
			},
		}))
		// What the D5 pass does (pkg/agent/empty_in_place.go → UnifiedStore).
		_, err := store.UpdateToolCallProjections(sessionID, []session.ToolCallProjectionUpdate{
			{ToolCallID: "call-1", ContentState: "emptied", Result: map[string]any{"text": "[mark]"}},
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
		api.HandleSessions(w, r)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var entries []struct {
			ToolCalls []generated.ToolCall `json:"tool_calls"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
		calls := make([]generated.ToolCall, 0, 2)
		for _, e := range entries {
			calls = append(calls, e.ToolCalls...)
		}
		require.Len(t, calls, 2)
		require.NotNil(t, calls[0].ContentState)
		assert.Equal(t, generated.ToolCallContentState("emptied"), *calls[0].ContentState)
		require.NotNil(t, calls[0].Result)
		assert.Equal(t, "[mark]", (*calls[0].Result)["text"], "the transcript result is the PROJECTED content")
		assert.Nil(t, calls[1].ContentState, "absent = full")
		assert.Equal(t, "kept", (*calls[1].Result)["text"])
	})
}

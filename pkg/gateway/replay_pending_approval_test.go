// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestReplayPendingToolCallApproval exercises #1397 through real sockets and
// attach/replay, not a replacement replay function. A result is terminal evidence:
// a pending record must replay START only, regardless of the tool's name or
// whether a live approval registry is available. Terminal controls pin the
// existing success/error wire projection, not a new status contract.
func TestReplayPendingToolCallApproval(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(h.Wait)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cases := []struct {
		name, tool, status, wantResultStatus, failure string
	}{
		{name: "pending ordinary", tool: "read_file", status: "pending"},
		{name: "pending delegate", tool: "delegate", status: "pending"},
		{name: "pending legacy spawn", tool: "spawn", status: "pending"},
		{name: "pending task", tool: "create_task", status: "pending"},
		{name: "success", tool: "read_file", status: "success", wantResultStatus: "success"},
		{name: "legacy empty success", tool: "read_file", wantResultStatus: "success"},
		{name: "error", tool: "read_file", status: "error", wantResultStatus: "error", failure: "file not found"},
		{name: "denied", tool: "read_file", status: "denied", wantResultStatus: "error", failure: "Not run: denied by the operator."},
		{name: "cancelled", tool: "read_file", status: "cancelled", wantResultStatus: "error", failure: "call cancelled"},
		{name: "interrupted by restart", tool: "read_file", status: "interrupted", wantResultStatus: "error", failure: "interrupted by gateway restart"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := session.ToolCall{
				ID: "pending-call", Tool: tc.tool, Status: tc.status,
				Parameters: map[string]any{"path": "pending.txt"}, Error: tc.failure,
			}
			if tc.wantResultStatus != "" {
				call.DurationMS = 17
				call.Result = map[string]any{"text": "recorded terminal output"}
			}
			sid, _ := seedApprovalReplay1397(t, store, call)
			frames := attachApprovalReplay1397(t, srv, sid)
			wantEvents := []string{"start:finished-call", "result:finished-call:success", "start:pending-call"}
			if tc.wantResultStatus != "" {
				wantEvents = append(wantEvents, "result:pending-call:"+tc.wantResultStatus)
			}
			assert.Equal(t, wantEvents, frames.events, "pending calls have no completion; terminal calls retain theirs")
			require.Len(t, frames.starts, 2, "both calls must remain visible after reload")
			assert.Equal(t, call.Tool, frames.starts[1].Tool)
			assert.Equal(t, call.Parameters, frames.starts[1].Params)
			assert.Equal(t, sid, frames.starts[1].SessionId)
			require.NotNil(t, frames.starts[1].AgentId)
			assert.Equal(t, "mia", *frames.starts[1].AgentId)
			assert.Nil(t, frames.starts[1].ParentCallId)
			wantResults := 1
			if tc.wantResultStatus != "" {
				wantResults = 2
			}
			require.Len(t, frames.results, wantResults, "pending call cannot acquire a fabricated terminal result")
			assert.Equal(t, map[string]any{"text": "saved-result"}, frames.results[0].Result)
			require.NotNil(t, frames.results[0].DurationMs)
			assert.Equal(t, 9, *frames.results[0].DurationMs)
			if tc.wantResultStatus != "" {
				result := frames.results[1]
				assert.Equal(t, call.Result, result.Result)
				assert.Equal(t, sid, result.SessionId)
				assert.Equal(t, call.Tool, result.Tool)
				require.NotNil(t, result.DurationMs)
				assert.Equal(t, 17, *result.DurationMs)
				if tc.failure == "" {
					assert.Nil(t, result.Error)
				} else {
					require.NotNil(t, result.Error)
					assert.Equal(t, tc.failure, *result.Error)
				}
			}
		})
	}

	for _, action := range []generated.ToolApprovalActionRequestAction{"allow_once", "deny"} {
		t.Run("live approval survives reload then "+string(action), func(t *testing.T) {
			reg := newApprovalRegistryV2(64, 5*time.Minute)
			reg.terminalRetention = 0
			h.approvalRegV2 = reg
			t.Cleanup(func() { reg.cancelAllPendingForRestart() })
			call := session.ToolCall{
				ID: "pending-call", Tool: "read_file", Status: "pending",
				Parameters: map[string]any{"path": "pending.txt"},
			}
			sid, addr := seedApprovalReplay1397(t, store, call)
			entry, accepted := reg.requestApproval(string(call.ID), call.Tool, call.Parameters, "mia", sid, "turn-1397")
			require.True(t, accepted)
			for range 2 {
				frames := attachApprovalReplay1397(t, srv, sid)
				assert.Equal(t, []string{"start:finished-call", "result:finished-call:success", "start:pending-call"}, frames.events)
				require.Len(t, frames.state.PendingApprovals, 1, "reconnect must re-offer the live approval")
				pending := frames.state.PendingApprovals[0]
				assert.Equal(t, entry.ApprovalID, pending.ApprovalId)
				assert.Equal(t, sid, pending.SessionId)
				assert.Equal(t, "mia", pending.AgentId)
				assert.Equal(t, call.Tool, pending.ToolName)
				assert.Positive(t, pending.ExpiresInMs)
				require.Len(t, reg.pendingApprovals(), 1, "replay must not resolve the approval")
				assert.Equal(t, entry.ApprovalID, reg.pendingApprovals()[0].ApprovalID)
			}

			// Use the exact approval id recovered from the reconnect snapshot.
			body, err := json.Marshal(generated.ToolApprovalActionRequest{Action: action})
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			api := &restAPI{agentLoop: al, approvalReg: reg}
			api.HandleToolApprovals(w, withAdminRole(r))
			require.Equal(t, http.StatusOK, w.Code, "approval from the reloaded card is answerable: %s", w.Body.String())
			var response generated.ToolApprovalResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, generated.ToolApprovalResponse{
				ApprovalId: entry.ApprovalID, Action: generated.ToolApprovalResponseAction(action), Status: "ok",
			}, response)
			wantOutcome := ApprovalOutcome{Approved: true, Reason: "approved"}
			call.Status = "success"
			call.Result = map[string]any{"text": "approved result"}
			wireStatus := "success"
			if action == "deny" {
				wantOutcome = ApprovalOutcome{Reason: "user"}
				call.Status = "denied"
				call.Result = map[string]any{"error": true, "reason": "user", "text": "Not run: denied by the operator."}
				wireStatus = "error"
			}
			assert.Equal(t, wantOutcome, apprResAwaitOutcome(t, entry), "the original waiting call receives the decision")
			assert.Empty(t, reg.pendingApprovals())
			// The real transcript settle operation replaces pending with a real
			// outcome; replay must now emit exactly one completion for that id.
			_, err = store.SettleToolCall(sid, addr, "pending", call)
			require.NoError(t, err)
			settled := attachApprovalReplay1397(t, srv, sid)
			assert.Equal(t, []string{"start:finished-call", "result:finished-call:success", "start:pending-call", "result:pending-call:" + wireStatus}, settled.events)
			require.Len(t, settled.results, 2)
			assert.Equal(t, call.Result, settled.results[1].Result)
			assert.Empty(t, settled.state.PendingApprovals)
		})
	}
}

// seedApprovalReplay1397 writes the same display records replay consumes from
// recordAskPendingToolCall: a finished sibling and a separate pending tool_call
// in the same turn. No model or tool execution is mocked into the replay path.
func seedApprovalReplay1397(t *testing.T, store *session.UnifiedStore, call session.ToolCall) (string, session.ArchiveAddress) {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", TurnID: "turn-1397",
		ToolCalls: []session.ToolCall{{
			ID: "finished-call", Tool: "read_file", Status: "success", DurationMS: 9,
			Parameters: map[string]any{"path": "finished.txt"}, Result: map[string]any{"text": "saved-result"},
		}},
	}))
	addr, err := store.AppendTranscriptAddressed(meta.ID, session.TranscriptEntry{
		Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", TurnID: "turn-1397",
		ToolCalls: []session.ToolCall{call},
	})
	require.NoError(t, err)
	return meta.ID, addr
}

type approvalReplay1397Frames struct {
	events  []string
	starts  []generated.ToolCallStartFrame
	results []generated.ToolCallResultFrame
	state   generated.SessionStateFrame
}

// attachApprovalReplay1397 opens a fresh socket, as a page reload does. Only
// production ServeHTTP/attach/replay code produces the frames under assertion.
func attachApprovalReplay1397(t *testing.T, srv *httptest.Server, sid string) approvalReplay1397Frames {
	t.Helper()
	conn := dialTestWS(t, srv)
	defer conn.Close()
	require.NoError(t, conn.WriteJSON(generated.AuthFrame{Type: "auth", Token: "dev-token"}))
	require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	var out approvalReplay1397Frames
	var snapshots, states, done int
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "attach did not complete; events=%v", out.events)
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &envelope))
		var kind string
		require.NoError(t, json.Unmarshal(envelope["type"], &kind))
		switch kind {
		case "session_snapshot":
			snapshots++
		case "session_state":
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			if state.SessionId != nil && *state.SessionId == sid {
				out.state = state
				states++
			}
		case "tool_call_start":
			var frame generated.ToolCallStartFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			out.starts = append(out.starts, frame)
			out.events = append(out.events, "start:"+frame.CallId)
		case "tool_call_result":
			var frame generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			out.results = append(out.results, frame)
			out.events = append(out.events, "result:"+frame.CallId+":"+frame.Status)
		case "subagent_start", "subagent_end", "error":
			out.events = append(out.events, kind)
		case "done":
			done++
		case "catch_up_complete":
			require.Equal(t, 1, snapshots, "a fresh attach must exercise transcript replay, not incremental catch-up")
			require.Equal(t, 1, states, "the bound session must have one authoritative state snapshot")
			require.Equal(t, 1, done, "the transcript replay must finish exactly once")
			return out
		}
	}
}

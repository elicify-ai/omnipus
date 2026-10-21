// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Promoted from the retained release probe. The oracle is the founder's
// dispatch: START does not cover RESULT; each uncovered finished result is
// sent once. Projection publication, snapshot and coverage recording are real.
func TestReplayStartDoesNotSuppressFinishedProjection(t *testing.T) {
	const sid = "session-projection-release"
	start := generated.ToolCallStartFrame{
		Type: "tool_call_start", SessionId: sid, CallId: "call-projection", Tool: "read_file",
		Params: map[string]any{"path": "finished.txt"},
	}
	startRaw, err := json.Marshal(start)
	require.NoError(t, err)
	for _, status := range []string{"success", "error"} {
		t.Run(status, func(t *testing.T) {
			end := generated.ToolCallResultFrame{
				Type: "tool_call_result", SessionId: sid, CallId: start.CallId, Tool: start.Tool,
				Result: map[string]any{"text": "real finished result"}, Status: status,
			}
			endRaw, err := json.Marshal(end)
			require.NoError(t, err)
			otherStart, otherEnd := start, end
			otherStart.CallId, otherEnd.CallId = "other-call", "other-call"
			for _, tc := range []struct {
				name       string
				replay     []any
				noStart    bool
				unfinished bool
				want       [][]byte
			}{
				{name: "no transcript coverage emits both", want: [][]byte{startRaw, endRaw}},
				{name: "replayed start still needs finished result", replay: []any{start}, want: [][]byte{endRaw}},
				{name: "replayed start and result emit neither", replay: []any{start, end}},
				{name: "result coverage does not cover start", replay: []any{end}, want: [][]byte{startRaw}},
				{name: "replayed start anchors a projected result without start bytes", replay: []any{start}, noStart: true, want: [][]byte{endRaw}},
				{name: "orphan result remains suppressed", noStart: true},
				{name: "pending call emits start only", unfinished: true, want: [][]byte{startRaw}},
				{name: "pending call already started emits nothing", unfinished: true, replay: []any{start}},
				{name: "unrelated coverage cannot suppress this call", replay: []any{otherStart, otherEnd}, want: [][]byte{startRaw, endRaw}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var projection activeTurnProjection
					if !tc.noStart {
						projection.update(hubFrameMeta{kind: hubKindToolStart, key: start.CallId, turnID: "active-turn"}, startRaw, sid)
					}
					if !tc.unfinished {
						projection.update(hubFrameMeta{kind: hubKindToolResult, key: start.CallId}, endRaw, sid)
					}
					require.True(t, projection.active(), "no turn done was published; the item is still projected")
					emitted := make(replayCoverage)
					for _, frame := range tc.replay {
						recordEmittedIDs(emitted, frame)
					}
					got := projectionFrames(sid, projection.snapshot(), emitted)
					assert.Equal(t, tc.want, got,
						"a replayed START must not suppress an uncovered terminal result; finished = exactly one result")
				})
			}
		})
	}
}

// A real socket attach merges a transcript's START with a genuinely finished
// tool still in the active hub projection. Reconnect before and after the
// transcript settles must rebuild exactly one result, never two or none.
func TestAttachProjectionCoverageReconnect(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(h.Wait)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	for _, status := range []string{"success", "error"} {
		t.Run(status, func(t *testing.T) {
			call := session.ToolCall{
				ID: "pending-call", Tool: "read_file", Status: "pending",
				Parameters: map[string]any{"path": "pending.txt"},
			}
			sid, addr := seedApprovalReplay1397(t, store, call)
			h.hubSyncTap(agent.Event{
				Kind: agent.EventKindToolExecStart, Meta: agent.EventMeta{TurnID: "turn-1397"},
				Payload: agent.ToolExecStartPayload{
					ToolCallID: call.ID, SessionID: sid, Tool: call.Tool, Arguments: call.Parameters, AgentID: "mia",
				},
			})
			pending := attachApprovalReplay1397(t, srv, sid)
			assert.Equal(t, []string{"start:finished-call", "result:finished-call:success", "start:pending-call"}, pending.events,
				"the unfinished projection must not change #1397's START-only replay")

			// Publish the real terminal event without settling the transcript yet:
			// this is the active-turn snapshot boundary that used to lose RESULT.
			const resultText = "finished in active turn"
			h.hubSyncTap(agent.Event{
				Kind: agent.EventKindToolExecEnd, Meta: agent.EventMeta{TurnID: "turn-1397"},
				Payload: agent.ToolExecEndPayload{
					ToolCallID: call.ID, SessionID: sid, Tool: call.Tool, AgentID: "mia",
					Result: resultText, IsError: status == "error", Duration: 17 * time.Millisecond,
				},
			})
			wantEvents := []string{"start:finished-call", "result:finished-call:success", "start:pending-call", "result:pending-call:" + status}
			for range 2 {
				frames := attachApprovalReplay1397(t, srv, sid)
				assert.Equal(t, wantEvents, frames.events, "every fresh snapshot includes exactly one finished result")
				require.Len(t, frames.results, 2, "one finished sibling and one projected result")
				assert.Equal(t, resultText, frames.results[1].Result)
				require.NotNil(t, frames.results[1].DurationMs)
				assert.Equal(t, 17, *frames.results[1].DurationMs)
			}
			assertProjectionIncremental1397(t, srv, h, sid)

			call.Status = status
			call.DurationMS = 17
			call.Result = map[string]any{"text": resultText}
			_, err := store.SettleToolCall(sid, addr, "pending", call)
			require.NoError(t, err)
			for range 2 {
				frames := attachApprovalReplay1397(t, srv, sid)
				assert.Equal(t, wantEvents, frames.events, "a settled replay result must not be repeated from projection")
				require.Len(t, frames.results, 2)
				assert.Equal(t, call.Result, frames.results[1].Result)
			}
			assertProjectionIncremental1397(t, srv, h, sid)
		})
	}
}

// A reconnect at the already observed head is incremental: no snapshot wipe,
// no replayed START or RESULT. Repeat it to pin idempotence at the same cursor.
func assertProjectionIncremental1397(t *testing.T, srv *httptest.Server, h *WSHandler, sid string) {
	t.Helper()
	head := int64(h.hubs.getOrCreate(sid).snapshotHead())
	boot := h.hubs.bootID
	for range 2 {
		conn := dialTestWS(t, srv)
		t.Cleanup(func() { _ = conn.Close() })
		require.NoError(t, conn.WriteJSON(generated.AuthFrame{Type: "auth", Token: "dev-token"}))
		require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{
			Type: "attach_session", SessionId: sid, BootId: &boot, SinceSeq: &head,
		}))
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
		var toolFrames, snapshots int
		for {
			_, raw, err := conn.ReadMessage()
			require.NoError(t, err)
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &envelope))
			var kind string
			require.NoError(t, json.Unmarshal(envelope["type"], &kind))
			switch kind {
			case "tool_call_start", "tool_call_result":
				toolFrames++
			case "session_snapshot":
				snapshots++
			case "error":
				t.Fatalf("incremental reconnect failed: %s", raw)
			case "catch_up_complete":
				var frame generated.CatchUpCompleteFrame
				require.NoError(t, json.Unmarshal(raw, &frame))
				assert.Equal(t, "incremental", frame.Mode)
				assert.Equal(t, head, frame.Seq)
				assert.Zero(t, snapshots, "a valid cursor must not rebuild the conversation")
				assert.Zero(t, toolFrames, "already observed results must not duplicate on incremental reconnect")
				require.NoError(t, conn.Close())
				goto nextReconnect
			}
		}
	nextReconnect:
	}
}

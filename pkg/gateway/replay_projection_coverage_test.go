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
	for _, launchTool := range []string{"delegate", "create_task"} {
		t.Run("active "+launchTool, func(t *testing.T) {
			assertActiveLaunchProjection1397(t, h, srv, store, launchTool)
		})
	}
}

// A launch acknowledgement is not the child's completion. Keep its real open
// span and retained acknowledgement in the same snapshot as an ordinary call
// whose finished result is only in the projection. Neither result may be guessed
// from START alone: the launch stays withheld, the ordinary result appears once.
func assertActiveLaunchProjection1397(t *testing.T, h *WSHandler, srv *httptest.Server, store *session.UnifiedStore, tool string) {
	t.Helper()
	child, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ackBytes, err := json.Marshal(map[string]any{"session_id": child.ID})
	require.NoError(t, err)
	ackText := string(ackBytes)
	launch := session.ToolCall{
		ID: "launch-call", Tool: tool, Status: "success",
		Parameters: map[string]any{"task": "research", "label": "research"},
		Result:     map[string]any{"text": ackText},
	}
	sid, _ := seedApprovalReplay1397(t, store, launch)
	spanID := agent.SubagentSpanID(string(launch.ID), 1)
	start := subagentStartEntryFull(spanID, string(launch.ID), "research", "mia", child.ID)
	start.SubagentStart.SessionId = sid
	require.NoError(t, store.AppendTranscript(sid, start))
	ordinary := session.ToolCall{
		ID: "ordinary-call", Tool: "read_file", Status: "pending",
		Parameters: map[string]any{"path": "ordinary.txt"},
	}
	addr, err := store.AppendTranscriptAddressed(sid, session.TranscriptEntry{
		Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", TurnID: "turn-1397",
		ToolCalls: []session.ToolCall{ordinary},
	})
	require.NoError(t, err)
	publish := func(call session.ToolCall, result string) {
		h.hubSyncTap(agent.Event{
			Kind: agent.EventKindToolExecStart, Meta: agent.EventMeta{TurnID: "turn-1397"},
			Payload: agent.ToolExecStartPayload{
				ToolCallID: call.ID, SessionID: sid, Tool: call.Tool, Arguments: call.Parameters, AgentID: "mia",
			},
		})
		h.hubSyncTap(agent.Event{
			Kind: agent.EventKindToolExecEnd, Meta: agent.EventMeta{TurnID: "turn-1397"},
			Payload: agent.ToolExecEndPayload{
				ToolCallID: call.ID, SessionID: sid, Tool: call.Tool, Result: result, AgentID: "mia",
			},
		})
	}
	const ordinaryText = "ordinary call genuinely finished"
	publish(launch, ackText)
	publish(ordinary, ordinaryText)
	wantOpen := []string{
		"start:finished-call", "result:finished-call:success", "start:launch-call", "subagent_start",
		"start:ordinary-call", "result:ordinary-call:success",
	}
	for range 2 {
		frames := attachApprovalReplay1397(t, srv, sid)
		assert.Equal(t, wantOpen, frames.events, "projection must not restore an intentionally withheld launch acknowledgement")
		require.Len(t, frames.results, 2, "only the two genuinely finished ordinary calls have results")
		assert.Equal(t, "finished-call", frames.results[0].CallId)
		assert.Equal(t, "ordinary-call", frames.results[1].CallId)
		assert.Equal(t, ordinaryText, frames.results[1].Result)
	}

	// Persist the ordinary result before the real parent TurnEnd clears the
	// projection. The child outlives that turn, so its launch must stay running.
	ordinary.Status = "success"
	ordinary.Result = map[string]any{"text": ordinaryText}
	_, err = store.SettleToolCall(sid, addr, "pending", ordinary)
	require.NoError(t, err)
	h.hubSyncTap(agent.Event{Kind: agent.EventKindTurnEnd, Meta: agent.EventMeta{TurnID: "turn-1397"}})
	hub := h.hubs.getOrCreate(sid)
	hub.mu.Lock()
	active := hub.proj.active()
	hub.mu.Unlock()
	require.False(t, active, "the parent TurnEnd must actually clear the retained acknowledgement")
	afterParent := attachApprovalReplay1397(t, srv, sid)
	assert.Equal(t, wantOpen, afterParent.events, "parent completion must not flip an active child's launch badge")
	require.Len(t, afterParent.results, 2)
	assert.Equal(t, ordinary.Result, afterParent.results[1].Result)

	// Withholding is per snapshot, not sticky. Once the child's persisted span
	// closes, the real replay terminal result becomes visible exactly once.
	end := subagentEndEntry(spanID, "success", 23)
	end.SubagentEnd.SessionId = sid
	parentID := string(launch.ID)
	end.SubagentEnd.ParentCallId = &parentID
	require.NoError(t, store.AppendTranscript(sid, end))
	closed := attachApprovalReplay1397(t, srv, sid)
	assert.Equal(t, []string{
		"start:finished-call", "result:finished-call:success", "start:launch-call", "result:launch-call:success",
		"subagent_start", "start:ordinary-call", "result:ordinary-call:success", "subagent_end",
	}, closed.events)
	require.Len(t, closed.results, 3)
	assert.Equal(t, launch.Result, closed.results[1].Result)
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

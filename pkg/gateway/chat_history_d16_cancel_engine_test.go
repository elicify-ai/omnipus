package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type historyD16CancelMode struct{ tree, late, overlap bool }
type historyD16Stage struct{ session, name string }

func historyD16Await(t *testing.T, signal <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(30 * time.Second):
		t.Fatalf("D16 real boundary did not complete: %s", why)
	}
}

func historyD16StageAwait(t *testing.T, stages <-chan historyD16Stage, sid, want string) {
	t.Helper()
	select {
	case stage := <-stages:
		require.Equal(t, historyD16Stage{sid, want}, stage, "genuine Stop stages, not a stopwatch estimate")
	case <-time.After(30 * time.Second):
		t.Fatalf("D16 actual Stop stage %s was not observed", want)
	}
}

func historyD16RequestedCall(id, step string) providers.ToolCall {
	args := map[string]any{"step": step}
	encoded, err := json.Marshal(args)
	if err != nil {
		panic(err) // fixed string-only process-edge fixture, never product error handling
	}
	return providers.ToolCall{ID: id, Type: "function", Name: historyD16ToolName, Arguments: args,
		Function: &providers.FunctionCall{Name: historyD16ToolName, Arguments: string(encoded)}}
}

func historyD16CancelProvider() *historyD16Provider {
	return &historyD16Provider{replies: []historyD16Reply{
		{calls: []providers.ToolCall{historyD16RequestedCall(historyD16OtherID, "completed")}},
		{content: "D16 unrelated session completed", usage: providers.UsageInfo{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}},
		{content: "D16 original target reply before Stop", calls: []providers.ToolCall{
			historyD16RequestedCall(historyD16CompletedID, "completed"),
			historyD16RequestedCall(historyD16CancelledID, "blocked"),
			historyD16RequestedCall(historyD16QueuedID, "queued"),
		}},
	}}
}

func historyD16NativeStop(t *testing.T, h *historyD16Harness, tool *historyD16Tool, sid, turn string, mode historyD16CancelMode) {
	t.Helper()
	stages := make(chan historyD16Stage, 8)
	result, err := h.loop.StopSession(context.Background(), agent.StopRequest{
		SessionID: sid, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "d16-disposable-human"}, Channel: "webchat", Tree: mode.tree,
		HooksFor: func(string) agent.CancelHooks {
			return agent.CancelHooks{SendStageFrame: func(sessionID, stage string) { stages <- historyD16Stage{sessionID, stage} }}
		},
	})
	require.NoError(t, err, "public Stop operation accepted the real running session")
	require.NoError(t, result.RootErr)
	require.True(t, result.Fired, "Stop must select the actual running tool turn")
	historyD16StageAwait(t, stages, sid, "graceful")
	historyD16Await(t, tool.cancelled, "forced Stop reached the real external tool context")
	historyD16StageAwait(t, stages, sid, "hard")
	if mode.late {
		historyD16StageAwait(t, stages, sid, "detached")
		// The actual execution is still blocked. Abandonment must not lose its
		// already-visible display identity while waiting for an untrusted return.
		historyD16CanonicalCalls(t, h.store, sid, []historyD16CallWant{{historyD16CancelledID, "blocked", "cancelled", turn}})
	}
	tool.unblock()
	historyD16Await(t, tool.finished, "owned external execution released and returned")
	end := h.event(func(event agent.Event) bool {
		payload, ok := event.Payload.(agent.TurnEndPayload)
		return event.Kind == agent.EventKindTurnEnd && ok && payload.SessionID == sid
	})
	endPayload, endPayloadOK := end.Payload.(agent.TurnEndPayload)
	require.True(t, endPayloadOK, "actual turn-end event has the required payload type")
	assert.Equal(t, agent.TurnEndStatusAborted, endPayload.Status)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.True(t, h.loop.WaitForActiveRequestsContext(ctx), "all actual turn work joined before canonical proof")
}

func historyD16HeldCancellation(t *testing.T, frames [][]byte) {
	t.Helper()
	complete, held := -1, 0
	var highWater int64
	for i, raw := range frames {
		switch historyD16Type(t, raw) {
		case "catch_up_complete":
			var done generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(raw, &done))
			complete, highWater = i, done.Seq
		case "tool_call_result":
			var result generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(raw, &result))
			if result.CallId != historyD16CancelledID || result.HistoryOrigin != nil {
				continue
			}
			held++
			assert.Equal(t, "cancelled", result.Status, "real held terminal event retains confirmed cancellation")
			require.NotNil(t, result.Seq)
			assert.GreaterOrEqual(t, complete, 0, "held event may not overtake the completion boundary")
			assert.Greater(t, i, complete)
			assert.Greater(t, *result.Seq, highWater)
		}
	}
	assert.Equal(t, 1, held, "FR-026: one genuine cancelled terminal event survives bind/hold, no fake error or missing result")
}

func historyD16EngineCancellation(t *testing.T, mode historyD16CancelMode) {
	home, cfg := historyD16Config(t)
	// Use the supported eager manifest so this registered process-edge probe
	// is actually offered. Policy filtering and the offered-tool guard stay
	// real; lazy-tool discovery is not the cancellation unit under test.
	cfg.Tools.Manifest.Compressed = false
	cfg.Sandbox.ToolPolicies[historyD16ToolName] = "allow"
	provider := historyD16CancelProvider()
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	tool := newHistoryD16Tool(mode.late)
	h.loop.RegisterTool(tool)
	inst, ok := h.loop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{historyD16ToolName: "allow"}})
	// Cleanup can never strand our external execution if an earlier assertion
	// fails: cancel the real engine context, then release our process edge.
	t.Cleanup(func() { h.cancel(); tool.unblock() })
	other := h.session()
	peer := h.connect()
	h.attach(peer, other)
	h.send(peer, other, "d16-other-real-instruction", "D16 complete the harmless control tool")
	h.turnDone(peer)
	otherEnd := h.event(func(event agent.Event) bool {
		payload, valid := event.Payload.(agent.TurnEndPayload)
		return event.Kind == agent.EventKindTurnEnd && valid && payload.SessionID == other && payload.Status == agent.TurnEndStatusCompleted
	})
	otherWant := []historyD16CallWant{{historyD16OtherID, "completed", "success", otherEnd.Meta.TurnID}}
	historyD16CanonicalCalls(t, h.store, other, otherWant)
	otherBefore := pagingCanonicalBytes(t, h.store, other)
	sid := h.session()
	h.attach(peer, sid)
	h.send(peer, sid, "d16-target-real-instruction", "D16 complete one call, start another, leave the third queued")
	historyD16Await(t, tool.started, "native tool dispatch actually entered its blocked execution")
	started := h.event(func(event agent.Event) bool {
		payload, valid := event.Payload.(agent.ToolExecStartPayload)
		return event.Kind == agent.EventKindToolExecStart && valid && payload.SessionID == sid && string(payload.ToolCallID) == historyD16CancelledID
	})
	payload, payloadOK := started.Payload.(agent.ToolExecStartPayload)
	require.True(t, payloadOK, "actual tool-start event has the required payload type")
	require.Equal(t, historyD16ToolName, payload.Tool)
	require.Equal(t, map[string]any{"step": "blocked"}, payload.Arguments)
	require.Equal(t, "mia", payload.AgentID)
	require.NotEmpty(t, started.Meta.TurnID)
	require.NotEmpty(t, started.Meta.SessionKey)
	// Prove the post-Stop model-admission check reads the real target window,
	// not an empty/wrong key that could make every exclusion falsely pass.
	acceptedInputs := 0
	for _, message := range h.store.GetHistory(started.Meta.SessionKey) {
		if message.Role == "user" && strings.Contains(message.Content, "D16 complete one call, start another, leave the third queued") {
			acceptedInputs++
		}
	}
	require.Equal(t, 1, acceptedInputs, "instrument: native event key exposes the actual accepted model input before Stop")
	live := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != "tool_call_start" {
			return false
		}
		var frame generated.ToolCallStartFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.CallId == historyD16CancelledID
	})
	require.NotEmpty(t, live, "actual hub/socket row exists before Stop")
	var overlap [][]byte
	if mode.overlap {
		setAttachHook(t, func(attached string) {
			if attached == sid {
				historyD16NativeStop(t, h, tool, sid, started.Meta.TurnID, mode)
			}
		})
		wc := &wsConn{sendCh: make(chan []byte, 1024), doneCh: make(chan struct{})}
		h.handler.mu.Lock()
		h.handler.sessions["d16-cancel-overlap"] = wc
		h.handler.mu.Unlock()
		h.handler.handleAttachSession(context.Background(), "d16-cancel-overlap", sid, nil, wc)
		for _, frame := range pagingDrain(t, wc) {
			overlap = append(overlap, frame.raw)
		}
		historyD16HeldCancellation(t, overlap)
		setAttachHook(t, nil)
	} else {
		historyD16NativeStop(t, h, tool, sid, started.Meta.TurnID, mode)
	}
	historyD16EngineCancellationReadback(t, h, tool, provider, sid, other, started.Meta.TurnID, started.Meta.SessionKey, otherWant, otherBefore, overlap)
}

func TestCancelledTool_ReloadPageSnapshotParity(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode historyD16CancelMode
	}{
		{"actual_Stop_hard_abort_preserves_original_calls", historyD16CancelMode{}},
		{"actual_Stop_all_preserves_unrelated_session", historyD16CancelMode{tree: true}},
		{"abandoned_execution_late_success_cannot_replace_cancelled", historyD16CancelMode{late: true}},
		{"actual_Stop_during_bind_holds_one_truthful_terminal_event", historyD16CancelMode{overlap: true}},
	} {
		t.Run(tc.name, func(t *testing.T) { historyD16EngineCancellation(t, tc.mode) })
	}
	t.Run("saved_cancelled_projection_keeps_generated_status_and_metadata_zero", historyD16SavedCancellation)
	t.Run("older_page_edge_uses_outside_terminal_effect_without_orphans", historyD16CancellationPageEdge)
}

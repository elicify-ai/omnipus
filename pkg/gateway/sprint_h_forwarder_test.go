// Sprint H WebSocket event forwarder tests — FR-H-004, FR-H-005, FR-H-011
// Traces to: sprint-h-subagent-block-spec.md TDD rows 5, 6, 7.

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// makeForwarderTestConn creates a wsConn wired to a buffered send channel for tests.
// The done channel is pre-created.
func makeForwarderTestConn(bufSize int) (*wsConn, chan []byte) {
	ch := make(chan []byte, bufSize)
	wc := &wsConn{
		sendCh: ch,
		doneCh: make(chan struct{}),
	}
	return wc, ch
}

// drainFrame reads one marshaled frame from the send channel and unmarshals it.
func drainFrame(t *testing.T, ch chan []byte) replayFrameDecoder {
	t.Helper()
	select {
	case data := <-ch:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(data, &f), "frame must be valid JSON")
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame on send channel")
		return replayFrameDecoder{}
	}
}

// runForwarder runs eventForwarder against a live EventBus subscription until
// the subscription is closed. Returns the done channel so callers can wait.
func runForwarder(h *WSHandler, wc *wsConn, chatID string, bus *agent.EventBus) chan struct{} {
	sub := bus.Subscribe(64)
	doneCh := make(chan struct{})
	go h.eventForwarder(wc, chatID, sub, doneCh)
	return doneCh
}

// attachHubTestBus is the #823 Lane A replacement for runForwarder in the
// sub-agent span tests: span frames and the orphan watchdog now live in the
// session hub, fed synchronously by the EventBus sync tap, not in a
// per-connection eventForwarder. It installs h.hubSyncTap on bus and binds
// wc under chatID to a session, the way a real connection's attach does, so
// events carrying only the chat id resolve to that session's hub.
func attachHubTestBus(h *WSHandler, bus *agent.EventBus, chatID string, wc *wsConn) {
	bus.SetSyncTap(h.hubSyncTap)
	bindTestConnToSession(h, chatID, "hub-test-session:"+chatID, wc)
}

// makeMinimalHandler builds a WSHandler with no real dependencies set.
// eventForwarder only uses h.mu and h.taskChatIDs from the handler struct.
//
// #823 catch-up redesign: also initializes hubs, mirroring newWSHandler,
// so tests can call h.hubSyncTap(evt) directly (the migrated equivalent of
// runForwarder+bus.Emit for the event kinds now routed through the session
// hub — see websocket_forward_hub.go). Harmless for every pre-existing
// caller: nothing touched this field before this change, so no existing
// test's behavior changes.
func makeMinimalHandler() *WSHandler {
	return &WSHandler{
		sessions:    make(map[string]*wsConn),
		sessionIDs:  make(map[string]string),
		taskChatIDs: make(map[string]string),
		hubs:        newHubRegistry(newHubBootID()),
	}
}

// TestSpawn_SubTurnStart_EmitsSubagentStart verifies FR-H-004:
// EventKindSubTurnSpawn → "subagent_start" frame with span_id, parent_call_id,
// task_label, and agent_id.
// Traces to: sprint-h-subagent-block-spec.md TDD row 5, BDD Scenario 1.
func TestSpawn_SubTurnStart_EmitsSubagentStart(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	attachHubTestBus(h, bus, "chat-1", wc)

	// Emit a SubTurnSpawn event with the ChatID matching the connection.
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "max",
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "audit go files",
			ChatID:            "chat-1",
		},
	})

	// Close bus so the forwarder terminates.
	bus.Close()

	// Exactly one frame must have been sent.
	require.Len(t, ch, 1, "exactly one frame must be emitted for SubTurnSpawn")

	frame := drainFrame(t, ch)
	assert.Equal(t, "subagent_start", frame.Type, "frame type must be subagent_start")
	assert.Equal(t, "span_c1", frame.SpanID, "span_id must be span_c1")
	assert.Equal(t, "c1", frame.ParentCallID, "parent_call_id must be c1")
	assert.Equal(t, "audit go files", frame.TaskLabel, "task_label must be propagated")
	assert.Equal(t, "max", frame.AgentID, "agent_id must be propagated")
}

// TestSpawn_SubTurnEnd_EmitsSubagentEnd verifies FR-H-004:
// EventKindSubTurnEnd → "subagent_end" frame with span_id, status, duration_ms.
// Traces to: sprint-h-subagent-block-spec.md TDD row 6, BDD Scenarios 3 & 6.
func TestSpawn_SubTurnEnd_EmitsSubagentEnd(t *testing.T) {
	for _, tc := range []struct {
		status agent.SubTurnStatus
		err    bool
	}{
		{status: agent.SubTurnStatusSuccess, err: false},
		{status: agent.SubTurnStatusError, err: true},
	} {
		t.Run("status="+string(tc.status), func(t *testing.T) {
			bus := agent.NewEventBus()
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(64)
			attachHubTestBus(h, bus, "chat-1", wc)

			bus.Emit(agent.Event{
				Kind: agent.EventKindSubTurnEnd,
				Payload: agent.SubTurnEndPayload{
					AgentID:           "max",
					Status:            tc.status,
					SpanID:            "span_c1",
					ParentSpawnCallID: session.ToolCallID("c1"),
					DurationMS:        4210,
					ChatID:            "chat-1",
				},
			})

			bus.Close()

			require.Len(t, ch, 1, "exactly one frame must be emitted for SubTurnEnd")
			frame := drainFrame(t, ch)
			assert.Equal(t, "subagent_end", frame.Type)
			assert.Equal(t, "span_c1", frame.SpanID)
			assert.Equal(t, "c1", frame.ParentCallID)
			assert.Equal(t, string(tc.status), frame.Status)
			assert.Equal(t, int64(4210), frame.DurationMs)
		})
	}
}

// TestToolExecStart_CarriesParentCallID verifies FR-H-005:
// tool_call_start frames fired inside a sub-turn carry parent_call_id.
// Traces to: sprint-h-subagent-block-spec.md TDD row 4, BDD Scenario 2.
// #823 catch-up redesign: migrated from runForwarder+bus.Emit (the retired
// per-connection eventForwarder path, websocket_forward.go's onToolExecStart)
// to h.hubSyncTap directly — EventKindToolExecStart is now translated and
// delivered by the session hub (websocket_forward_hub.go's hubToolExecStart),
// exactly once per event. The FR-H-005 parent_call_id assertions this test
// pins are unchanged — hubToolExecStart's body was ported verbatim from
// onToolExecStart.
func TestToolExecStart_CarriesParentCallID(t *testing.T) {
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-1", "chat-1", wc)

	// Emit a ToolExecStart event that has a non-empty ParentSpawnCallID (inside a sub-turn).
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID:        session.ToolCallID("t1"),
			ChatID:            "chat-1",
			SessionID:         "chat-1",
			Tool:              "fs.list",
			Arguments:         map[string]any{"path": "/tmp"},
			ParentSpawnCallID: session.ToolCallID("c1"),
		},
	})

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_start", frame.Type)
	assert.Equal(t, "t1", frame.CallID)
	assert.Equal(t, "c1", frame.ParentCallID,
		"parent_call_id must be propagated from ParentSpawnCallID (FR-H-005)")
}

// TestToolExecStart_NoParentCallID_TopLevel verifies FR-H-005 negative case:
// top-level tool calls (empty ParentSpawnCallID) must NOT carry parent_call_id.
// #823 catch-up redesign: migrated to h.hubSyncTap, see the sibling test's
// comment above.
func TestToolExecStart_NoParentCallID_TopLevel(t *testing.T) {
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-1", "chat-1", wc)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: session.ToolCallID("t2"),
			ChatID:     "chat-1",
			SessionID:  "chat-1",
			Tool:       "shell",
			// ParentSpawnCallID is empty — top-level call.
		},
	})

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_start", frame.Type)
	assert.Empty(t, frame.ParentCallID,
		"top-level tool calls must not carry parent_call_id")
}

// TestSpawn_SubTurnOutlivesParentTurn_NeverSynthesizesInterrupted proves
// ADR-091 UAT defect 2's fix: the "parent_done_early" orphan watchdog
// (FR-H-004 Scenario 7, from the pre-ADR-091 design where a nested sub-turn
// was structurally scoped to its parent's own turn) is retired. Under
// ADR-091 D1 a delegated child is a session of its own, designed to keep
// running after its parent's turn ends — the founder's round-9 decision —
// and its real terminal state arrives independently via its own
// EventKindSubTurnEnd (steer_frames.go's deliverSubagentEnd, driven by the
// child's own steer.Outcome). The parent's turn ending is no longer
// evidence of anything wrong with a still-open span; synthesizing
// subagent_end{status:"interrupted"} here fabricated a false failure on
// every single healthy delegation — the exact UAT symptom ("1 failed" while
// the child worked normally, self-correcting only once the real end frame
// eventually arrived and overwrote it).
// Traces to: ADR-091 UAT defect 2 (the tester's three-way proof: a child
// marked "interrupted" this way went on to spawn a grandchild, another kept
// working three more minutes and finished, and the durable transcript
// recorded subagent_end{status:"success"} for both).
func TestSpawn_SubTurnOutlivesParentTurn_NeverSynthesizesInterrupted(t *testing.T) {
	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	attachHubTestBus(h, bus, "chat-1", wc)

	// 1. A sub-turn (an ADR-091 steered child) starts.
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "max",
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "long-running delegated work",
			ChatID:            "chat-1",
		},
	})

	// 2. The PARENT's own turn ends normally — ADR-091 D1: the child is
	//    designed to keep running past this point; this must arm nothing.
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnEnd,
		Payload: agent.TurnEndPayload{
			Status: agent.TurnEndStatusCompleted,
			ChatID: "chat-1",
			IsRoot: true,
		},
	})

	// 3. The child keeps working well past what used to be the watchdog's
	//    60s default (shortened here only by NOT waiting for it — there is
	//    no timer left to wait for; this sleep proves none fires).
	time.Sleep(150 * time.Millisecond)

	// 4. The child finishes for real, long after the parent's turn ended.
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnEnd,
		Payload: agent.SubTurnEndPayload{
			AgentID:           "max",
			Status:            agent.SubTurnStatusSuccess,
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			DurationMS:        150,
			ChatID:            "chat-1",
		},
	})

	bus.Close()

	var frames []replayFrameDecoder
	for len(ch) > 0 {
		frames = append(frames, drainFrame(t, ch))
	}

	var sawInterrupted, sawRealEnd bool
	for _, f := range frames {
		if f.Type != "subagent_end" {
			continue
		}
		if f.Status == "interrupted" {
			sawInterrupted = true
		}
		if f.Status == "success" {
			sawRealEnd = true
		}
	}
	assert.False(t, sawInterrupted,
		"a parent turn ending must NEVER synthesize subagent_end{interrupted} for a still-running "+
			"ADR-091 child — a child is designed to outlive its parent's turn (D1)")
	assert.True(t, sawRealEnd, "the child's own real subagent_end{success} must still be forwarded")
}

// TestToolExecEnd_DelegationDenied_SubstitutesStructuredResult proves the UAT
// fix wiring in eventForwarder: a tool result with status=="error" whose Result
// is a structured delegation_denied payload is substituted — frame.Result
// becomes the parsed object (not the raw string) and frame.Error carries the
// human-readable reason. Mirrors the forwarder tests above.
// #823 catch-up redesign: migrated from runForwarder+bus.Emit to
// h.hubSyncTap directly — EventKindToolExecEnd now goes through the session
// hub (websocket_forward_hub.go's hubToolExecEnd, ported verbatim from the
// retired onToolExecEnd), exactly once per event.
func TestToolExecEnd_DelegationDenied_SubstitutesStructuredResult(t *testing.T) {
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-1", "sess-1", wc)

	denial := `{"error":"delegation_denied","reason":"target untrusted","policy":"trust_set","tool":"spawn","target_agent_id":"evil"}`
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			ToolCallID: session.ToolCallID("call-deny"),
			ChatID:     "chat-1",
			SessionID:  "sess-1",
			Tool:       "spawn",
			IsError:    true,
			Result:     denial,
		},
	})

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_result", frame.Type)
	assert.Equal(t, "error", frame.Status, "denied delegation must be an error result")
	assert.Equal(t, "target untrusted", frame.Error,
		"frame.error must carry the human-readable denial reason")

	// frame.Result must be the parsed object, not the raw JSON string.
	obj, ok := frame.Result.(map[string]any)
	require.True(t, ok, "result must be substituted with the parsed object, got %T", frame.Result)
	assert.Equal(t, "delegation_denied", obj["error"])
	assert.Equal(t, "trust_set", obj["policy"])
	assert.Equal(t, "spawn", obj["tool"])
}

// TestToolExecEnd_PlainError_NotSubstituted proves the negative case: an ordinary
// error result is forwarded as the raw string, not parsed into an object.
//
// frame.Error's expectation was updated for the live/replay error-parity fix
// (pkg/gateway/websocket.go's EventKindToolExecEnd handler): before that fix,
// this test asserted frame.Error stayed EMPTY for a non-delegation failure —
// which was itself the exact defect the fix closes (see
// TestToolExecEnd_NonDelegationFailure_LivePathPopulatesError for the
// dedicated regression coverage). A plain (non-delegation) error result is
// still forwarded as the raw, unparsed string via frame.Result — that half
// of "not substituted" is unchanged — but frame.Error is now populated from
// that same string too, matching what pkg/gateway/replay.go's buildResult
// already persists as session.ToolCall.Error for every failed tool call.
// #823 catch-up redesign: migrated to h.hubSyncTap, see the sibling test's
// comment above.
func TestToolExecEnd_PlainError_NotSubstituted(t *testing.T) {
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-1", "sess-1", wc)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			ToolCallID: session.ToolCallID("call-err"),
			ChatID:     "chat-1",
			SessionID:  "sess-1",
			Tool:       "fs.read",
			IsError:    true,
			Result:     "permission denied",
		},
	})

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_result", frame.Type)
	assert.Equal(t, "error", frame.Status)
	assert.Equal(t, "permission denied", frame.Result,
		"a plain error result must be forwarded as the raw string, not substituted with a parsed object")
	assert.Empty(t, frame.Error,
		"Error must stay EMPTY when Result already carries the reason verbatim — setting both would "+
			"ship the identical text twice in one frame. Error is populated live only when Result "+
			"cannot carry the reason (offloaded to a ToolResultRef sentinel, or replaced by a parsed "+
			"object), and unconditionally on REPLAY, where the persisted Result is nil for ordinary "+
			"tool failures. This assertion was briefly inverted while establishing live/replay parity; "+
			"the original expectation was correct.")
}

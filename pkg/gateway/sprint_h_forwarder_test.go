//go:build !cgo

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

// makeMinimalHandler builds a WSHandler with no real dependencies set.
// eventForwarder only uses h.mu and h.taskChatIDs from the handler struct.
func makeMinimalHandler() *WSHandler {
	return &WSHandler{
		sessions:    make(map[string]*wsConn),
		sessionIDs:  make(map[string]string),
		taskChatIDs: make(map[string]string),
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
	done := runForwarder(h, wc, "chat-1", bus)

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
	<-done

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
			done := runForwarder(h, wc, "chat-1", bus)

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
			<-done

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
func TestToolExecStart_CarriesParentCallID(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	// Emit a ToolExecStart event that has a non-empty ParentSpawnCallID (inside a sub-turn).
	bus.Emit(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID:        session.ToolCallID("t1"),
			ChatID:            "chat-1",
			Tool:              "fs.list",
			Arguments:         map[string]any{"path": "/tmp"},
			ParentSpawnCallID: session.ToolCallID("c1"),
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_start", frame.Type)
	assert.Equal(t, "t1", frame.CallID)
	assert.Equal(t, "c1", frame.ParentCallID,
		"parent_call_id must be propagated from ParentSpawnCallID (FR-H-005)")
}

// TestToolExecStart_NoParentCallID_TopLevel verifies FR-H-005 negative case:
// top-level tool calls (empty ParentSpawnCallID) must NOT carry parent_call_id.
func TestToolExecStart_NoParentCallID_TopLevel(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	bus.Emit(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: session.ToolCallID("t2"),
			ChatID:     "chat-1",
			Tool:       "shell",
			// ParentSpawnCallID is empty — top-level call.
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_start", frame.Type)
	assert.Empty(t, frame.ParentCallID,
		"top-level tool calls must not carry parent_call_id")
}

// TestSpawn_OrphanSubTurn_EmitsInterruptedAfter5s verifies FR-H-004 / Scenario 7:
// When the parent turn ends before the sub-turn, the orphan watchdog synthesizes
// subagent_end{status:"interrupted"} after orphanWatchdogTimeout.
// Uses SetOrphanWatchdogTimeoutForTest to use a short timeout (~200ms) in tests.
// Traces to: sprint-h-subagent-block-spec.md TDD row 7, BDD Scenario 7.
func TestSpawn_OrphanSubTurn_EmitsInterruptedAfter5s(t *testing.T) {
	// Override watchdog timeout to 200ms so test doesn't sleep 5 seconds.
	restore := SetOrphanWatchdogTimeoutForTest(200 * time.Millisecond)
	defer restore()

	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	// 1. A sub-turn starts: emit subagent_start.
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "max",
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "some task",
			ChatID:            "chat-1",
		},
	})

	// 2. Parent turn ends — trigger orphan watchdog.
	// W1-2: must include ChatID matching the forwarder's chatID and IsRoot=true;
	// sub-turn ends from unrelated chats or non-root turns must not arm the watchdog.
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnEnd,
		Payload: agent.TurnEndPayload{
			Status: agent.TurnEndStatusCompleted,
			ChatID: "chat-1",
			IsRoot: true,
		},
	})

	// 3. Do NOT emit SubTurnEnd. The watchdog should fire after ~200ms.
	// W2-11: Replace time.Sleep(300ms) with require.Eventually to avoid CI flakes.
	// Poll until the interrupted frame is emitted into the send channel.
	// Traces to: temporal-puzzling-melody.md W2-11
	require.Eventually(t, func() bool {
		// Check if a frame is queued in the channel.
		// We want at least 2 frames: subagent_start + synthesized subagent_end{interrupted}.
		return len(ch) >= 2
	}, 2*time.Second, 10*time.Millisecond,
		"watchdog must emit subagent_end{interrupted} within 2s after parent turn ends")

	bus.Close()
	<-done

	// Drain frames: first is subagent_start, second is synthesized subagent_end.
	var frames []replayFrameDecoder
	for len(ch) > 0 {
		frames = append(frames, drainFrame(t, ch))
	}

	require.GreaterOrEqual(t, len(frames), 2,
		"must have at least 2 frames: subagent_start and synthesized subagent_end{interrupted}")

	// Find the subagent_end frame.
	var foundEnd bool
	for _, f := range frames {
		if f.Type == "subagent_end" {
			assert.Equal(t, "interrupted", f.Status,
				"orphaned span must resolve to interrupted status")
			assert.Equal(t, "span_c1", f.SpanID)
			assert.Equal(t, "c1", f.ParentCallID)
			foundEnd = true
		}
	}
	assert.True(t, foundEnd, "orphan watchdog must emit subagent_end{status:interrupted}")
}

// TestSpawn_SubTurnEnd_AfterParentDone_CancelsWatchdog verifies that when
// EventKindSubTurnEnd arrives after TurnEnd (but before the watchdog fires),
// the watchdog is canceled and NO interrupted frame is emitted.
func TestSpawn_SubTurnEnd_AfterParentDone_CancelsWatchdog(t *testing.T) {
	// Use a longer watchdog timeout so our SubTurnEnd can arrive first.
	restore := SetOrphanWatchdogTimeoutForTest(500 * time.Millisecond)
	defer restore()

	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	// 1. Sub-turn spawns.
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "max",
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "task",
			ChatID:            "chat-1",
		},
	})

	// 2. Parent turn ends — starts watchdog (500ms timer).
	// W1-2: must include ChatID and IsRoot=true for the watchdog to arm.
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnEnd,
		Payload: agent.TurnEndPayload{
			ChatID: "chat-1",
			IsRoot: true,
		},
	})

	// 3. Sub-turn ends normally (before the 500ms watchdog fires).
	time.Sleep(50 * time.Millisecond)
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnEnd,
		Payload: agent.SubTurnEndPayload{
			AgentID:           "max",
			Status:            agent.SubTurnStatusSuccess,
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			DurationMS:        100,
			ChatID:            "chat-1",
		},
	})

	// Wait past where the watchdog WOULD have fired (600ms > 500ms timer).
	time.Sleep(600 * time.Millisecond)
	bus.Close()
	<-done

	// Drain all frames.
	var frames []replayFrameDecoder
	for len(ch) > 0 {
		frames = append(frames, drainFrame(t, ch))
	}

	// Must have subagent_start and subagent_end(success) — no interrupted.
	hasInterrupted := false
	for _, f := range frames {
		if f.Type == "subagent_end" && f.Status == "interrupted" {
			hasInterrupted = true
		}
	}
	assert.False(t, hasInterrupted,
		"watchdog must not fire when sub-turn ended normally before the timeout")
}

// TestToolExecEnd_DelegationDenied_SubstitutesStructuredResult proves the UAT
// fix wiring in eventForwarder: a tool result with status=="error" whose Result
// is a structured delegation_denied payload is substituted — frame.Result
// becomes the parsed object (not the raw string) and frame.Error carries the
// human-readable reason. Mirrors the forwarder tests above.
func TestToolExecEnd_DelegationDenied_SubstitutesStructuredResult(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	denial := `{"error":"delegation_denied","reason":"target untrusted","policy":"trust_set","tool":"spawn","target_agent_id":"evil"}`
	bus.Emit(agent.Event{
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

	bus.Close()
	<-done

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
func TestToolExecEnd_PlainError_NotSubstituted(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	bus.Emit(agent.Event{
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

	bus.Close()
	<-done

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_result", frame.Type)
	assert.Equal(t, "error", frame.Status)
	assert.Empty(t, frame.Error, "a plain error must not set the delegation error field")
	assert.Equal(t, "permission denied", frame.Result,
		"a plain error result must be forwarded as the raw string")
}

// Regression coverage for #605: the orphan watchdog arming race.
//
// The root TurnEnd and a delegate's SubTurnSpawn are emitted from DIFFERENT
// goroutines (the parent turn's goroutine vs the detached async-delegate
// goroutine, which does real file I/O before emitting), so a spawn event can
// legally reach the eventForwarder AFTER the root turn_end. The
// EventKindTurnEnd case only arms spans already registered in openSpans; before
// the #605 latch, a span registered after it was never armed — no reschedule
// ceiling, no forced subagent_end{interrupted}, a subagent stuck "running" in
// the UI forever. This surfaced as
// TestOrphanWatchdog_PermanentlyStuckDelegate_ForceFiresInterruptedPastCeiling
// timing out on both Linux legs of cross-platform CI run 31184159924.
package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// emitRootTurnEnd emits a root TurnEnd for chatID on the bus.
func emitRootTurnEnd(bus *agent.EventBus, chatID string) {
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnEnd,
		Payload: agent.TurnEndPayload{
			Status: agent.TurnEndStatusCompleted,
			ChatID: chatID,
			IsRoot: true,
		},
	})
}

// emitSpawn emits a SubTurnSpawn for chatID with the given span/call IDs.
func emitSpawn(bus *agent.EventBus, chatID, spanID, callID string) {
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "ray",
			SpanID:            spanID,
			ParentSpawnCallID: session.ToolCallID(callID),
			TaskLabel:         "late-registered task",
			ChatID:            chatID,
		},
	})
}

// TestOrphanWatchdog_SpawnAfterRootTurnEnd_StillArmsAndInterrupts is the
// direct #605 regression: the spawn event arrives AFTER the root turn_end
// (the reversed ordering of TestSpawn_OrphanSubTurn_EmitsInterruptedAfter5s).
// The late-registered span must still be armed and force-resolved to
// subagent_end{interrupted}.
func TestOrphanWatchdog_SpawnAfterRootTurnEnd_StillArmsAndInterrupts(t *testing.T) {
	restore := SetOrphanWatchdogTimeoutForTest(100 * time.Millisecond)
	defer restore()

	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	// 1. Root turn ends FIRST (the delegate's spawn event is still in flight
	//    on its detached goroutine).
	emitRootTurnEnd(bus, "chat-1")
	// 2. The spawn event lands late.
	emitSpawn(bus, "chat-1", "span_late", "c-late")

	// The span must be armed at registration via the rootTurnEnded latch and
	// resolve to interrupted: subagent_start + synthesized subagent_end.
	require.Eventually(t, func() bool {
		return len(ch) >= 2
	}, 2*time.Second, 10*time.Millisecond,
		"BUG REGRESSION (#605): a span registered AFTER the root turn_end must still be "+
			"armed by the orphan watchdog and force-resolved to subagent_end{interrupted}")

	bus.Close()
	<-done

	var frames []replayFrameDecoder
	for len(ch) > 0 {
		frames = append(frames, drainFrame(t, ch))
	}
	var foundEnd bool
	for _, f := range frames {
		if f.Type == "subagent_end" {
			assert.Equal(t, "interrupted", f.Status,
				"late-registered orphan span must resolve to interrupted")
			assert.Equal(t, "span_late", f.SpanID)
			foundEnd = true
		}
	}
	assert.True(t, foundEnd,
		"orphan watchdog must emit subagent_end{status:interrupted} for a late-registered span")
}

// TestOrphanWatchdog_NewRootTurnStart_ResetsLatch guards the latch reset: a
// span spawned after a NEW root turn began belongs to a live root turn and
// must NOT be armed at registration (its own TurnEnd will arm it later).
func TestOrphanWatchdog_NewRootTurnStart_ResetsLatch(t *testing.T) {
	restore := SetOrphanWatchdogTimeoutForTest(100 * time.Millisecond)
	defer restore()

	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	// Previous root turn ended...
	emitRootTurnEnd(bus, "chat-1")
	// ...then a NEW root turn starts on the same chat...
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnStart,
		Payload: agent.TurnStartPayload{
			Channel: "webchat",
			ChatID:  "chat-1",
			IsRoot:  true,
		},
	})
	// ...and spawns a delegate. The latch must have been reset: registered
	// UNARMED, so no interrupted frame may appear.
	emitSpawn(bus, "chat-1", "span_live", "c-live")

	// Give the (not-running) watchdog 5x its timeout to misfire.
	time.Sleep(500 * time.Millisecond)

	bus.Close()
	<-done

	require.Len(t, ch, 1,
		"a span spawned under a LIVE root turn must produce only subagent_start — "+
			"an interrupted frame here means the TurnStart latch reset is broken")
	frame := drainFrame(t, ch)
	assert.Equal(t, "subagent_start", frame.Type)
}

// TestOrphanWatchdog_ChildTurnStart_DoesNotResetLatch guards the subtle
// sibling hole: a CHILD turn's own turn-start (IsRoot=false) arrives between
// its spawn event and any new root turn. If it reset the latch, a SECOND
// delegate's later-arriving spawn event would be registered unarmed and become
// invisible — reintroducing #605 for multi-delegate turns.
func TestOrphanWatchdog_ChildTurnStart_DoesNotResetLatch(t *testing.T) {
	restore := SetOrphanWatchdogTimeoutForTest(100 * time.Millisecond)
	defer restore()

	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	// Root turn ended; delegate A's spawn lands late and starts running.
	emitRootTurnEnd(bus, "chat-1")
	emitSpawn(bus, "chat-1", "span_a", "c-a")
	// Delegate A's own (non-root) turn-start.
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnStart,
		Payload: agent.TurnStartPayload{
			Channel: "webchat",
			ChatID:  "chat-1",
			IsRoot:  false,
		},
	})
	// Delegate B's spawn lands even later. The latch must still be set.
	emitSpawn(bus, "chat-1", "span_b", "c-b")

	// Both spans must force-resolve to interrupted: 2 starts + 2 ends.
	require.Eventually(t, func() bool {
		return len(ch) >= 4
	}, 2*time.Second, 10*time.Millisecond,
		"BUG REGRESSION (#605 sibling hole): a child turn-start must NOT reset the "+
			"root-turn-ended latch — delegate B's late spawn was registered unarmed")

	bus.Close()
	<-done

	interrupted := map[string]bool{}
	for len(ch) > 0 {
		f := drainFrame(t, ch)
		if f.Type == "subagent_end" && f.Status == "interrupted" {
			interrupted[f.SpanID] = true
		}
	}
	assert.True(t, interrupted["span_a"], "delegate A must be force-interrupted")
	assert.True(t, interrupted["span_b"], "delegate B must be force-interrupted")
}

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// S1 final-gate M8: the shared steering enqueue reports `interrupt.received`
// against the turn that OWNS the steered scope - never against whichever turn
// the process-wide registry happens to hold first. Session A is active; a steer
// for session B (which has no live turn) must be reported against B.
func TestSteeringEnqueue_InterruptReceivedIsAttributedToTheSteeredSession(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	tsA := &turnState{turnID: "turn-of-A", sessionKey: "sess-A", agentID: "agent-A", finishedChan: make(chan struct{})}
	al.activeTurnStates.Store("sess-A", tsA)
	defer al.activeTurnStates.Delete("sess-A")

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	_, _, err := al.enqueueSteeringMessage("sess-B", "agent-B", providers.Message{Role: "user", Content: "for B"}, "")
	require.NoError(t, err)

	evt, ok := findEvent(collectEventStream(sub.C), EventKindInterruptReceived)
	require.True(t, ok, "the enqueue must emit interrupt.received")
	require.Equal(t, "sess-B", evt.Meta.SessionKey, "the event must name the steered session, not session A")
	require.Equal(t, "agent-B", evt.Meta.AgentID)
	require.NotEqual(t, "turn-of-A", evt.Meta.TurnID, "the event must not carry session A's turn id")
}

// Control: when the steered scope DOES have a live turn, the event is attributed
// to that turn.
func TestSteeringEnqueue_InterruptReceivedUsesTheScopesOwnLiveTurn(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	tsB := &turnState{turnID: "turn-of-B", sessionKey: "sess-B", agentID: "agent-B", finishedChan: make(chan struct{})}
	al.activeTurnStates.Store("sess-B", tsB)
	defer al.activeTurnStates.Delete("sess-B")
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	_, _, err := al.enqueueSteeringMessage("sess-B", "agent-B", providers.Message{Role: "user", Content: "for B"}, "")
	require.NoError(t, err)
	evt, ok := findEvent(collectEventStream(sub.C), EventKindInterruptReceived)
	require.True(t, ok)
	require.Equal(t, "turn-of-B", evt.Meta.TurnID)
	require.Equal(t, "sess-B", evt.Meta.SessionKey)
}

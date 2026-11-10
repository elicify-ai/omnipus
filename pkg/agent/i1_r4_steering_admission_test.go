package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func i1R4HumanMessage(h *d2bRoot) bus.InboundMessage {
	return bus.InboundMessage{Channel: "webchat", ChatID: h.id, SessionID: h.id,
		Content:       "Keep this instruction when shutdown refuses its continuation.",
		UserInitiated: true, GatewayUserID: "d2b-owner",
		Sender:   bus.SenderInfo{CanonicalID: "d2b-owner"},
		Metadata: map[string]string{"agent_id": testDefaultAgentID, "workspace_id": testHarnessWorkspaceMembershipID}}
}

// Silent A: rejection before detached ownership must preserve the whole typed
// instruction through the real worker's existing inbox fallback.
func TestI1R4ShutdownSteeringKeepsInstruction(t *testing.T) {
	h := i1R3RestartStopped(t)
	msg := i1R4HumanMessage(h)
	route, _, err := h.al.resolveMessageRoute(msg)
	require.NoError(t, err)
	before := h.journal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.True(t, h.al.WaitForActiveRequestsContext(ctx), "actual shutdown intake must be closed before steering")
	handled, refusal := h.al.reviveInactiveInbound(route, msg)
	assert.False(t, handled, "shutdown refusal cannot claim continuation ownership")
	assert.ErrorIs(t, refusal, context.Canceled, "caller must see shutdown instead of accepting a stopped steering queue")
	w := newSessionWorker(route.SessionKey, h.al, func() {})
	t.Cleanup(w.cancel)
	w.inTurn.Store(true)
	require.True(t, w.enqueue(msg), "existing worker must retain or visibly reject a typed instruction")
	select {
	case retained := <-w.inbox:
		assert.Equal(t, msg, retained, "shutdown refusal must retain the exact original instruction, not claim detached ownership")
	default:
		t.Fatal("shutdown-refused continuation was neither run nor retained in the worker inbox")
	}
	assert.Empty(t, h.provider.calls())
	assert.Equal(t, before, h.journal(t), "shutdown cannot revive or stamp a new execution")
	assert.Equal(t, 0, h.al.pendingSteeringCountForScope(route.SessionKey), "cannot strand text in a stopped turn's steering queue")
}

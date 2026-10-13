package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// U8 security F8: an owner wake (a guest answered a request from this chat)
// carries no human authority. However late a human Stop lands - before the
// producer published it, or between its precheck and its consumption - the
// wake must not revive the stopped chat nor run the owner. The answer stays in
// the transcript. These drive the real intake (processMessage), not the bus.

func TestOwnerWake_AfterLandedStop_DoesNotReviveOrRun(t *testing.T) {
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleStopped)
	before := h.journal(t)
	meta, err := h.al.GetSessionStore().GetMeta(h.id)
	require.NoError(t, err)
	wake := ownerWakeInbound(addressing.Capture{RequestID: "q1", Source: addressing.Source{SessionID: h.id}},
		meta.AgentID, meta.WorkspaceID, session.TranscriptEntry{ID: "ans-1", AgentID: "guest"})

	reply, _, perr := h.al.processMessage(context.Background(), wake)
	require.NoError(t, perr, "a stop is not a failure: the retained wake reports no error")
	assert.Empty(t, reply, "and publishes no reply into the answer pseudo-chat")

	assert.Empty(t, h.provider.calls(), "an owner wake must never run the model on a stopped chat")
	got := h.load(t)
	assert.Equal(t, session.LifecycleStopped, got.State, "the human's Stop must still hold")
	assert.Equal(t, 1, got.Generation, "no new generation may be minted by a wake")
	assert.Equal(t, string(before), string(h.journal(t)), "the wake must not append to the lifecycle journal")
}

func TestOwnerWake_AfterLandedStop_ControlsStillWork(t *testing.T) {
	// Positive control 1: a wake for a COMPLETED (idle) chat still runs the owner.
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleCompleted)
	meta, err := h.al.GetSessionStore().GetMeta(h.id)
	require.NoError(t, err)
	wake := ownerWakeInbound(addressing.Capture{RequestID: "q1", Source: addressing.Source{SessionID: h.id}},
		meta.AgentID, meta.WorkspaceID, session.TranscriptEntry{ID: "ans-1", AgentID: "guest"})
	_, _, err = h.al.processMessage(context.Background(), wake)
	require.NoError(t, err)
	assert.Len(t, h.provider.calls(), 1, "an idle owner must still be woken by an answer")

	// Positive control 2: a person's message still revives a stopped chat.
	h2 := newD2bRoot(t)
	h2.seedState(t, session.LifecycleStopped)
	_, err = h2.humanTurn(t, "carry on")
	require.NoError(t, err)
	assert.Len(t, h2.provider.calls(), 1, "a human message must still continue a stopped chat")
	assert.NotEqual(t, session.LifecycleStopped, h2.load(t).State)
}

func TestOwnerWake_IsNotAHumanTurn(t *testing.T) {
	wake := ownerWakeInbound(addressing.Capture{RequestID: "q1", Source: addressing.Source{SessionID: "s"}}, "ann", "ws", session.TranscriptEntry{ID: "a"})
	assert.False(t, reviveInboundIsHumanTurn(wake), "the owner wake carries no human revival authority")
}

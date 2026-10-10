package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Security review OPSITE-connector_egress (HIGH): the main-connector refusal
// note is a server-generated prompt. It carries no human authority: however
// late a person's Stop lands, consuming the note must neither clear the Stop
// nor start a turn. It is delivered normally to a live, unstopped main.

// producedRefusalNote runs the real producer and returns the inbound it queued.
func producedRefusalNote(t *testing.T, h *d2bRoot) bus.InboundMessage {
	t.Helper()
	ag, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	h.al.refuseMainConnectorDefaultSend(context.Background(), "telegram.a", h.id, ag)
	select {
	case m := <-h.al.bus.InboundChan():
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("the refusal note was not published")
		return bus.InboundMessage{}
	}
}

func TestRefusalNote_AfterLandedStop_DoesNotReviveOrRun(t *testing.T) {
	h := newD2bRoot(t)
	note := producedRefusalNote(t, h) // pending ...
	h.seedState(t, session.LifecycleStopped) // ... a person's Stop lands ...
	before := h.journal(t)

	reply, _, perr := h.al.processMessage(context.Background(), note) // ... then it is consumed

	require.NoError(t, perr, "a stop is not a failure: the retained note reports no error")
	assert.Empty(t, reply)
	assert.Empty(t, h.provider.calls(), "a refusal note must never run the model on a stopped chat")
	got := h.load(t)
	assert.Equal(t, session.LifecycleStopped, got.State, "the human's Stop must still hold")
	assert.Equal(t, 1, got.Generation, "no new generation may be minted by a note")
	assert.Equal(t, string(before), string(h.journal(t)), "the note must not append to the lifecycle journal")
}

func TestRefusalNote_IsNotAHumanTurnAndNeverAnOperatorPrompt(t *testing.T) {
	h := newD2bRoot(t)
	note := producedRefusalNote(t, h)
	assert.False(t, reviveInboundIsHumanTurn(note), "a server-generated note carries no human revival authority")
	assert.False(t, note.OperatorPrompt, "the note must never release the browser wheel")
	assert.False(t, note.UserInitiated)
}

func TestRefusalNote_ControlLiveUnstoppedMainStillGetsIt(t *testing.T) {
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleCompleted) // an idle, unstopped chat
	note := producedRefusalNote(t, h)
	_, _, err := h.al.processMessage(context.Background(), note)
	require.NoError(t, err)
	assert.Len(t, h.provider.calls(), 1, "the agent must still be prompted to answer each sender")
	assert.NotEqual(t, session.LifecycleStopped, h.load(t).State)
}

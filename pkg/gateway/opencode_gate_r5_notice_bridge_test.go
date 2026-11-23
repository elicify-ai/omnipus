package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-R5-2 bridge CONTROL: this is deliberately an isolated synthetic event,
// not proof the helper producer uses it. Its real-producer RED is in agent's
// TestOpenCodeR5_FinishingWindowRefusalUsesNonTerminalOwnSessionCarrier.
func TestOpenCodeR5_NonTerminalRefusalCarrierReachesBothOwnSessionViews(t *testing.T) {
	h := makeMinimalHandler()
	first, firstFrames := makeForwarderTestConn(16)
	second, secondFrames := makeForwarderTestConn(16)
	parent, parentFrames := makeForwarderTestConn(16)
	const sid = "r5-helper-two-views"
	bindTestConnToSession(h, "r5-view-a", sid, first)
	bindTestConnToSession(h, "r5-view-b", sid, second)
	bindTestConnToSession(h, "r5-parent-view", "r5-parent", parent)
	bus := agent.NewEventBus()
	t.Cleanup(bus.Close)
	bus.SetSyncTap(h.hubSyncTap)
	// Prove this real hub/tap can expose a terminal error before asserting
	// its absence for the explicitly non-terminal notice below.
	bus.Emit(agent.Event{Kind: agent.EventKindError,
		Meta:    agent.EventMeta{SessionKey: sid, TurnID: "r5-instrument-turn"},
		Payload: agent.ErrorPayload{SessionID: sid, Code: "unknown", Message: "bridge control"},
	})
	for _, frames := range []chan []byte{firstFrames, secondFrames} {
		require.Len(t, frames, 1, "instrument: actual error forwarding reaches both bound views")
		var frame generated.ErrorFrame
		require.NoError(t, json.Unmarshal(<-frames, &frame))
		require.Equal(t, "error", frame.Type)
	}
	id, stamp := "r5-own-saved-refusal", "2026-11-23T12:00:00Z"
	const text = "The rejected input is separate from the healthy answer."
	bus.Emit(agent.Event{Kind: agent.EventKindError,
		// The supported notice's own session must win over legacy tab data.
		Meta: agent.EventMeta{SessionKey: "r5-parent"},
		Payload: agent.ErrorPayload{SessionID: sid, ChatID: "r5-parent-view", Stage: "human_input",
			Code: "delegation_not_resumable", Message: text,
			ChatNotice: &generated.ReplayMessageFrame{Type: "replay_message", SessionId: sid,
				Role: "system", Id: &id, Timestamp: &stamp, Content: text}},
	})
	require.Len(t, firstFrames, 1)
	require.Len(t, secondFrames, 1)
	firstRaw, secondRaw := <-firstFrames, <-secondFrames
	assert.Equal(t, firstRaw, secondRaw, "both views receive the same actual sequenced notice")
	var frame generated.ReplayMessageFrame
	require.NoError(t, json.Unmarshal(secondRaw, &frame))
	assert.Equal(t, "replay_message", frame.Type, "never translate a non-terminal refusal into a turn-ending error")
	assert.Equal(t, sid, frame.SessionId)
	assert.Equal(t, "system", frame.Role)
	require.NotNil(t, frame.Id)
	assert.Equal(t, id, *frame.Id)
	assert.Equal(t, text, frame.Content)
	assert.Nil(t, frame.ClientMessageId, "this carrier must not invent original client correlation")
	assert.Empty(t, parentFrames)
}

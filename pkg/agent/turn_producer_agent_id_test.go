package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// producerAgentIDMockStreamer is a bus.Streamer that also records
// SetProducerAgentID calls, mirroring *gateway.wsStreamer's method of the
// same name. It lets stampStreamerProducerAgentID's type-assertion be
// exercised without importing the gateway package.
type producerAgentIDMockStreamer struct {
	mockStreamer
	setProducerAgentIDCalls []string
}

func (m *producerAgentIDMockStreamer) SetProducerAgentID(agentID string) {
	m.setProducerAgentIDCalls = append(m.setProducerAgentIDCalls, agentID)
}

// TestStampStreamerProducerAgentID_UsesTurnsOwnAgent verifies FIX 5a's wiring:
// stampStreamerProducerAgentID must stamp the streamer with the CURRENT turn's
// own resolved agent (ts.agent.ID) — the true producer — not any other value.
// This is the scenario that matters most: a background/delegated sub-turn
// running as agent "ava-worker" (per ADR-032, never inheriting the parent's
// identity) must stamp its streamer with "ava-worker", even though the
// session the frames are delivered on might be "actively" associated with a
// completely different agent (e.g. "jim", the delegating parent).
//
// BDD:
//
//	Given a turnState whose resolved agent is "ava-worker" (a background
//	  delegate, distinct from any parent/session-active agent),
//	When stampStreamerProducerAgentID is called with a streamer that
//	  implements SetProducerAgentID,
//	Then the streamer is stamped with "ava-worker" — never "jim" or any
//	  other value the caller did not explicitly resolve for this turn.
//
// Traces to: pkg/agent/turn.go stampStreamerProducerAgentID; called from
// pkg/agent/loop.go's streaming branch immediately after GetStreamer.
func TestStampStreamerProducerAgentID_UsesTurnsOwnAgent(t *testing.T) {
	ts := &turnState{agent: &AgentInstance{ID: "ava-worker"}}
	streamer := &producerAgentIDMockStreamer{}

	ts.stampStreamerProducerAgentID(streamer)

	require.Len(t, streamer.setProducerAgentIDCalls, 1,
		"SetProducerAgentID must be called exactly once")
	assert.Equal(t, "ava-worker", streamer.setProducerAgentIDCalls[0],
		"the streamer must be stamped with the TURN's own resolved agent ID (the true "+
			"producer), never a parent/session-active-agent guess")
}

// TestStampStreamerProducerAgentID_NoOpWhenStreamerDoesNotImplementInterface
// verifies that non-webchat streamers (telegram, wecom, sse — none of which
// implement SetProducerAgentID) are left untouched: the type-assertion must
// fail silently, matching the established pattern used by
// markLastStreamerProducedModel / markLastStreamerTranscriptPersisted.
//
// BDD:
//
//	Given a turnState and a bus.Streamer that does NOT implement
//	  SetProducerAgentID,
//	When stampStreamerProducerAgentID is called,
//	Then it does not panic and has no observable effect.
func TestStampStreamerProducerAgentID_NoOpWhenStreamerDoesNotImplementInterface(t *testing.T) {
	ts := &turnState{agent: &AgentInstance{ID: "ava-worker"}}
	streamer := &mockStreamer{} // does NOT implement SetProducerAgentID

	assert.NotPanics(t, func() {
		ts.stampStreamerProducerAgentID(streamer)
	}, "must be a safe no-op for streamers without SetProducerAgentID")
}

// TestStampStreamerProducerAgentID_NoOpWhenAgentNil guards against a nil
// ts.agent (should not happen for a live turn, but stampStreamerProducerAgentID
// must not panic if it ever does).
func TestStampStreamerProducerAgentID_NoOpWhenAgentNil(t *testing.T) {
	ts := &turnState{}
	streamer := &producerAgentIDMockStreamer{}

	assert.NotPanics(t, func() {
		ts.stampStreamerProducerAgentID(streamer)
	})
	assert.Empty(t, streamer.setProducerAgentIDCalls,
		"must not stamp the streamer when the turn has no resolved agent")
}

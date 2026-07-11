package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// producerAgentIDMockStreamer is a bus.Streamer that also records
// SetProducerAgentID and SetTurnID calls, mirroring *gateway.wsStreamer's
// methods of the same names. It lets stampStreamerProducerAgentID's and
// stampStreamerTurnID's type-assertions be exercised without importing the
// gateway package.
type producerAgentIDMockStreamer struct {
	mockStreamer
	setProducerAgentIDCalls []string
	setTurnIDCalls          []string
}

func (m *producerAgentIDMockStreamer) SetProducerAgentID(agentID string) {
	m.setProducerAgentIDCalls = append(m.setProducerAgentIDCalls, agentID)
}

// SetTurnID mirrors *gateway.wsStreamer.SetTurnID's own no-op-on-empty
// guard, so tests exercising stampStreamerTurnID's caller-side behavior
// (which — unlike stampStreamerProducerAgentID — has no nil-pointer reason
// to skip the call itself, and so unconditionally forwards to the streamer,
// relying on the streamer's own empty check) observe production-equivalent
// results.
func (m *producerAgentIDMockStreamer) SetTurnID(turnID string) {
	if turnID == "" {
		return
	}
	m.setTurnIDCalls = append(m.setTurnIDCalls, turnID)
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

// TestStampStreamerTurnID_UsesTurnsOwnID verifies FIX 5c/1's wiring:
// stampStreamerTurnID must stamp the streamer with the CURRENT turn's own
// ID (ts.turnID). This is the fix for the confirmed live-verification bug
// where the assistant transcript entry carried no TurnID at all, breaking
// both the turn_canceled replay correlation and MarkLastEntryTruncated's
// own turn-scoped matching.
//
// Traces to: pkg/agent/turn.go stampStreamerTurnID; called from
// pkg/agent/loop.go's streaming branch immediately after GetStreamer,
// alongside stampStreamerProducerAgentID.
func TestStampStreamerTurnID_UsesTurnsOwnID(t *testing.T) {
	ts := &turnState{turnID: "turn-abc123"}
	streamer := &producerAgentIDMockStreamer{}

	ts.stampStreamerTurnID(streamer)

	require.Len(t, streamer.setTurnIDCalls, 1, "SetTurnID must be called exactly once")
	assert.Equal(t, "turn-abc123", streamer.setTurnIDCalls[0],
		"the streamer must be stamped with the TURN's own ID")
}

// TestStampStreamerTurnID_NoOpWhenStreamerDoesNotImplementInterface mirrors
// TestStampStreamerProducerAgentID_NoOpWhenStreamerDoesNotImplementInterface
// for SetTurnID: non-webchat streamers are left untouched.
func TestStampStreamerTurnID_NoOpWhenStreamerDoesNotImplementInterface(t *testing.T) {
	ts := &turnState{turnID: "turn-abc123"}
	streamer := &mockStreamer{} // does NOT implement SetTurnID

	assert.NotPanics(t, func() {
		ts.stampStreamerTurnID(streamer)
	}, "must be a safe no-op for streamers without SetTurnID")
}

// TestStampStreamerTurnID_NoOpWhenTurnIDEmpty guards against an empty
// ts.turnID (should not happen for a live turn). Unlike
// stampStreamerProducerAgentID (which skips the call at the caller to avoid
// a nil-pointer dereference on ts.agent.ID), stampStreamerTurnID has no such
// structural reason to skip and always forwards to SetTurnID — the no-op
// guarantee comes from the callee's own empty check (mirrored by the mock
// here), exactly like production *gateway.wsStreamer.SetTurnID.
func TestStampStreamerTurnID_NoOpWhenTurnIDEmpty(t *testing.T) {
	ts := &turnState{}
	streamer := &producerAgentIDMockStreamer{}

	ts.stampStreamerTurnID(streamer)

	assert.Empty(t, streamer.setTurnIDCalls,
		"must not stamp the streamer when the turn has no ID")
}

// turn_stream_test.go: tests for streamer lifecycle for a turn — start, finalize, flush

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from turn.go tests 2026-09-15 ---

// TestFinalizeStreamer_PushesTokenSplitToStreamer closes the seam that made the
// token-accounting fix inert for the surface that matters most.
//
// The split was threaded through turnState -> TranscriptEntry -> session stats
// and covered by tests at each end. But a STREAMED turn — every ordinary
// webchat turn — does not go through appendAssistantTranscript at all; the
// streamer builds its own TranscriptEntry in Finalize. finalizeStreamer pushed
// only the collapsed total, so the streamer had no split to stamp and every
// webchat session kept reporting tokens_in: 0.
//
// Every other test in this change stayed green throughout that. This one fails
// if the streamerIOStatsSetter call is removed from finalizeStreamer.
func TestFinalizeStreamer_PushesTokenSplitToStreamer(t *testing.T) {
	ts := &turnState{startedAt: time.Now()}
	streamer := &fakeIOStreamer{}
	ts.lastStreamer = streamer

	ts.AddTurnStats(1000, 0.5)
	ts.AddTurnIOStats(800, 200)
	ts.AddTurnCacheStats(150, 50)

	ts.finalizeStreamer(context.Background())

	if !streamer.finalized {
		t.Fatal("streamer was never finalized")
	}
	if streamer.gotTokens != 1000 {
		t.Errorf("total tokens = %d, want 1000", streamer.gotTokens)
	}
	if streamer.gotPrompt != 800 {
		t.Errorf("prompt tokens = %d, want 800 — the streamer got no input/output split, "+
			"so its TranscriptEntry will carry none and tokens_in stays 0", streamer.gotPrompt)
	}
	if streamer.gotCompletion != 200 {
		t.Errorf("completion tokens = %d, want 200", streamer.gotCompletion)
	}
	if streamer.gotCacheRead != 150 || streamer.gotCacheWrite != 50 {
		t.Errorf("cache split = %d/%d, want 150/50", streamer.gotCacheRead, streamer.gotCacheWrite)
	}
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

// TestStampStreamerParentSpawnCallID_UsesTurnsOwnParentSpawnCallID verifies
// the A-I4 live/reload parity fix's wiring: a delegation CHILD sub-turn's own
// streamer must be stamped with childTS.parentSpawnCallID, the same
// correlation subturn.go sets at spawnSubTurn construction — this is what
// lets the transcript entry wsStreamer.Finalize writes carry the nesting
// signal pkg/gateway/replay.go needs to withhold the child's own raw
// narration/report from top-level replay.
//
// BDD:
//
//	Given a turnState for a delegation CHILD sub-turn (parentSpawnCallID ==
//	  "call_abc123", the spawning "delegate" ToolCall.ID in the parent turn),
//	When stampStreamerParentSpawnCallID is called with a streamer that
//	  implements SetParentSpawnCallID,
//	Then the streamer is stamped with "call_abc123".
//
// Traces to: pkg/agent/turn.go stampStreamerParentSpawnCallID; called from
// pkg/agent/loop.go's streaming branch immediately after GetStreamer,
// alongside stampStreamerProducerAgentID/stampStreamerTurnID.
func TestStampStreamerParentSpawnCallID_UsesTurnsOwnParentSpawnCallID(t *testing.T) {
	ts := &turnState{parentSpawnCallID: "call_abc123"}
	streamer := &producerAgentIDMockStreamer{}

	ts.stampStreamerParentSpawnCallID(streamer)

	require.Len(t, streamer.setParentSpawnCallIDCalls, 1,
		"SetParentSpawnCallID must be called exactly once")
	assert.Equal(t, "call_abc123", streamer.setParentSpawnCallIDCalls[0],
		"the streamer must be stamped with the CHILD turn's own spawning ToolCall.ID")
}

// TestStampStreamerParentSpawnCallID_RootTurnStampsEmpty verifies the
// counterpart case: a ROOT (non-delegated) turn has parentSpawnCallID == "",
// and — unlike stampStreamerTurnID, which relies on the streamer's own
// no-op-on-empty guard — this call must still reach SetParentSpawnCallID
// with the empty value, because empty is itself meaningful (it is what
// tells Finalize's transcript entry "this is NOT a delegation child").
// A silently-skipped call here would leave a REUSED wsStreamer instance
// (unusual, but not structurally prevented) carrying a stale non-empty
// value from a prior turn.
func TestStampStreamerParentSpawnCallID_RootTurnStampsEmpty(t *testing.T) {
	ts := &turnState{}
	streamer := &producerAgentIDMockStreamer{}

	ts.stampStreamerParentSpawnCallID(streamer)

	require.Len(t, streamer.setParentSpawnCallIDCalls, 1,
		"SetParentSpawnCallID must be called even for a root turn (empty value)")
	assert.Equal(t, "", streamer.setParentSpawnCallIDCalls[0])
}

// TestStampStreamerParentSpawnCallID_NoOpWhenStreamerDoesNotImplementInterface
// mirrors the sibling stamp functions' fallback tests: streamers without the
// optional SetParentSpawnCallID method are left untouched.
func TestStampStreamerParentSpawnCallID_NoOpWhenStreamerDoesNotImplementInterface(t *testing.T) {
	ts := &turnState{parentSpawnCallID: "call_abc123"}
	streamer := &mockStreamer{} // does NOT implement SetParentSpawnCallID

	assert.NotPanics(t, func() {
		ts.stampStreamerParentSpawnCallID(streamer)
	}, "must be a safe no-op for streamers without SetParentSpawnCallID")
}

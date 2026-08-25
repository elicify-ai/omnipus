// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Call-site regression coverage for the three ts.stampStreamer* calls in
// loop.go's runTurn streaming branch (immediately after
// al.bus.GetStreamer(...) returns hasStreamer==true).
//
// Confirmed gap: all nine TestStampStreamer* tests in
// turn_stream_identity_stamp_test.go call ts.stampStreamerProducerAgentID /
// stampStreamerTurnID / stampStreamerParentSpawnCallID directly against a
// bare *turnState — none of them drive a real turn through loop.go. With
// the three call sites replaced by no-ops, all nine still pass (9/9, 0
// failures) — nothing registers a streamer and drives a real streaming turn
// through the normal path, so the call sites can be deleted freely and no
// existing test notices.
//
// turn_stream_identity_stamp_test.go's own file header explains why these
// stamps exist: pre-fix, the assistant transcript entry Finalize writes
// carried no TurnID at all, breaking Stop's cancel correlation and replay's
// turn-scoped matching. This file proves the wiring that fix depends on is
// actually reached from a real streaming turn, not just implemented as
// dead, uncalled methods.
//
// Traces to: pkg/agent/loop.go::runTurn (streaming branch, the three
// ts.stampStreamer* calls immediately after GetStreamer); pkg/agent/turn.go
// stampStreamerProducerAgentID / stampStreamerTurnID /
// stampStreamerParentSpawnCallID.
//
// Build: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestRunTurn_StreamingIdentityStamps' -p 1 ./pkg/agent/

package agent

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// identityStampStreamDelegate is a bus.StreamDelegate that always returns
// the given streamer, regardless of channel/chatID/sessionID — enough to
// force loop.go's streaming branch (al.bus.GetStreamer returning
// hasStreamer==true) for a single-turn test.
type identityStampStreamDelegate struct {
	streamer bus.Streamer
}

func (d *identityStampStreamDelegate) GetStreamer(_ context.Context, _, _, _ string) (bus.Streamer, bool) {
	return d.streamer, true
}

var _ bus.StreamDelegate = (*identityStampStreamDelegate)(nil)

// identityStampRecordingStreamer wraps producerAgentIDMockStreamer (which
// already records SetProducerAgentID/SetTurnID/SetParentSpawnCallID calls)
// and additionally records every Update() call, so the test can prove the
// STREAMING path (ChatStream, with per-chunk Update calls) actually engaged
// rather than the non-streaming Chat path — producerAgentIDMockStreamer's
// embedded mockStreamer.Update is a silent no-op and doesn't track this.
type identityStampRecordingStreamer struct {
	producerAgentIDMockStreamer
	updates []string

	// updateCountAtStamp records len(s.updates) at the moment each Set* call
	// landed — the ordering half of the identity-stamp contract. The original
	// bug this whole file exists to catch was the stamps arriving AFTER
	// tokens had already streamed out unidentified; asserting the calls
	// merely happened (their prior only-check) cannot detect that, since
	// stampStreamer* could be moved to run after ChatStream returns and every
	// existing assertion here would still pass. Verified: doing exactly that
	// in loop.go's runTurn left this test green until these fields and their
	// assertions were added.
	updateCountAtStamp struct {
		producerAgentID   int
		turnID            int
		parentSpawnCallID int
	}
}

func (s *identityStampRecordingStreamer) Update(_ context.Context, content string) error {
	s.updates = append(s.updates, content)
	return nil
}

func (s *identityStampRecordingStreamer) SetProducerAgentID(agentID string) {
	s.updateCountAtStamp.producerAgentID = len(s.updates)
	s.producerAgentIDMockStreamer.SetProducerAgentID(agentID)
}

func (s *identityStampRecordingStreamer) SetTurnID(turnID string) {
	s.updateCountAtStamp.turnID = len(s.updates)
	s.producerAgentIDMockStreamer.SetTurnID(turnID)
}

func (s *identityStampRecordingStreamer) SetParentSpawnCallID(parentSpawnCallID string) {
	s.updateCountAtStamp.parentSpawnCallID = len(s.updates)
	s.producerAgentIDMockStreamer.SetParentSpawnCallID(parentSpawnCallID)
}

// TestRunTurn_StreamingIdentityStamps_ReachRealStreamer drives one real
// streaming turn through AgentLoop.processMessage with a live streamer
// registered via bus.SetStreamDelegate, and asserts the streamer actually
// observes all three identity stamps with THIS turn's own values.
//
// BDD:
//
//	Given a real AgentLoop with a StreamingProvider and a live streamer
//	  registered for a root (non-delegated) turn,
//	When a real turn is driven through AgentLoop.processMessage and the
//	  streaming branch engages (ChatStream, not the non-streaming Chat path),
//	Then the streamer observes exactly one SetProducerAgentID call carrying
//	  the turn's own resolved agent ID, exactly one SetTurnID call whose
//	  value matches the turnEventScope format "<agentID>-turn-<N>"
//	  (al.newTurnEventScope, loop.go), and exactly one SetParentSpawnCallID
//	  call carrying "" (root turn — ADR-057: empty is the valid "not a
//	  delegation child" value, not a missing/skipped stamp).
func TestRunTurn_StreamingIdentityStamps_ReachRealStreamer(t *testing.T) {
	provider := &asyncResultStreamingProvider{content: "hello from the stamped turn"}
	al, msgBus := newProgressWiringTestLoop(t, provider)

	streamer := &identityStampRecordingStreamer{}
	msgBus.SetStreamDelegate(&identityStampStreamDelegate{streamer: streamer})

	_, agent, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "webchat",
		Sender:  bus.SenderInfo{CanonicalID: "user:1", DisplayName: "Tester"},
		ChatID:  "direct",
		Content: "hi",
	})
	require.NoError(t, err)
	require.NotNil(t, agent, "test setup: processMessage must resolve a real agent")
	require.Equal(t, "mia", agent.ID, "test setup invariant: the configured single agent's ID")

	require.NotEmpty(t, streamer.updates, "test setup invariant: the streaming path must have "+
		"engaged (ChatStream via GetStreamer), not the non-streaming Chat path — otherwise this "+
		"test proves nothing about the streaming call site")

	require.Len(t, streamer.setProducerAgentIDCalls, 1,
		"SetProducerAgentID must be called exactly once before any token flows")
	assert.Equal(t, agent.ID, streamer.setProducerAgentIDCalls[0],
		"the streamer must be stamped with the turn's own resolved agent ID (the true producer), "+
			"never a different/parent agent")
	assert.Equal(t, 0, streamer.updateCountAtStamp.producerAgentID,
		"SetProducerAgentID must land BEFORE the first token Update — a stamp that arrives after "+
			"tokens have already streamed identifies nothing for those tokens")

	require.Len(t, streamer.setTurnIDCalls, 1,
		"SetTurnID must be called exactly once before any token flows")
	turnIDFormat := regexp.MustCompile(`^` + regexp.QuoteMeta(agent.ID) + `-turn-[1-9][0-9]*$`)
	assert.Regexp(t, turnIDFormat, streamer.setTurnIDCalls[0],
		"the stamped TurnID must match the turnEventScope format the rest of the event/replay "+
			"system depends on (agentID-turn-N, al.newTurnEventScope in loop.go) — an empty or "+
			"malformed TurnID is exactly the original bug this stamp fixes: the assistant transcript "+
			"entry Finalize writes carried no TurnID at all, breaking Stop's cancel correlation and "+
			"replay's turn-scoped matching")
	assert.Equal(t, 0, streamer.updateCountAtStamp.turnID,
		"SetTurnID must land BEFORE the first token Update — this is the exact original bug: the "+
			"assistant transcript entry carried no TurnID because the stamp raced (or lost to) the "+
			"first streamed token, not just because the stamp never happened")

	require.Len(t, streamer.setParentSpawnCallIDCalls, 1,
		"SetParentSpawnCallID must be called exactly once before any token flows")
	assert.Equal(t, "", streamer.setParentSpawnCallIDCalls[0],
		"a root (non-delegated) turn must stamp an EMPTY ParentSpawnCallID — ADR-057: this is the "+
			"valid 'not a delegation child' value, distinct from the child's own separately inherited "+
			"routingSessionID, and it must still be an explicit stamped call, not a skipped one")
	assert.Equal(t, 0, streamer.updateCountAtStamp.parentSpawnCallID,
		"SetParentSpawnCallID must land BEFORE the first token Update, same as the other two stamps")
}

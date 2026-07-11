//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// See websocket_test.go for the same rationale.

// Regression coverage for a live-UAT-reported HIGH-severity bug (persona
// "Alex"): two concurrent delegate turns streaming to the same webchat
// chatID had their token deltas interleave into one garbled message —
// confirmed via screenshot, and confirmed to survive reload (the live
// view, cached/replayed by the client, is what was corrupted; each
// streamer's own Finalize-written transcript entry was already correctly
// isolated per turn before this fix — see WSHandler.streamOwners' doc
// comment in websocket.go for the full root-cause writeup).
//
// This scenario is reachable even without the companion cancel-orphan bug
// (see pkg/agent/cancel_orphan_delegate_test.go): the delegation system
// intentionally supports multiple CONCURRENT sub-turns from a single parent
// (SubTurn.MaxConcurrent), so two legitimately-concurrent delegate streams
// on the same chat session is a supported, non-buggy scenario that must
// never corrupt the wire either.

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// buildOwnershipTestStreamer constructs a wsStreamer wired to a REAL
// *webchatChannel/*WSHandler pair (so WSHandler.streamOwners is reachable —
// unlike buildWsStreamer's bare fixture, which leaves channel nil and is
// used by tests that don't exercise the ownership gate) sharing chatID, and
// returns the streamer plus its own connection's outbound frame channel.
func buildOwnershipTestStreamer(
	t *testing.T,
	h *WSHandler,
	wch *webchatChannel,
	chatID, turnID string,
) (*wsStreamer, chan []byte) {
	t.Helper()
	ch := make(chan []byte, 16)
	conn := &wsConn{
		sendCh:         ch,
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}
	s := &wsStreamer{
		conn:      conn,
		sessionID: "session-" + chatID,
		chatID:    chatID,
		channel:   wch,
	}
	s.SetTurnID(turnID)
	return s, ch
}

// drainTokenFrame reads one frame off ch and unmarshals it as a TokenFrame,
// failing the test if nothing arrives within the timeout.
func drainTokenFrame(t *testing.T, ch chan []byte, timeout time.Duration) generated.TokenFrame {
	t.Helper()
	select {
	case raw := <-ch:
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame
	case <-time.After(timeout):
		t.Fatal("timeout waiting for token frame")
		return generated.TokenFrame{}
	}
}

// assertNoFrame fails the test if ANY frame arrives on ch within the given
// window — used to prove a shadow stream withholds live delivery.
func assertNoFrame(t *testing.T, ch chan []byte, window time.Duration) {
	t.Helper()
	select {
	case raw := <-ch:
		t.Fatalf(
			"BUG REGRESSION: unexpected frame delivered to a shadow (non-owning) stream's connection: %s",
			string(raw),
		)
	case <-time.After(window):
		// expected — no frame
	}
}

// TestWsStreamer_Update_SecondConcurrentTurnOnSameChatID_WithholdsLiveFrames
// is the "point of emission" regression test: two streamers with DIFFERENT
// turnIDs sharing the same chatID — simulating an orphaned background
// delegate resurfacing concurrently with a later, unrelated delegate call
// on the same chat session — must not both push live TokenFrames. The
// first to call Update() owns the slot and streams live; the second's
// Update() calls must be withheld from the wire (though still captured in
// its own accumulator — see the companion Finalize test below) so the two
// narrations can never interleave into one message.
func TestWsStreamer_Update_SecondConcurrentTurnOnSameChatID_WithholdsLiveFrames(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(al.Close)
	wch := newWebchatChannel(h)

	const chatID = "chat-concurrent-delegates"
	streamA, chA := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-A-orphan")
	streamB, chB := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-B-later")

	// Stream A claims the slot first (simulating the orphan resurfacing first).
	require.NoError(t, streamA.Update(context.Background(), "Both background, fire-and-forget"))
	frameA := drainTokenFrame(t, chA, 2*time.Second)
	assert.Equal(t, "Both background, fire-and-forget", frameA.Content)

	// Stream B (a different, later, unrelated turn) tries to stream to the
	// SAME chatID concurrently. Its content must NOT reach the wire while A
	// still owns the slot.
	require.NoError(t, streamB.Update(context.Background(), "Good results anytime"))
	assertNoFrame(t, chB, 300*time.Millisecond)

	// A continues streaming live as normal — proves the gate doesn't just
	// block A once B shows up.
	require.NoError(t, streamA.Update(context.Background(), " — not waiting."))
	frameA2 := drainTokenFrame(t, chA, 2*time.Second)
	assert.Equal(t, " — not waiting.", frameA2.Content,
		"the owning stream must keep streaming live undisturbed by a concurrent shadow stream")

	// B still withheld.
	require.NoError(t, streamB.Update(context.Background(), " Poll me to get solid results."))
	assertNoFrame(t, chB, 300*time.Millisecond)
}

// TestWsStreamer_Finalize_ReleasesOwnership_NextConcurrentTurnBecomesLive
// proves the ownership claim is released exactly once, at true turn end
// (Finalize), so a shadow stream is not starved forever — once the owner
// finishes, the next turn to call Update() on that chatID becomes live.
func TestWsStreamer_Finalize_ReleasesOwnership_NextConcurrentTurnBecomesLive(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(al.Close)
	wch := newWebchatChannel(h)

	const chatID = "chat-ownership-release"
	streamA, chA := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-A-first")
	streamB, chB := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-B-second")

	require.NoError(t, streamA.Update(context.Background(), "first stream content"))
	drainTokenFrame(t, chA, 2*time.Second)

	// B is shadow while A owns the slot.
	require.NoError(t, streamB.Update(context.Background(), "should be withheld"))
	assertNoFrame(t, chB, 300*time.Millisecond)

	// A finishes — releases the slot.
	require.NoError(t, streamA.Finalize(context.Background(), "first stream content"))

	// A NEW streamer for a THIRD turn now claims the freshly-released slot
	// and streams live immediately.
	streamC, chC := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-C-third")
	require.NoError(t, streamC.Update(context.Background(), "third turn, now live"))
	frameC := drainTokenFrame(t, chC, 2*time.Second)
	assert.Equal(t, "third turn, now live", frameC.Content,
		"the slot must be claimable again once the previous owner's Finalize released it")
}

// TestWsStreamer_Update_SameTurnMultipleStreamerInstances_NeverTreatedAsShadow
// is the same-turn-multiple-iterations regression: a single turn typically
// opens SEVERAL sequential wsStreamer instances across its own tool-calling
// iterations (turnState.lastStreamer is overwritten each LLM call within one
// turn — see pkg/agent/loop.go). Each such instance must see itself as
// "still the owner" (same turnID), never as a foreign claimant, even though
// only the LAST instance of the turn ever has Finalize called on it.
func TestWsStreamer_Update_SameTurnMultipleStreamerInstances_NeverTreatedAsShadow(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(al.Close)
	wch := newWebchatChannel(h)

	const chatID = "chat-same-turn-multi-iter"
	const turnID = "turn-multi-iteration"

	// Iteration 1's streamer claims the slot but is never Finalized directly
	// (matches production: only the turn's LAST streamer gets Finalize
	// called — see turnState.finalizeStreamer).
	iter1, ch1 := buildOwnershipTestStreamer(t, h, wch, chatID, turnID)
	require.NoError(t, iter1.Update(context.Background(), "iteration 1 narration"))
	drainTokenFrame(t, ch1, 2*time.Second)

	// Iteration 2's streamer is a DIFFERENT instance, but the SAME turnID —
	// must still stream live, not be shadowed by its own turn's earlier claim.
	iter2, ch2 := buildOwnershipTestStreamer(t, h, wch, chatID, turnID)
	require.NoError(t, iter2.Update(context.Background(), "iteration 2 narration"))
	frame2 := drainTokenFrame(t, ch2, 2*time.Second)
	assert.Equal(t, "iteration 2 narration", frame2.Content,
		"a later streamer instance from the SAME turn must never be treated as a foreign claimant")
}

// TestWsStreamer_Finalize_ShadowStreamStillPersistsCompleteCorrectTranscript
// proves the OTHER half of the fix: a shadow (withheld) stream's content is
// NOT lost — it is fully captured in its own private accumulator and
// written out as a complete, correct, separately-attributed transcript
// entry by Finalize, exactly like before this fix. Only the LIVE wire
// delivery is gated; persistence is unaffected.
func TestWsStreamer_Finalize_ShadowStreamStillPersistsCompleteCorrectTranscript(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(al.Close)
	wch := newWebchatChannel(h)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	const chatID = "chat-shadow-persists"
	owner, chOwner := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-owner")
	owner.sessionID = meta.ID
	owner.agentStore = store

	shadow, chShadow := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-shadow-delegate")
	shadow.sessionID = meta.ID
	shadow.agentStore = store
	shadow.SetProducerAgentID("worker-delegate")

	require.NoError(t, owner.Update(context.Background(), "owner content"))
	drainTokenFrame(t, chOwner, 2*time.Second)

	require.NoError(t, shadow.Update(context.Background(), "FINAL_SYNTHESIZED_ANSWER-delegate research complete"))
	assertNoFrame(t, chShadow, 300*time.Millisecond)

	require.NoError(t, shadow.Finalize(context.Background(), "FINAL_SYNTHESIZED_ANSWER-delegate research complete"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var shadowEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].TurnID == "turn-shadow-delegate" {
			shadowEntry = &entries[i]
		}
	}
	require.NotNil(t, shadowEntry, "the shadow stream's own transcript entry must still be written")
	assert.Equal(t, "FINAL_SYNTHESIZED_ANSWER-delegate research complete", shadowEntry.Content,
		"a shadow stream's content must be fully and correctly persisted even though live delivery was withheld")
	assert.Equal(t, "worker-delegate", shadowEntry.AgentID)
}

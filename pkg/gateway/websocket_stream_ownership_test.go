//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// See websocket_test.go for the same rationale.

// Regression coverage for a live-UAT-reported HIGH-severity bug: two
// concurrent delegate turns streaming to the same webchat chatID had their
// token deltas interleave into one garbled message — confirmed via
// screenshot, and confirmed to survive reload (the live view, cached/replayed
// by the client, is what was corrupted; each streamer's own Finalize-written
// transcript entry was already correctly isolated per turn before this fix —
// see WSHandler.streamOwners' doc comment in websocket.go for the full
// root-cause writeup).
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
	"sync"
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

// TestWsStreamer_AbandonedPathReleasesOwnership_NextConcurrentTurnBecomesLive
// is the regression test for a CRITICAL bug unanimously flagged by a
// 7-reviewer gate (architect, silent-failure-hunter, type-design-analyzer,
// pr-test-analyzer): turnState.finalizeStreamer's B4 abandoned-turn early
// return (pkg/agent/turn.go) skips the whole Finalize call — Finalize being
// the ONLY place a wsStreamer released its WSHandler.streamOwners live-
// stream ownership claim before this fix. A background delegate that became
// the live owner for a chatID and was later MarkAbandoned()'d (cancel.go
// PHASE C, 5s after a hard-abort escalation) left that chatID's ownership
// entry stuck on the dead turn's ID forever — no TTL, no sweep, no
// WS-teardown hook ever touches streamOwners — permanently shadowing every
// later turn on the same chat.
//
// This test simulates that exact abandoned path by calling
// streamA.ReleaseStreamOwnership() directly instead of streamA.Finalize(...)
// — precisely mirroring what turnState.finalizeStreamer's abandoned branch
// now does via the streamOwnershipReleaser optional interface (pkg/agent
// cannot be imported here — gateway imports agent, not the reverse — so this
// exercises the SAME production method pkg/agent's type assertion resolves
// to on a real *wsStreamer; see TestTurnState_Abandoned_ReleasesStreamOwnership
// in pkg/agent/turn_test.go for the companion test proving finalizeStreamer
// actually calls it).
//
// BDD: Given stream A has claimed live-stream ownership for a chatID (via
// Update) and a second stream B on the same chatID is consequently shadowed,
// When A's turn is abandoned — releasing ownership via
// ReleaseStreamOwnership instead of the normal Finalize path — and a THIRD
// stream C (a later, unrelated turn) then calls Update on the same chatID,
// Then C successfully claims live ownership and its frames are delivered —
// the chatID is NOT permanently shadowed.
func TestWsStreamer_AbandonedPathReleasesOwnership_NextConcurrentTurnBecomesLive(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(al.Close)
	wch := newWebchatChannel(h)

	const chatID = "chat-abandoned-releases-ownership"
	streamA, chA := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-A-abandoned")
	streamB, chB := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-B-shadow")

	// A claims the slot first (simulating the background delegate that will
	// later be abandoned).
	require.NoError(t, streamA.Update(context.Background(), "delegate narration before stop"))
	drainTokenFrame(t, chA, 2*time.Second)

	// B is shadow while A owns the slot — proves the claim really is held.
	require.NoError(t, streamB.Update(context.Background(), "should be withheld while A owns the slot"))
	assertNoFrame(t, chB, 300*time.Millisecond)

	// A's turn is abandoned (cancel.go PHASE C): finalizeStreamer's B4 branch
	// never calls Finalize, only ReleaseStreamOwnership via the optional
	// interface. Simulate exactly that here.
	streamA.ReleaseStreamOwnership()

	// A THIRD, later, unrelated turn now claims the freshly-released slot.
	// BUG REGRESSION: before this fix, nothing ever released A's claim once
	// it was abandoned, so this Update would be silently withheld forever —
	// the chatID permanently shadowed.
	streamC, chC := buildOwnershipTestStreamer(t, h, wch, chatID, "turn-C-later")
	require.NoError(t, streamC.Update(context.Background(), "later turn, must stream live"))
	frameC := drainTokenFrame(t, chC, 2*time.Second)
	assert.Equal(t, "later turn, must stream live", frameC.Content,
		"BUG REGRESSION: an abandoned turn's live-stream ownership claim must be released "+
			"even though Finalize is never called, or every later turn on the same chatID is "+
			"permanently shadowed")

	// Idempotency: releasing an already-released (or never-held) claim must
	// not panic or affect a different, still-live owner.
	require.NotPanics(t, func() { streamA.ReleaseStreamOwnership() })
	require.NoError(t, streamC.Update(context.Background(), " still live after a redundant release call"))
	frameC2 := drainTokenFrame(t, chC, 2*time.Second)
	assert.Equal(t, " still live after a redundant release call", frameC2.Content)
}

// TestClaimStreamOwnership_StaleClaimIsForceReclaimed exercises the
// defense-in-depth backstop (streamOwnershipStaleAfter): an unreleased claim
// older than the staleness threshold degrades to "reclaimable by a new
// turn" rather than shadowing a chatID forever, protecting against any
// FUTURE bug in this family (not just the abandoned-turn path this wave
// fixed directly).
func TestClaimStreamOwnership_StaleClaimIsForceReclaimed(t *testing.T) {
	var owners sync.Map
	owners.Store("chat-stale", streamOwnerClaim{
		turnID:    "turn-old-leaked",
		claimedAt: time.Now().Add(-streamOwnershipStaleAfter - time.Minute),
	})

	ok := claimStreamOwnership(&owners, "chat-stale", "turn-new")
	assert.True(t, ok, "a claim older than streamOwnershipStaleAfter must be force-reclaimable by a new turn")

	actual, loaded := owners.Load("chat-stale")
	require.True(t, loaded)
	claim, ok := actual.(streamOwnerClaim)
	require.True(t, ok)
	assert.Equal(t, "turn-new", claim.turnID, "the stored claim must now belong to the reclaiming turn")
}

// TestClaimStreamOwnership_FreshClaimIsNotReclaimed proves the staleness
// backstop does not weaken the normal, fast-path ownership gate: a claim
// well within streamOwnershipStaleAfter held by a different turn must still
// deny a concurrent claimant, exactly like before the staleness feature was
// added.
func TestClaimStreamOwnership_FreshClaimIsNotReclaimed(t *testing.T) {
	var owners sync.Map
	owners.Store("chat-fresh", streamOwnerClaim{
		turnID:    "turn-current-owner",
		claimedAt: time.Now(),
	})

	ok := claimStreamOwnership(&owners, "chat-fresh", "turn-other")
	assert.False(t, ok, "a fresh (non-stale) claim held by a different turn must not be reclaimed")

	actual, loaded := owners.Load("chat-fresh")
	require.True(t, loaded)
	claim := actual.(streamOwnerClaim)
	assert.Equal(t, "turn-current-owner", claim.turnID, "the original owner's claim must be untouched")
}

// websocket_streamer_test.go: tests for the wsStreamer: start, write, finalize

package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from websocket_pump.go tests 2026-09-15 ---

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
	claim, claimOk := actual.(streamOwnerClaim)
	require.True(t, claimOk, "stored owner must be a streamOwnerClaim")
	assert.Equal(t, "turn-current-owner", claim.turnID, "the original owner's claim must be untouched")
}

// TestGetStreamer_WebchatAlwaysNonNil proves FR-003/S-05: a webchat
// GetStreamer call with a real session id but ZERO bound connections still
// returns a non-nil streamer and ok=true.
func TestGetStreamer_WebchatAlwaysNonNil(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Deliberately do NOT bind any connection to meta.ID — this is the
	// "only viewer disconnected mid-turn" / "keeper follow-up, no viewer at
	// all" scenario.
	streamer, ok := handler.GetStreamer(context.Background(), "webchat", "chat-no-conn", meta.ID)
	require.True(t, ok, "GetStreamer must return true for webchat even with zero bound connections")
	require.NotNil(t, streamer, "GetStreamer must return a non-nil streamer")
}

// TestGetStreamer_NonWebchatChannelReturnsFalse proves the channel != "webchat"
// early-return is unchanged.
func TestGetStreamer_NonWebchatChannelReturnsFalse(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	streamer, ok := handler.GetStreamer(context.Background(), "telegram", "chat-x", "session-x")
	assert.False(t, ok)
	assert.Nil(t, streamer)
}

// TestGetStreamer_EmptySessionIDAndNoBinding_ReturnsFalse proves that a
// caller supplying neither a session id nor a chatID with an existing
// binding still gets a clean "no streamer" result rather than a panic or a
// streamer with no usable session.
func TestGetStreamer_EmptySessionIDAndNoBinding_ReturnsFalse(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	streamer, ok := handler.GetStreamer(context.Background(), "webchat", "chat-unbound", "")
	assert.False(t, ok)
	assert.Nil(t, streamer)
}

// TestGetStreamer_RegistersLiveStreamer proves GetStreamer registers the
// streamer in h.liveStreamers keyed by session id (ADR-082 D3/D4's
// prerequisite for catch-up snapshot and active_turn reporting).
func TestGetStreamer_RegistersLiveStreamer(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	streamer, ok := handler.GetStreamer(context.Background(), "webchat", "chat-reg", meta.ID)
	require.True(t, ok)

	handler.mu.Lock()
	registered, exists := handler.liveStreamers[meta.ID]
	handler.mu.Unlock()
	require.True(t, exists, "GetStreamer must register the streamer in liveStreamers")
	assert.Same(t, streamer, registered)
}

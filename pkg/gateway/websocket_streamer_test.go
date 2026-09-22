// websocket_streamer_test.go: tests for the wsStreamer: start, write, finalize

package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGatewaySteerAudience is a minimal steer.AudienceResolver double, keyed
// by session id, for ADR-091 boundary 6 tests.
type fakeGatewaySteerAudience struct {
	audience map[string]steer.Audience
}

var _ steer.AudienceResolver = (*fakeGatewaySteerAudience)(nil)

func (f *fakeGatewaySteerAudience) Audience(_ context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	a, ok := f.audience[sessionID]
	if !ok {
		return steer.AudienceUser, steer.ClassOrdinaryRoot, nil
	}
	return a, steer.ClassSteered, nil
}

// recordingGatewayObserver is a minimal steer.BoundaryObserver double.
type recordingGatewayObserver struct {
	calls []steer.Boundary
}

var _ steer.BoundaryObserver = (*recordingGatewayObserver)(nil)

func (r *recordingGatewayObserver) Observe(b steer.Boundary, _ string, _ steer.Audience) {
	r.calls = append(r.calls, b)
}

// TestWsStreamer_SteeredSession_ShadowedByAudience proves ADR-091 boundary 6
// (landing order §6, FR-B-001/FR-B-014): a steered session's live token
// stream is contained by the injected steer.AudienceResolver alone (no
// parentSpawnCallID stamp) — resolved once, lazily, on the streamer's first
// Update() call, and the boundary is proven exercised via Observe.
func TestWsStreamer_SteeredSession_ShadowedByAudience(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	t.Cleanup(func() { SetGatewaySteerAudienceDeps(nil, nil) })

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	obs := &recordingGatewayObserver{}
	SetGatewaySteerAudienceDeps(&fakeGatewaySteerAudience{
		audience: map[string]steer.Audience{meta.ID: steer.AudienceSteeringSession},
	}, obs)

	streamerIface, ok := handler.GetStreamer(context.Background(), "webchat", "chat-steered", meta.ID)
	require.True(t, ok)
	streamer, ok := streamerIface.(*wsStreamer)
	require.True(t, ok, "GetStreamer for webchat must return a *wsStreamer")

	require.NoError(t, streamer.Update(context.Background(), "child narration"))

	assert.True(t, streamer.isShadowStream, "a steered session's stream must be shadowed by audience alone")
	if len(obs.calls) != 1 || obs.calls[0] != steer.BoundaryWebchatStreaming {
		t.Fatalf("expected exactly one Observe(webchat_streaming, ...) call, got %+v", obs.calls)
	}
}

// TestWsStreamer_OrdinaryRootSession_UnaffectedByAudience proves a wired
// resolver does not interfere with an ordinary root's own live stream.
func TestWsStreamer_OrdinaryRootSession_UnaffectedByAudience(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	t.Cleanup(func() { SetGatewaySteerAudienceDeps(nil, nil) })

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	SetGatewaySteerAudienceDeps(&fakeGatewaySteerAudience{audience: map[string]steer.Audience{}}, nil)

	streamerIface, ok := handler.GetStreamer(context.Background(), "webchat", "chat-root", meta.ID)
	require.True(t, ok)
	streamer, ok := streamerIface.(*wsStreamer)
	require.True(t, ok)

	require.NoError(t, streamer.Update(context.Background(), "root text"))
	assert.False(t, streamer.isShadowStream, "an ordinary root session's stream must not be shadowed")
}

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

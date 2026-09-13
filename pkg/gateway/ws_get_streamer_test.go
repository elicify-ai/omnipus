// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_get_streamer_test.go — unit coverage for ADR-082 D2/FR-003:
// WSHandler.GetStreamer must return a non-nil streamer for every webchat
// turn that carries a session id, even with zero bound connections — this is
// what keeps the webchat path on ChatStream for every LLM round instead of
// silently degrading to a non-streaming Chat call the moment the
// originating connection closes (ADR-082 §2 evidence E4).

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

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

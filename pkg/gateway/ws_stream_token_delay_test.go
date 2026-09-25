// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// timeTwoTokens publishes two token frames through a fresh handler's web
// streamer and returns how long the two Update calls took, plus how many
// tokens reached the session's journal.
func timeTwoTokens(t *testing.T) (time.Duration, int) {
	t.Helper()
	h := makeMinimalHandler()
	s := &wsStreamer{sessionID: "sess-delay", chatID: "chat-delay", h: h}
	s.SetTurnID("turn-delay")
	start := time.Now()
	require.NoError(t, s.Update(context.Background(), "a"))
	require.NoError(t, s.Update(context.Background(), "b"))
	elapsed := time.Since(start)
	hub := h.hubs.lookup("sess-delay")
	require.NotNil(t, hub)
	return elapsed, len(journalFramesOfType(t, hub, "token"))
}

// TestHub_StreamTokenDelay_TestOnlyKnob pins the e2e scenario-h shortcut:
// the test-only env var OMNIPUS_TEST_ONLY_STREAM_TOKEN_DELAY_MS makes the web
// streamer pause after each published token, so a real-browser test can kill
// the gateway mid-answer. Unset (every real install), zero or invalid, it
// changes nothing — read once when the hub registry is built, never on the
// token path.
func TestHub_StreamTokenDelay_TestOnlyKnob(t *testing.T) {
	t.Run("unset: no delay", func(t *testing.T) {
		os.Unsetenv(streamTokenDelayEnvOverrideVar)
		require.Zero(t, newHubRegistry("boot-1").streamTokenDelay)
		elapsed, tokens := timeTwoTokens(t)
		assert.Equal(t, 2, tokens)
		assert.Less(t, elapsed, 150*time.Millisecond, "no knob, no pause")
	})
	t.Run("zero and invalid: no delay", func(t *testing.T) {
		for _, v := range []string{"0", "-5", "soon"} {
			t.Setenv(streamTokenDelayEnvOverrideVar, v)
			assert.Zero(t, newHubRegistry("boot-1").streamTokenDelay, "value %q", v)
		}
	})
	t.Run("set: each published token is followed by the pause", func(t *testing.T) {
		t.Setenv(streamTokenDelayEnvOverrideVar, "200")
		require.Equal(t, 200*time.Millisecond, newHubRegistry("boot-1").streamTokenDelay)
		elapsed, tokens := timeTwoTokens(t)
		assert.Equal(t, 2, tokens, "the pause delays tokens, never drops them")
		assert.GreaterOrEqual(t, elapsed, 400*time.Millisecond)
	})
}

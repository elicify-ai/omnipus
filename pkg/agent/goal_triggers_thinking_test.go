// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED test for the goal-evaluation reader (spec section 8.1 row for
// pkg/agent/goal_loop.go / pkg/agent/goal_triggers.go: "Exclude (explicit
// type filter) - goal evaluation is role-keyed today
// (pkg/agent/goal_triggers.go matches e.Role == "user"), so 'naturally
// excluded' is not a guarantee; the reader states its thinking exclusion
// explicitly").
//
// Oracle: liftGoalKeeperStopPauseIfNewTurn decides "the user asked for a new
// turn" from the transcript. A thinking entry - even one with role "user"
// PLANTED - must never lift the Stop-pause. RED today: the backward scan
// matches e.Role == "user" with no type filter, so a planted-role thinking
// entry lifts the pause. Positive control: a real user entry after the Stop
// still lifts it (the instrument can see a genuine lift).

func wpcNewHydrationStyleLoop(t *testing.T) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	return al
}

func wpcGoalCleanupPauses(t *testing.T, sessionIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		s := goalTriggers()
		s.mu.Lock()
		for _, id := range sessionIDs {
			delete(s.keeperPausedByStop, id)
		}
		s.mu.Unlock()
	})
}

func TestLiftGoalKeeperStopPause_ThinkingNeverCountsAsNewTurn(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)

	t.Run("planted-role thinking entry never lifts the Stop-pause", func(t *testing.T) {
		al := wpcNewHydrationStyleLoop(t)
		const sessionID = "wpc-goal-lift-1"
		wpcGoalCleanupPauses(t, sessionID)

		al.pauseGoalKeeperForStop(sessionID, "ws")
		pausedAt, paused := al.goalKeeperStopPausedAt(sessionID)
		require.True(t, paused, "instrument check: the Stop-pause must be registered")

		now := time.Now().UTC()
		require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
			Role: "user", Content: "older instruction from before the Stop",
			Timestamp: pausedAt.Add(-time.Minute),
		}))
		require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
			Type:         session.EntryTypeThinking,
			Role:         "user", // PLANTED - the explicit-filter case the spec names
			Content:      "",
			ThinkingText: "a new turn was requested " + thinkingSentinel,
			Timestamp:    pausedAt.Add(time.Minute),
		}))

		assert.False(t, al.liftGoalKeeperStopPauseIfNewTurn(store, sessionID),
			"a thinking entry - even with role user PLANTED - is not a new turn; the Stop-pause must hold (section 8.1)")
	})

	t.Run("real user entry after the Stop still lifts", func(t *testing.T) {
		al := wpcNewHydrationStyleLoop(t)
		const sessionID = "wpc-goal-lift-2"
		wpcGoalCleanupPauses(t, sessionID)

		al.pauseGoalKeeperForStop(sessionID, "ws")
		pausedAt, paused := al.goalKeeperStopPausedAt(sessionID)
		require.True(t, paused, "instrument check: the Stop-pause must be registered")

		now := time.Now().UTC()
		require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
			Role: "user", Content: "older instruction from before the Stop",
			Timestamp: pausedAt.Add(-time.Minute),
		}))
		require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
			Role: "user", Content: "actual new instruction after the Stop",
			Timestamp: pausedAt.Add(time.Minute),
		}))

		assert.True(t, al.liftGoalKeeperStopPauseIfNewTurn(store, sessionID),
			"instrument check: a real user entry after the Stop MUST lift the pause - "+
				"if this fails the lift mechanism itself is broken and the exclusion test proves nothing")
	})

}

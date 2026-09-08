// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// active_turn_info_test.go — regression coverage for ADR-082 review F5/S2:
// AgentLoop.ActiveForegroundTurnInfo must (1) never report a turnState that
// is still present in al.activeTurnStates but has already Finish()ed, and
// (2) return a deterministic answer when more than one live root turnState
// shares a transcriptSessionID, instead of whatever sync.Map.Range's
// randomized iteration order happens to visit first.

package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFix_F5_ActiveForegroundTurnInfo_RequiresIsAlive proves the IsAlive()
// gate: clearActiveTurn (which removes a turnState from al.activeTurnStates)
// and finalizeStreamer both run as separate, independently-registered defers
// in runTurn — Go's LIFO defer order means clearActiveTurn actually fires
// BEFORE finalizeStreamer, and Finish() itself (which sets isFinished) runs
// LAST of the three. A turnState can therefore be observed here in a state
// where Finish() has already run but activeTurnStates has not yet been
// cleared (or, more generally, any caller racing the removal). Before this
// fix, ActiveForegroundTurnInfo had no liveness check at all, so a
// finished-but-not-yet-cleared root would be reported as "in flight" —
// session_state.active_turn would re-arm the SPA's Stop control for a turn
// that has already ended.
func TestFix_F5_ActiveForegroundTurnInfo_RequiresIsAlive(t *testing.T) {
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	sessionID := fmt.Sprintf("f5-finished-session-%d", nonce)

	ts := &turnState{
		turnID:              "f5-finished-turn",
		sessionKey:          sessionID,
		transcriptSessionID: sessionID,
		agentID:             "mia",
		startedAt:           time.Now(),
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(ts)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(ts.sessionKey, ts) })

	// While alive, ActiveForegroundTurnInfo must report it.
	turnID, agentID, _, ok := al.ActiveForegroundTurnInfo(sessionID)
	require.True(t, ok, "a live root turn must be reported as in flight")
	assert.Equal(t, "f5-finished-turn", turnID)
	assert.Equal(t, "mia", agentID)

	// Finish it WITHOUT clearing it from activeTurnStates — reproducing the
	// exact race this fix closes: clearActiveTurn is a separate step from
	// Finish itself, and both are independent defers in runTurn.
	ts.Finish(false)
	require.False(t, ts.IsAlive())

	turnID, agentID, _, ok = al.ActiveForegroundTurnInfo(sessionID)
	assert.False(t, ok,
		"BUG REGRESSION: a finished turnState still present in activeTurnStates must not be "+
			"reported as in flight — session_state.active_turn would otherwise re-arm Stop for a "+
			"turn that has already ended")
	assert.Empty(t, turnID)
	assert.Empty(t, agentID)
}

// TestFix_F5_ActiveForegroundTurnInfo_DeterministicWithTwoLiveRoots proves
// the second half of the fix: when two LIVE root turnStates share the same
// transcriptSessionID (rare, but not impossible), ActiveForegroundTurnInfo
// must return the SAME answer on every call — deterministically preferring
// the earlier-started turn — rather than whatever sync.Map.Range's
// randomized iteration order happens to visit first.
func TestFix_F5_ActiveForegroundTurnInfo_DeterministicWithTwoLiveRoots(t *testing.T) {
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	sessionID := fmt.Sprintf("f5-two-roots-session-%d", nonce)

	earlier := &turnState{
		turnID:              "f5-earlier-turn",
		sessionKey:          sessionID + "-a",
		transcriptSessionID: sessionID,
		agentID:             "mia",
		startedAt:           time.Now().Add(-time.Minute),
		finishedChan:        make(chan struct{}),
	}
	later := &turnState{
		turnID:              "f5-later-turn",
		sessionKey:          sessionID + "-b",
		transcriptSessionID: sessionID,
		agentID:             "jim",
		startedAt:           time.Now(),
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(earlier)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(earlier.sessionKey, earlier) })
	al.registerActiveTurn(later)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(later.sessionKey, later) })

	for i := 0; i < 20; i++ {
		turnID, agentID, _, ok := al.ActiveForegroundTurnInfo(sessionID)
		require.True(t, ok)
		assert.Equal(t, "f5-earlier-turn", turnID,
			"must deterministically prefer the earlier-started root on every call (iteration %d)", i)
		assert.Equal(t, "mia", agentID)
	}
}

// TestFix_F5_ActiveForegroundTurnInfo_IgnoresNonRootDescendant proves the
// pre-existing root-exclusivity behavior survives the fix unchanged: a
// delegated (non-root) sub-turn sharing the same transcriptSessionID must
// never be reported, even when it is the ONLY live entry for that session.
func TestFix_F5_ActiveForegroundTurnInfo_IgnoresNonRootDescendant(t *testing.T) {
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	sessionID := fmt.Sprintf("f5-nonroot-session-%d", nonce)

	child := &turnState{
		turnID:              "f5-child-turn",
		sessionKey:          sessionID + "-child",
		transcriptSessionID: sessionID,
		depth:               1,
		parentTurnID:        "f5-parent-turn",
		agentID:             "ray",
		startedAt:           time.Now(),
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(child)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(child.sessionKey, child) })

	turnID, _, _, ok := al.ActiveForegroundTurnInfo(sessionID)
	assert.False(t, ok, "a non-root descendant must never count as the session's foreground turn")
	assert.Empty(t, turnID)
}

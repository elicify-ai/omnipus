// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression/unit coverage for the orphan-foreground-turn watchdog
// (ADR-045, pkg/agent/orphan_watch.go). The safety-critical property under
// test is that the watchdog is TURN-SCOPED, not session-wide: a live
// Critical/background delegate sub-turn sharing the armed session's
// transcriptSessionID must survive the full escalation (graceful nudge,
// turn-scoped hard abort, turn-scoped detach) untouched, exactly mirroring
// the protection TestRequestCancel_OrphanedBackgroundDelegate_...
// (cancel_orphan_delegate_test.go) already proves for the real user-cancel
// path.
//
// orphanWatchHardAbortDelay/orphanWatchDetachDelay are shrunk for the
// duration of each cascade test (and restored via t.Cleanup) so these tests
// don't pay the full production 3s+5s wall-clock cost. None of these tests
// use t.Parallel(), so mutating those package-level vars is race-free: Go's
// testing framework only begins running parallel-marked tests concurrently
// once every non-parallel top-level test (these included) has already
// returned — see testing.T.Parallel's documented pause/resume model.
package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// shrinkOrphanWatchTimersForTest overrides the PHASE B/C escalation delays
// to short durations for the lifetime of the calling test, restoring the
// real production values via t.Cleanup. Not safe to call from a
// t.Parallel() test (see package doc comment above).
func shrinkOrphanWatchTimersForTest(t *testing.T, hardAbortDelay, detachDelay time.Duration) {
	t.Helper()
	origHard, origDetach := orphanWatchHardAbortDelay, orphanWatchDetachDelay
	orphanWatchHardAbortDelay = hardAbortDelay
	orphanWatchDetachDelay = detachDelay
	t.Cleanup(func() {
		orphanWatchHardAbortDelay = origHard
		orphanWatchDetachDelay = origDetach
	})
}

// newOrphanTestAgentLoop builds a minimal AgentLoop + shared session store for
// the orphan-watch tests, mirroring cancel_orphan_delegate_test.go's setup.
func newOrphanTestAgentLoop(t *testing.T) (*AgentLoop, string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "orphan-watch-test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	return al, meta.ID
}

// TestOrphanWatch_GraceElapses_TurnScopedHardAbortFires is
// the basic end-to-end escalation case: a lone root turn (no descendants)
// that never finishes on its own must be turn-scoped hard-aborted once the
// grace period AND the PHASE B window elapse.
func TestOrphanWatch_GraceElapses_TurnScopedHardAbortFires(t *testing.T) {
	shrinkOrphanWatchTimersForTest(t, 150*time.Millisecond, 150*time.Millisecond)
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-basic-fire",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     al.GetSessionStore(),
		// providerCancel intentionally nil: this bare turnState never finishes
		// on its own, so PHASE A's graceful nudge alone cannot terminate it —
		// PHASE B's turn-scoped hard abort must be the thing that fires.
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	al.ArmOrphanForegroundTurnWatch(sessionID, 1)

	require.Eventually(t, rootTS.hardAbortRequested, 5*time.Second, 20*time.Millisecond,
		"the orphaned root turn must be turn-scoped hard-aborted once grace + PHASE B elapse")
	assert.True(t, rootTS.IsAlive(), "a bare test turnState with no real loop never observes its own hardAbort flag and finishes — this just confirms hard-abort didn't panic/crash anything")
}

// TestOrphanWatch_DisarmBeforeGrace_NoOp proves Disarm
// called before the grace timer fires cancels the watch entirely — the turn
// is never touched at all, not even the PHASE A graceful nudge.
func TestOrphanWatch_DisarmBeforeGrace_NoOp(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-disarm",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	al.ArmOrphanForegroundTurnWatch(sessionID, 1)
	al.DisarmOrphanForegroundTurnWatch(sessionID)

	// Wait past the grace period with margin. Because Disarm stops the
	// INITIAL timer, fireOrphanForegroundTurnWatch never runs at all, so
	// there is no need to wait through PHASE B/C — they are only ever
	// scheduled from within that function.
	time.Sleep(1500 * time.Millisecond)

	graceful, _ := rootTS.gracefulInterruptRequested()
	assert.False(t, graceful, "a disarmed watch must never send even the graceful nudge")
	assert.False(t, rootTS.hardAbortRequested())
	assert.True(t, rootTS.IsAlive())

	_, armed := al.orphanWatches.Load(sessionID)
	assert.False(t, armed, "the disarmed watch must be removed from the map")
}

// TestOrphanWatch_CriticalDescendantSurvives is the
// safety-critical regression test: a background/Critical delegate sub-turn
// sharing the armed session's transcriptSessionID must survive the FULL
// escalation (graceful, turn-scoped hard, turn-scoped detach) untouched,
// even though the root turn it descends from finishes gracefully almost
// immediately (the common case — mirrors
// TestRequestCancel_OrphanedBackgroundDelegate_HardAbortedAfterParentGracefullyFinishes's
// scenario, but for the ORPHAN-WATCHDOG path instead of RequestCancel).
func TestOrphanWatch_CriticalDescendantSurvives(t *testing.T) {
	shrinkOrphanWatchTimersForTest(t, 150*time.Millisecond, 150*time.Millisecond)
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-with-descendant",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     al.GetSessionStore(),
	}
	// Simulates the common case: PHASE A's providerCancel call unwinds the
	// root's own in-flight LLM call almost instantly.
	rootTS.providerCancel = func() { rootTS.Finish(false) }

	childTS := &turnState{
		turnID:              "child-critical-delegate",
		transcriptSessionID: sessionID, // shares the parent's session
		depth:               1,
		parentTurnID:        rootTS.turnID,
		parentTurnState:     rootTS,
		finishedChan:        make(chan struct{}),
		// providerCancel intentionally nil: an orphaned background delegate
		// that ignores its own graceful nudge (mid multi-tool task) is
		// exactly what this test simulates.
	}

	al.activeTurnStates.Store(sessionID, rootTS)
	al.activeTurnStates.Store(childTS.turnID, childTS)
	defer al.activeTurnStates.Delete(sessionID)
	defer al.activeTurnStates.Delete(childTS.turnID)

	al.ArmOrphanForegroundTurnWatch(sessionID, 1)

	// The root must finish gracefully shortly after PHASE A fires.
	require.Eventually(t, func() bool { return !rootTS.IsAlive() }, 3*time.Second, 10*time.Millisecond,
		"root turn must finish gracefully shortly after the watchdog's PHASE A cascade fires")

	// Give PHASE B and PHASE C their full (shrunk) windows to run, then
	// assert the child was NEVER touched at any point.
	time.Sleep(1 * time.Second)

	assert.True(t, childTS.IsAlive(), "the descendant must survive the entire escalation — turn-scoped means it is structurally unreachable")
	assert.False(t, childTS.hardAbortRequested(), "BUG REGRESSION: a Critical/background delegate must never be turn-scoped hard-aborted by the orphan watchdog")
}

// TestOrphanWatch_ReArmReplacesPriorTimer proves that
// arming a session that already has a pending watch cancels the OLD watch
// and installs a new one — at most one timer is ever pending per session.
func TestOrphanWatch_ReArmReplacesPriorTimer(t *testing.T) {
	shrinkOrphanWatchTimersForTest(t, 150*time.Millisecond, 150*time.Millisecond)
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-rearm",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	// Arm with a long grace first (would not fire within this test's budget).
	al.ArmOrphanForegroundTurnWatch(sessionID, 100)

	oldVal, ok := al.orphanWatches.Load(sessionID)
	require.True(t, ok, "first arm must register a watch")
	oldWatch, ok := oldVal.(*orphanWatch)
	require.True(t, ok)

	// Re-arm with a short grace — must cancel and replace the long-grace watch.
	al.ArmOrphanForegroundTurnWatch(sessionID, 1)

	assert.True(t, oldWatch.isCanceled(), "the prior (long-grace) watch must be canceled by the re-arm")

	newVal, ok := al.orphanWatches.Load(sessionID)
	require.True(t, ok)
	newWatch, ok := newVal.(*orphanWatch)
	require.True(t, ok)
	assert.NotSame(t, oldWatch, newWatch, "re-arm must install a NEW watch instance, not mutate the old one")

	// Proof the short-grace watch is the one actually driving behavior: if
	// the long-grace timer had NOT been canceled/replaced, this would never
	// go true within the test's bounded wait (its own grace is 100s).
	require.Eventually(t, rootTS.hardAbortRequested, 5*time.Second, 20*time.Millisecond,
		"the re-armed (short-grace) watch must be the one that actually escalates")
}

// TestOrphanWatch_GraceZeroOrNegative_Disabled proves the
// watchdog is a no-op (disabled) for graceSeconds <= 0 — no timer is ever
// registered and the turn is left completely untouched.
func TestOrphanWatch_GraceZeroOrNegative_Disabled(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-disabled",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	for _, grace := range []int{0, -1, -300} {
		al.ArmOrphanForegroundTurnWatch(sessionID, grace)
		_, armed := al.orphanWatches.Load(sessionID)
		assert.False(t, armed, "graceSeconds=%d must never register a watch", grace)
	}

	time.Sleep(1200 * time.Millisecond)
	graceful, _ := rootTS.gracefulInterruptRequested()
	assert.False(t, graceful)
	assert.False(t, rootTS.hardAbortRequested())
	assert.True(t, rootTS.IsAlive())
}

// TestOrphanWatch_TurnIDMismatch_ReplacementTurnUntouched
// proves the "turn finished naturally and a DIFFERENT turn now owns the
// session" case is a harmless no-op: the watch was armed for turnID
// "root-v1"; by the time it fires, "root-v1" has finished and a brand-new
// "root-v2" (same session, unrelated to the armed watch) has taken its
// place. The watchdog must never touch root-v2.
func TestOrphanWatch_TurnIDMismatch_ReplacementTurnUntouched(t *testing.T) {
	shrinkOrphanWatchTimersForTest(t, 150*time.Millisecond, 150*time.Millisecond)
	al, sessionID := newOrphanTestAgentLoop(t)

	rootV1 := &turnState{
		turnID:              "root-v1",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootV1)

	al.ArmOrphanForegroundTurnWatch(sessionID, 1)

	// Simulate "root-v1 finished naturally and a brand new turn started on
	// the same session before the grace timer fired" — e.g. a scheduled
	// follow-up or a fresh user message via another channel.
	rootV1.Finish(false)
	rootV2 := &turnState{
		turnID:              "root-v2",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootV2)
	defer al.activeTurnStates.Delete(sessionID)

	// Wait past grace + PHASE B + PHASE C with margin.
	time.Sleep(1200 * time.Millisecond)

	graceful, _ := rootV2.gracefulInterruptRequested()
	assert.False(t, graceful, "BUG: the watchdog must not act on a turn it was never armed for")
	assert.False(t, rootV2.hardAbortRequested())
	assert.True(t, rootV2.IsAlive())
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression/unit coverage for the orphan-foreground-turn watchdog
// (ADR-045, pkg/agent/orphan_watch.go), redesigned (2026-07) to reap an
// abandoned foreground turn by calling into RequestCancel (the SAME
// cancellation state machine every other cancel surface uses) rather than
// reimplementing its own turn-scoped PHASE A/B/C escalation.
//
// The safety-critical property under test is unchanged from the original
// design's intent, even though the mechanism is now completely different:
// a live Critical/background delegate sub-turn sharing the armed session's
// transcriptSessionID must NEVER be reaped by this mechanism — see
// TestOrphanWatch_RootAlreadyClearedFromActiveTurnStates_CriticalDelegateNeverReaped
// and TestOrphanWatch_LiveRootWithLiveCriticalDelegate_ReapDeferred below,
// which mirror the protection
// TestRequestCancel_OrphanedBackgroundDelegate_HardAbortedAfterParentGracefullyFinishes
// (cancel_orphan_delegate_test.go) already proves for the real user-cancel
// path — except here the correct behavior is to DEFER reaping entirely
// (RequestCancel is never even called) rather than to escalate around it.
//
// Because there is only a single grace timer now (no PHASE B/C sub-timers),
// these tests no longer need to shrink package-level escalation-delay vars —
// grace is passed directly to ArmOrphanForegroundTurnWatch in whole seconds.

package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newOrphanTestAgentLoop builds a minimal AgentLoop + shared session store for
// the orphan-watch tests, mirroring cancel_orphan_delegate_test.go's setup.
// The returned sessionID belongs to a real SessionTypeChat session, so MA-2's
// session-type gate never blocks arming in tests that don't specifically
// exercise it.
func newOrphanTestAgentLoop(t *testing.T) (*AgentLoop, string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "orphan-watch-test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
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

// alwaysOrphaned is a stillOrphaned callback stub for tests where no
// reconnect is being simulated.
func alwaysOrphaned() bool { return true }

// TestOrphanWatch_GraceElapses_ReapInvokedOnceWithOrphanReason is the basic
// end-to-end case (MA-4): a lone root turn with no descendants that never
// finishes on its own must have the caller-supplied reap callback invoked
// EXACTLY ONCE, with reason "orphan_timeout", once the grace period elapses
// — proving the redesigned fire path actually calls through to the
// side-effecting reap closure (which the gateway wires to RequestCancel).
// Also asserts the dedicated turn.orphan_timeout audit event is emitted.
func TestOrphanWatch_GraceElapses_ReapInvokedOnceWithOrphanReason(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	rootTS := &turnState{
		turnID:              "root-basic-fire",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair `[grill C-1]`:
		// ArmOrphanForegroundTurnWatch/fireOrphanForegroundTurnWatch resolve
		// the session's live turn via GetActiveTurnHookForSession, a role-B
		// predicate that now matches on routingSessionID, not
		// transcriptSessionID — without it, arming itself is refused (no
		// active turn hook found for the session).
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
		// providerCancel intentionally nil: this bare turnState never finishes
		// on its own, so it stays "alive" for the whole test.
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	var reapCount atomic.Int64
	var mu sync.Mutex
	var lastReason string
	reap := func(reason string) {
		reapCount.Add(1)
		mu.Lock()
		lastReason = reason
		mu.Unlock()
	}

	al.ArmOrphanForegroundTurnWatch(sessionID, 1, reap, alwaysOrphaned)

	require.Eventually(t, func() bool { return reapCount.Load() == 1 }, 5*time.Second, 20*time.Millisecond,
		"reap must be invoked exactly once once the grace period elapses")

	mu.Lock()
	assert.Equal(t, "orphan_timeout", lastReason)
	mu.Unlock()

	require.Eventually(t, func() bool {
		events := readCancelAuditEvents(t, auditDir)
		for _, ev := range events {
			if ev == audit.EventTurnOrphanTimeout {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "turn.orphan_timeout audit event must be emitted when the watchdog reaps")

	// reap must not fire again even after waiting further — the watch was a
	// single-shot timer, already removed from the map at fire time.
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int64(1), reapCount.Load(), "reap must fire at most once per arm")
}

// TestOrphanWatch_DisarmBeforeGrace_NoOp proves Disarm called before the
// grace timer fires cancels the watch entirely — reap is never invoked, not
// even once.
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

	var reapCalled atomic.Bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled.Store(true) },
		alwaysOrphaned,
	)
	al.DisarmOrphanForegroundTurnWatch(sessionID)

	// Wait past the grace period with margin.
	time.Sleep(1500 * time.Millisecond)

	assert.False(t, reapCalled.Load(), "a disarmed watch must never reap")
	assert.True(t, rootTS.IsAlive())

	_, armed := al.orphanWatches.Load(sessionID)
	assert.False(t, armed, "the disarmed watch must be removed from the map")
}

// TestOrphanWatch_DisarmAfterFire_NoOpOnAlreadyReapedTurn covers MA-3: once
// the grace timer has already fired and reaped, a LATER Disarm call (e.g. a
// reconnect landing after the fact) must be a harmless no-op — no panic, and
// critically no SECOND reap. The watch's map entry is removed the instant
// fireOrphanForegroundTurnWatch runs, so Disarm finds nothing to cancel.
func TestOrphanWatch_DisarmAfterFire_NoOpOnAlreadyReapedTurn(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-disarm-after-fire",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: see the identical note in
		// TestOrphanWatch_GraceElapses_ReapInvokedOnceWithOrphanReason above.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	var reapCount atomic.Int64
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCount.Add(1) },
		alwaysOrphaned,
	)

	require.Eventually(t, func() bool { return reapCount.Load() == 1 }, 3*time.Second, 20*time.Millisecond,
		"precondition: the watch must fire-and-reap once")

	require.NotPanics(t, func() { al.DisarmOrphanForegroundTurnWatch(sessionID) },
		"MA-3: disarming after fire-and-reap must not panic")

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int64(1), reapCount.Load(), "disarm after fire-and-reap must not cause a second reap")
}

// TestOrphanWatch_RootAlreadyClearedFromActiveTurnStates_CriticalDelegateNeverReaped
// is the MA-1 regression test: it mirrors production's clearActiveTurn by
// EXPLICITLY removing the root's own activeTurnStates entry once it
// finishes — loop.go's `defer al.clearActiveTurn(ts)` runs BEFORE
// `defer ts.Finish(false)` even sets isFinished (see turn.go/loop.go's
// documented defer ordering) — so by the time the grace timer fires, the
// session's ONLY resolvable turnState is the still-alive Critical delegate.
//
// The PRIOR version of this test (pre-redesign) only called
// rootTS.Finish(false) and left the (now-finished) root's own map entry in
// place — GetActiveTurnHookForSession's root-preferring match still found
// that stale, finished entry, and the bespoke fire code's OWN
// TurnID-mismatch/IsAlive() check bailed for a reason that had NOTHING to do
// with the actual MA-1 invariant, completely masking the real bug (a
// resolved-but-uncleared root looks identical to a live one from that
// check's perspective). Explicitly deleting the root's map entry here closes
// that gap and proves getActiveRootTurnStateForSession genuinely returns nil
// (never falling back to the child) once the root is truly gone.
func TestOrphanWatch_RootAlreadyClearedFromActiveTurnStates_CriticalDelegateNeverReaped(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-with-descendant",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	childTS := &turnState{
		turnID:              "child-critical-delegate",
		transcriptSessionID: sessionID, // shares the parent's session
		depth:               1,
		parentTurnID:        rootTS.turnID,
		parentTurnState:     rootTS,
		critical:            true, // marks this as a Critical/background delegate
		finishedChan:        make(chan struct{}),
	}

	al.activeTurnStates.Store(sessionID, rootTS)
	al.activeTurnStates.Store(childTS.turnID, childTS)
	defer al.activeTurnStates.Delete(childTS.turnID)

	var reapCalled atomic.Bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled.Store(true) },
		alwaysOrphaned,
	)

	// Mirror production: root finishes AND is cleared from activeTurnStates,
	// while the Critical delegate keeps running.
	rootTS.Finish(false)
	al.activeTurnStates.Delete(sessionID)

	// Condition-1 isolation (white-box): with the root cleared, the root-only
	// resolver MUST return nil rather than fall back to the delegate. Asserted
	// directly so a regression that reintroduces an "any-match" fallback is
	// caught HERE, not silently absorbed by condition-2 (hasLiveCriticalDelegate),
	// which would independently block the reap in this exact scenario.
	assert.Nil(t, al.getActiveRootTurnStateForSession(sessionID),
		"MA-1 condition-1: root-only resolver must not fall back to a non-root delegate")

	time.Sleep(1500 * time.Millisecond) // past the 1s grace, with margin

	assert.False(t, reapCalled.Load(),
		"MA-1 REGRESSION: reap must NEVER be attempted when the only resolvable turn is a Critical delegate")
	assert.True(t, childTS.IsAlive(), "the Critical delegate must never be touched by this mechanism")
}

// TestOrphanWatch_LiveRootWithLiveCriticalDelegate_ReapDeferred covers the
// second half of the core invariant: even when the ROOT turn is ALSO still
// genuinely alive (not yet cleared), a live Critical/background delegate
// sharing the session must still block the reap entirely. Reaping the root
// via RequestCancel here would risk RequestCancel's own session-wide
// PHASE B/C escalation reaching the still-working delegate.
func TestOrphanWatch_LiveRootWithLiveCriticalDelegate_ReapDeferred(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-still-alive",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	childTS := &turnState{
		turnID:              "child-critical-alive",
		transcriptSessionID: sessionID,
		depth:               1,
		parentTurnID:        rootTS.turnID,
		parentTurnState:     rootTS,
		critical:            true,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	al.activeTurnStates.Store(childTS.turnID, childTS)
	defer al.activeTurnStates.Delete(sessionID)
	defer al.activeTurnStates.Delete(childTS.turnID)

	var reapCalled atomic.Bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled.Store(true) },
		alwaysOrphaned,
	)

	time.Sleep(1500 * time.Millisecond)

	assert.False(t, reapCalled.Load(),
		"reap must be deferred entirely while a live Critical delegate survives, even with a live root")
	assert.True(t, rootTS.IsAlive())
	assert.True(t, childTS.IsAlive())
}

// TestOrphanWatch_StillOrphanedFalseAtFire_NoReap covers MA-5: a reconnect
// that races the grace timer (landing after a Disarm call would have caught
// it, but before the fire goroutine actually runs) must still prevent the
// reap via the stillOrphaned() callback's own fresh check at fire time.
func TestOrphanWatch_StillOrphanedFalseAtFire_NoReap(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-reconnect-race",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	var reapCalled atomic.Bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled.Store(true) },
		func() bool { return false }, // simulates "someone reconnected"
	)

	time.Sleep(1500 * time.Millisecond)

	assert.False(t, reapCalled.Load(), "MA-5: stillOrphaned()==false at fire time must prevent reap")
	assert.True(t, rootTS.IsAlive())
}

// TestOrphanWatch_ReArmReplacesPriorTimer proves that arming a session that
// already has a pending watch cancels the OLD watch and installs a new one —
// at most one timer is ever pending per session, and the OLD watch's reap
// callback must never fire.
func TestOrphanWatch_ReArmReplacesPriorTimer(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := &turnState{
		turnID:              "root-rearm",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: see the identical note on
		// TestOrphanWatch_GraceElapses_ReapInvokedOnceWithOrphanReason above.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	var reapCount atomic.Int64
	reap := func(reason string) { reapCount.Add(1) }

	// Arm with a long grace first (would not fire within this test's budget).
	al.ArmOrphanForegroundTurnWatch(sessionID, 100, reap, alwaysOrphaned)

	oldVal, ok := al.orphanWatches.Load(sessionID)
	require.True(t, ok, "first arm must register a watch")
	oldWatch, ok := oldVal.(*orphanWatch)
	require.True(t, ok)

	// Re-arm with a short grace — must cancel and replace the long-grace watch.
	al.ArmOrphanForegroundTurnWatch(sessionID, 1, reap, alwaysOrphaned)

	assert.True(t, oldWatch.isCanceled(), "the prior (long-grace) watch must be canceled by the re-arm")

	newVal, ok := al.orphanWatches.Load(sessionID)
	require.True(t, ok)
	newWatch, ok := newVal.(*orphanWatch)
	require.True(t, ok)
	assert.NotSame(t, oldWatch, newWatch, "re-arm must install a NEW watch instance, not mutate the old one")

	// Proof the short-grace watch is the one actually driving behavior: if
	// the long-grace timer had NOT been canceled/replaced, this would never
	// go true within the test's bounded wait (its own grace is 100s).
	require.Eventually(t, func() bool { return reapCount.Load() == 1 }, 5*time.Second, 20*time.Millisecond,
		"the re-armed (short-grace) watch must be the one that actually fires")
}

// TestOrphanWatch_GraceZeroOrNegative_Disabled proves the watchdog is a no-op
// (disabled) for graceSeconds <= 0 — no timer is ever registered and reap is
// never invoked.
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

	reap := func(reason string) { t.Error("reap must never be called when the watchdog is disabled") }

	for _, grace := range []int{0, -1, -300} {
		al.ArmOrphanForegroundTurnWatch(sessionID, grace, reap, alwaysOrphaned)
		_, armed := al.orphanWatches.Load(sessionID)
		assert.False(t, armed, "graceSeconds=%d must never register a watch", grace)
	}

	time.Sleep(1200 * time.Millisecond)
	assert.True(t, rootTS.IsAlive())
}

// TestOrphanWatch_NilCallbacks_Disabled proves a nil reap or stillOrphaned
// callback also disables arming entirely (defensive: the gateway must always
// supply both, but AgentLoop does not trust that blindly).
func TestOrphanWatch_NilCallbacks_Disabled(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)
	rootTS := &turnState{
		turnID:              "root-nil-callbacks",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	al.ArmOrphanForegroundTurnWatch(sessionID, 1, nil, alwaysOrphaned)
	_, armed := al.orphanWatches.Load(sessionID)
	assert.False(t, armed, "a nil reap callback must never arm a watch")

	al.ArmOrphanForegroundTurnWatch(sessionID, 1, func(string) {}, nil)
	_, armed = al.orphanWatches.Load(sessionID)
	assert.False(t, armed, "a nil stillOrphaned callback must never arm a watch")
}

// TestOrphanWatch_TaskSessionType_NeverArmed is the MA-2 regression test: a
// Task-type session must never arm the orphan-foreground-turn watchdog, even
// with a genuinely active turn present — proving the non-arming is
// attributable to the session-type gate specifically, not to "no active
// turn".
func TestOrphanWatch_TaskSessionType_NeverArmed(t *testing.T) {
	al, _ := newOrphanTestAgentLoop(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeTask, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	rootTS := &turnState{
		turnID:              "root-task-session",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	var reapCalled atomic.Bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled.Store(true) },
		alwaysOrphaned,
	)

	_, armed := al.orphanWatches.Load(sessionID)
	assert.False(t, armed, "MA-2: a Task-type session must never arm the orphan-foreground-turn watchdog")

	time.Sleep(1200 * time.Millisecond)
	assert.False(t, reapCalled.Load())
}

// TestOrphanWatch_DifferentRootAtFireTime_StillReaped documents a deliberate
// behavior simplification versus the original (pre-redesign) bespoke
// mechanism: the watchdog now tracks "is this SESSION's CURRENT root turn
// still genuinely orphaned", not "is the EXACT turn ID armed at Arm time
// still the one running". If the originally-armed root finishes and a
// brand-new root turn takes over the same session before the grace timer
// fires (e.g. an agent-initiated follow-up), and that NEW root is ALSO still
// genuinely unwatched (stillOrphaned() still true) by fire time, it receives
// the exact same protection: an abandoned foreground turn on this session,
// regardless of which specific turn ID currently occupies it. The original
// design's turnID-equality check existed only to compensate for its own
// bespoke escalation timers and is not reintroduced here.
func TestOrphanWatch_DifferentRootAtFireTime_StillReaped(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	// ADR-057 FR-011/FR-015 fixture repair `[grill C-1]`: see the identical
	// note on TestOrphanWatch_GraceElapses_ReapInvokedOnceWithOrphanReason
	// above — applies to both roots here, since GetActiveTurnHookForSession
	// must resolve whichever one is live at fire time.
	rootV1 := &turnState{
		turnID:              "root-v1",
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootV1)

	var reapCount atomic.Int64
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCount.Add(1) },
		alwaysOrphaned,
	)

	rootV1.Finish(false)
	rootV2 := &turnState{
		turnID:              "root-v2",
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootV2) // overwrites rootV1's map entry
	defer al.activeTurnStates.Delete(sessionID)

	require.Eventually(
		t,
		func() bool { return reapCount.Load() == 1 },
		3*time.Second,
		20*time.Millisecond,
		"the session's CURRENT root turn at fire time must be reaped even though it differs from the turn live at arm time",
	)
}

// TestOrphanWatch_ArmDisarmStress_NoPanicConsistentFinalState is the MA-6
// coverage: since pkg/agent cannot run under -race (transitively imports
// !cgo-gated middleware), this is a FUNCTIONAL stress test — hammering
// Arm then immediate Disarm on the same session from many goroutines — that
// asserts no panic/deadlock and a consistent final state. It does not
// replace a real race detector run; MA-6's actual fix (constructing and
// fully initializing the timer before publishing the watch) is a targeted,
// reviewable one-line reorder in ArmOrphanForegroundTurnWatch.
func TestOrphanWatch_ArmDisarmStress_NoPanicConsistentFinalState(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)
	rootTS := &turnState{
		turnID:              "root-stress",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	reap := func(reason string) {}

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			al.ArmOrphanForegroundTurnWatch(sessionID, 1, reap, alwaysOrphaned)
			al.DisarmOrphanForegroundTurnWatch(sessionID)
		}()
	}
	wg.Wait()

	// Give any timer that raced past its own disarm a moment to (harmlessly)
	// fire and settle.
	time.Sleep(1500 * time.Millisecond)

	_, armed := al.orphanWatches.Load(sessionID)
	assert.False(t, armed, "no watch should remain armed after the stress sequence settles")
}

// TestOrphanWatch_Close_StopsTimerAndNeverReaps proves AgentLoop.Close()
// stops every pending orphan-watch timer so none of them can fire (and
// therefore reap) against a torn-down AgentLoop after Close() returns.
func TestOrphanWatch_Close_StopsTimerAndNeverReaps(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "orphan-watch-close-test"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	// NOTE: no t.Cleanup(al.Close) — calling Close() explicitly is the very
	// thing this test exercises.

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	rootTS := &turnState{
		turnID:              "root-close-test",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: see the identical note on
		// TestOrphanWatch_GraceElapses_ReapInvokedOnceWithOrphanReason above.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, rootTS)
	defer al.activeTurnStates.Delete(sessionID)

	var reapCalled atomic.Bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 100, // long grace — Close() must stop it before it ever fires
		func(reason string) { reapCalled.Store(true) },
		alwaysOrphaned,
	)
	_, armed := al.orphanWatches.Load(sessionID)
	require.True(t, armed, "precondition: watch must be armed before Close")

	al.Close()

	_, stillArmed := al.orphanWatches.Load(sessionID)
	assert.False(t, stillArmed, "Close must remove the pending watch from the map")

	// Give any (incorrectly still-running) timer a moment it would need to
	// fire, then confirm reap was never invoked.
	time.Sleep(200 * time.Millisecond)
	assert.False(t, reapCalled.Load(), "Close must stop the grace timer — reap must never fire after Close")
}

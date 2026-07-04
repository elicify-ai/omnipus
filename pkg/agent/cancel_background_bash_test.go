// Package agent — cancel_background_bash_test.go
//
// Integration tests proving RequestCancel -> CancelHooks.KillBackgroundSessions
// -> tools.SessionManager.KillAllForSession fires correctly when wired
// (FR-B10/FR-B11, User Story 5: "Canceling a session also stops any
// background bash work it started"). The bash tool itself does not exist yet
// this wave (a later, parallel wave owns it), so these tests exercise the
// cascade at the level the current wave actually delivers: a ProcessSession
// registered directly against a tools.SessionManager, with
// CancelHooks.KillBackgroundSessions wired to that manager's
// KillAllForSession — the same shape production wiring uses in
// pkg/agent/cancel.go's killBackgroundSessionsForCancelSurface and
// pkg/gateway's websocket.go/schedules.go call sites.
//
// Spec: docs/internal/specs/bash-tool-spec.md, User Story 5 + FR-B10/FR-B11/FR-B14.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// newRunningBackgroundSession is a small helper building a ProcessSession that
// looks like a real detached background bash/exec session: Status "running",
// a PID chosen well outside any real process range in this sandbox (so
// killProcessGroup's underlying syscall.Kill(-pid, ...) reliably returns
// ESRCH, treated as a successful kill — see
// pkg/tools/session_process_unix.go), and the given OwnerSessionID.
func newRunningBackgroundSession(id, ownerSessionID string, pid int) *tools.ProcessSession {
	return &tools.ProcessSession{
		ID:             id,
		OwnerSessionID: ownerSessionID,
		Status:         "running",
		PID:            pid,
		StartTime:      time.Now().Unix(),
	}
}

// registerActiveTurn injects a minimal turnState for sessionID so RequestCancel
// finds an active turn to claim (mirrors the pattern used throughout
// cancel_test.go, e.g. TestRequestCancel_ActiveTurn_FiredTrue).
func registerActiveTurn(t *testing.T, al *AgentLoop, sessionID, turnID string) {
	t.Helper()
	ts := &turnState{
		turnID:              turnID,
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(sessionID) })
}

// TestBash_CancelCascade_KillsOwnedBackgroundSessions covers BDD Scenario
// "Canceling a session kills its background bash sessions" (User Story 5,
// Acceptance Scenario 1): a running background session owned by the canceled
// session must be killed as part of RequestCancel's existing graceful-cascade
// phase, and the kill must be observable via the counter (FR-B14) without
// relying on a subsequent poll.
func TestBash_CancelCascade_KillsOwnedBackgroundSessions(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	registerActiveTurn(t, al, "sess-bg-cascade", "turn-bg-cascade")

	sm := tools.NewSessionManager()
	bgSession := newRunningBackgroundSession("bg-1", "sess-bg-cascade", 999991001)
	sm.Add(bgSession)

	countBefore := sm.KilledBackgroundSessionsCount()

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-bg-cascade"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) {
				sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "an active turn exists, so the cancel must fire")

	got, getErr := sm.Get("bg-1")
	require.NoError(t, getErr)
	assert.Equal(t, "done", got.GetStatus(),
		"the background session owned by the canceled chat session must be killed")

	assert.Equal(t, countBefore+1, sm.KilledBackgroundSessionsCount(),
		"FR-B14: the kill counter must increment so the cascade is observable without a subsequent poll")
}

// TestBash_CancelCascade_NoOpWhenNothingToKill covers BDD Scenario "Canceling
// a session with no background work is a no-op for the new cascade step"
// (User Story 5, Acceptance Scenario 2): existing cancel behavior (Fired,
// audit, transcript) must complete exactly as it does today, with no error or
// delay introduced by the new hook.
func TestBash_CancelCascade_NoOpWhenNothingToKill(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	registerActiveTurn(t, al, "sess-bg-noop", "turn-bg-noop")

	sm := tools.NewSessionManager() // deliberately empty — no background sessions at all

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-bg-noop"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) {
				sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "cancel must fire normally even with no background work")
	assert.Equal(t, int64(0), sm.KilledBackgroundSessionsCount(),
		"no background sessions exist, so the counter must stay at zero")
}

// TestBash_CancelCascade_DoesNotAffectOtherSessions covers BDD Scenario
// "Canceling one session does not affect another session's background work"
// (User Story 5, Acceptance Scenario 3): canceling session A must kill only
// A's background session, leaving unrelated session B's background session
// running untouched.
func TestBash_CancelCascade_DoesNotAffectOtherSessions(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	registerActiveTurn(t, al, "sess-A", "turn-A")
	// Session B has no active turn registered; its background work must
	// simply be left alone by canceling A regardless.

	sm := tools.NewSessionManager()
	bgA := newRunningBackgroundSession("bg-A", "sess-A", 999992001)
	bgB := newRunningBackgroundSession("bg-B", "sess-B", 999992002)
	sm.Add(bgA)
	sm.Add(bgB)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-A"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) {
				sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired)

	gotA, err := sm.Get("bg-A")
	require.NoError(t, err)
	assert.Equal(t, "done", gotA.GetStatus(), "session A's background work must be killed")

	gotB, err := sm.Get("bg-B")
	require.NoError(t, err)
	assert.Equal(t, "running", gotB.GetStatus(), "session B's background work must be left untouched")
}

// TestBash_CancelCascade_HookNotCalledWhenNoActiveTurn verifies that a
// double-cancel-style no-op (no active turn claimed, ClaimCancel never
// succeeds) does not reach PHASE A at all, so KillBackgroundSessions is never
// invoked — matching RequestCancel's existing "Fired: false" short-circuit for
// unmatched sessions (regression guard: the new hook must not change today's
// no-active-turn behavior).
func TestBash_CancelCascade_HookNotCalledWhenNoActiveTurn(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	// Deliberately do not register any turnState for this session.

	called := false
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-never-active"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) {
				called = true
			},
		},
	)
	require.NoError(t, err)
	assert.False(t, outcome.Fired, "no active turn means no claim, so the cancel does not fire")
	assert.False(t, called, "KillBackgroundSessions must not be invoked when no active turn is claimed")
}

// Package agent — cancel_prearm_expiry_hook_test.go
//
// Tests for CancelHooks.OnLatchExpired (cancel.go / cancel_prearm.go): the
// notification path for a pre-registration cancel latch that ages out
// (cancelPreArmTTL) before any turn ever consumes it. Before this hook
// existed, that outcome — a Stop click acknowledged via CancelOutcome.Armed
// but never actually applied to any turn — was reported nowhere: consume's
// own expiry branch only logged at slog.Debug, and armLocked's opportunistic
// sweep (the ONLY discovery point for a latch whose target turn never
// registers at all) discarded it in total silence.
//
// Both discovery paths are covered here:
//   - Path 1 (consume): a LATER, unrelated turn's own registration attempt
//     happens to check a latch that has already aged out.
//   - Path 2 (armLocked): a completely different cancel's own arm call
//     opportunistically sweeps out an expired latch that no turn ever
//     checked at all (the dropped-message case cancel_prearm.go's own doc
//     comment names as the reason the TTL exists in the first place).

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForLatchExpiredCall blocks until ch receives a value or the timeout
// elapses, failing the test on timeout. notifyLatchExpired always runs the
// hook on its own goroutine (never synchronously inside RequestCancel or
// registerActiveTurn), so every assertion in this file needs to wait for it.
func waitForLatchExpiredCall(t *testing.T, ch <-chan struct {
	scope     CancelScope
	canceller CancelCanceller
}, timeout time.Duration) struct {
	scope     CancelScope
	canceller CancelCanceller
} {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(timeout):
		t.Fatal("OnLatchExpired was never called within the timeout")
		panic("unreachable")
	}
}

// TestPreArmedCancel_OnLatchExpired_FiresOnLateConsume covers Path 1: a
// turn registers under the SAME identity as an armed latch, but only after
// the latch has already aged past cancelPreArmTTL — consume's own expiry
// branch discovers it. OnLatchExpired must fire with the ORIGINAL scope and
// canceller this cancel request supplied.
func TestPreArmedCancel_OnLatchExpired_FiresOnLateConsume(t *testing.T) {
	// Not t.Parallel(): mutates the package-level cancelPreArmTTL var.
	origTTL := cancelPreArmTTL
	cancelPreArmTTL = 20 * time.Millisecond
	t.Cleanup(func() { cancelPreArmTTL = origTTL })

	al := newCancelTestAgentLoop(t)

	const sessionID = "sess-prearm-expiry-hook-consume"

	calls := make(chan struct {
		scope     CancelScope
		canceller CancelCanceller
	}, 4)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			OnLatchExpired: func(scope CancelScope, canceller CancelCanceller) {
				calls <- struct {
					scope     CancelScope
					canceller CancelCanceller
				}{scope, canceller}
			},
		},
	)
	require.NoError(t, err)
	require.True(t, outcome.Armed, "precondition: the cancel must have armed a latch")

	// Let the latch age past its (shrunk) TTL before any turn registers.
	time.Sleep(60 * time.Millisecond)

	ts := newPreArmTestTurnState("turn-prearm-expiry-hook-consume", "sess-prearm-expiry-hook-consume-key", sessionID, "", "")
	al.registerActiveTurn(ts)
	defer al.clearActiveTurn(ts)

	assert.False(t, ts.cancelFired.Load(),
		"an expired latch must NOT cancel the turn that registers after it has aged out (unchanged contract)")

	got := waitForLatchExpiredCall(t, calls, 2*time.Second)
	assert.Equal(t, sessionID, got.scope.SessionID, "OnLatchExpired must receive the ORIGINAL cancel's scope")
	assert.Equal(t, "alice", got.canceller.UserID, "OnLatchExpired must receive the ORIGINAL cancel's canceller")
	assert.Equal(t, "web", got.canceller.Channel)
}

// TestPreArmedCancel_OnLatchExpired_FiresOnOpportunisticSweep covers Path 2:
// a latch armed for session A ages out with NO turn ever registering for it
// at all (e.g. a dropped Tier B message) — the only thing that ever
// discovers this is a totally unrelated cancel's own armLocked sweep. Before
// notifyLatchExpired existed this was pure silence: no log, no hook, nothing.
func TestPreArmedCancel_OnLatchExpired_FiresOnOpportunisticSweep(t *testing.T) {
	// Not t.Parallel(): mutates the package-level cancelPreArmTTL var.
	origTTL := cancelPreArmTTL
	cancelPreArmTTL = 20 * time.Millisecond
	t.Cleanup(func() { cancelPreArmTTL = origTTL })

	al := newCancelTestAgentLoop(t)

	const sessionA = "sess-prearm-expiry-hook-sweep-a"
	const sessionB = "sess-prearm-expiry-hook-sweep-b"

	calls := make(chan struct {
		scope     CancelScope
		canceller CancelCanceller
	}, 4)

	// Arm A's latch — nothing will ever register for it.
	outcomeA, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionA},
		CancelCanceller{UserID: "bob", Channel: "telegram"},
		CancelHooks{
			OnLatchExpired: func(scope CancelScope, canceller CancelCanceller) {
				calls <- struct {
					scope     CancelScope
					canceller CancelCanceller
				}{scope, canceller}
			},
		},
	)
	require.NoError(t, err)
	require.True(t, outcomeA.Armed, "precondition: session A's cancel must have armed a latch")

	// Let A's latch age past its (shrunk) TTL.
	time.Sleep(60 * time.Millisecond)

	// A completely unrelated cancel for session B arms its own latch. Its
	// armLocked call opportunistically sweeps the table and finds A's entry
	// already expired — the ONLY discovery point for A, since no turn ever
	// registers under A's identity in this test.
	outcomeB, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionB},
		CancelCanceller{UserID: "carol", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcomeB.Armed, "precondition: session B's cancel must also arm its own latch")

	got := waitForLatchExpiredCall(t, calls, 2*time.Second)
	assert.Equal(t, sessionA, got.scope.SessionID,
		"OnLatchExpired must fire for session A (the expired, never-consumed latch), not session B")
	assert.Equal(t, "bob", got.canceller.UserID)
	assert.Equal(t, "telegram", got.canceller.Channel)
}

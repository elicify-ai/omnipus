package agent

// helper_redirect_turn_id_test.go — SV-21 guard (r7, S-17; r8 two-case rule;
// #1428 step 2 option C, qa-lead). Spec §6.1: "A running native helper H with
// turn T1 (real AgentLoop, scripted provider): RedirectSessionTurn(H, "x",
// user, "web") → after the stop lands and the replacement starts,
// ActiveForegroundTurnInfo(H) returns ok=true with turnID != T1. Two separate
// named cases: (i) running helper (the RedirectSteeredSession path); (ii)
// stopped helper (the direct reviveStoppedSession path). Each case asserts on
// its own; one case passing never stands for the other. Control: the
// ordinary-chat redirect gives the same result (already F-tt). This guards
// the premise of B-26 for helpers; MU-46 must turn it red."
//
// RED/GUARD note: this test is GREEN by design today — the spec names it a
// mutation target (MU-46) guarding existing behaviour; its failability proof
// is CHECK's mutation run, not a RED run. The plan records it as GUARD.
//
// ORACLE: the turn id observed through ActiveForegroundTurnInfo(H) AFTER the
// replacement starts must differ from T1, the id observed while H ran its
// first turn — on BOTH helper paths independently, plus the ordinary-chat
// control. Nothing here asserts implementation internals.

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

const sv21ReplacementInstruction = "SV-21 replacement: answer the redirected instruction instead."

// helperRedirectFixture builds helper H with a LIVE first turn T1 (real
// SteerLauncher dispatch, parked provider), and returns the loop, H's id and
// T1's observed foreground turn id.
func helperRedirectFixture(t *testing.T) (*AgentLoop, string, string) {
	t.Helper()
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	provider, _ := installParkedProvider(t, al)

	steererID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))
	childID, childGen := launchSteeredChild(t, al, steererID, "call-sv21-helper", "SV-21 helper first turn")
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), childID, childGen)
	require.NoError(t, err, "SETUP: Dispatch(H)")
	require.Equal(t, steer.DispatchRunning, dispatched.State, "SETUP: H must dispatch as running")

	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: H's first turn never reached its provider")
	}

	t1 := waitForForegroundTurn(t, al, childID, "SETUP: H's first turn")
	require.NotEmpty(t, t1, "SETUP: H's first turn must have an observable foreground turn id")
	return al, childID, t1
}

// waitForForegroundTurn polls ActiveForegroundTurnInfo until the session
// reports a live foreground turn, returning its id. Fails loudly on timeout —
// a missing observation is a fixture failure, never a pass.
func waitForForegroundTurn(t *testing.T, al *AgentLoop, sessionID, phase string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		turnID, _, _, ok := al.ActiveForegroundTurnInfo(sessionID)
		if ok && turnID != "" {
			return turnID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: ActiveForegroundTurnInfo(%s) never reported a live foreground turn", phase, sessionID)
	return ""
}

// requireReplacedForegroundTurn is SV-21's oracle for one case: after the
// redirect's replacement starts, H reports a live foreground turn whose id is
// NOT the first turn's id.
func requireReplacedForegroundTurn(t *testing.T, al *AgentLoop, sessionID, oldTurnID, phase string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		turnID, _, _, ok := al.ActiveForegroundTurnInfo(sessionID)
		if ok && turnID != "" && turnID != oldTurnID {
			return turnID
		}
		time.Sleep(20 * time.Millisecond)
	}
	lastID, _, _, lastOK := al.ActiveForegroundTurnInfo(sessionID)
	t.Fatalf("SV-21 (%s) violated: after the redirect stopped turn %q and the replacement started, "+
		"ActiveForegroundTurnInfo(%s) never reported a NEW foreground turn id (last observation: ok=%v id=%q)",
		phase, oldTurnID, sessionID, lastOK, lastID)
	return ""
}

// TestHelperRedirect_RunningHelperReplacesForegroundTurnID is case (i): the
// RedirectSteeredSession path — redirect a helper whose turn T1 is LIVE.
func TestHelperRedirect_RunningHelperReplacesForegroundTurnID(t *testing.T) {
	al, childID, t1 := helperRedirectFixture(t)

	require.NoError(t, al.RedirectSessionTurn(context.Background(), childID,
		sv21ReplacementInstruction, "tester", "web"),
		"(i): redirecting a running helper must be accepted")

	newID := requireReplacedForegroundTurn(t, al, childID, t1, "(i) running helper")
	t.Logf("(i): foreground turn replaced: %q -> %q", t1, newID)
}

// TestHelperRedirect_StoppedHelperRevivesWithNewForegroundTurnID is case
// (ii): the direct reviveStoppedSession path — stop the live helper first
// (durable stop lands), then redirect the STOPPED helper.
func TestHelperRedirect_StoppedHelperRevivesWithNewForegroundTurnID(t *testing.T) {
	al, childID, t1 := helperRedirectFixture(t)

	res, err := al.StopSession(context.Background(), StopRequest{
		SessionID: childID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"},
		Channel:   "web",
	})
	require.NoError(t, err, "(ii) SETUP: stopping the live helper")
	require.NoError(t, res.RootErr, "(ii) SETUP: the stop must not error")
	require.True(t, res.Durable, "(ii) SETUP: the stop must land durably")

	deadline := time.Now().Add(30 * time.Second)
	for {
		rec, loadErr := al.GetSessionLifecycleStore().Load(childID)
		require.NoError(t, loadErr, "(ii) SETUP: read H's record")
		// Both the state AND the in-flight stop fence must have landed —
		// reviveStoppedSession refuses while the fence is still set
		// (the same predicate stopRedirectAwaitIdle waits on).
		if rec.Stopped() && !lifecycleInFlightStopFence(rec) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("(ii) SETUP: H's record never landed stopped with its fence cleared (state=%s fence=%v)",
				rec.State, lifecycleInFlightStopFence(rec))
		}
		time.Sleep(20 * time.Millisecond)
	}

	require.NoError(t, al.RedirectSessionTurn(context.Background(), childID,
		sv21ReplacementInstruction, "tester", "web"),
		"(ii): redirecting a stopped helper must be accepted and revive it")

	newID := requireReplacedForegroundTurn(t, al, childID, t1, "(ii) stopped helper")
	t.Logf("(ii): foreground turn replaced: %q -> %q", t1, newID)
}

// TestHelperRedirect_OrdinaryChatControlAlsoReplacesTurnID is SV-21's
// control: the ordinary-chat redirect gives the same result (already F-tt).
func TestHelperRedirect_OrdinaryChatControlAlsoReplacesTurnID(t *testing.T) {
	al, rec, provider, humanDone := newStopRedirectRoot(t)

	t1 := waitForForegroundTurn(t, al, rec.SessionID, "control SETUP: the live ordinary turn")

	require.NoError(t, al.RedirectSessionTurn(context.Background(), rec.SessionID,
		stopRedirectRootInstruction, "root-owner", "webchat"),
		"control: redirecting the ordinary chat must be accepted")

	select {
	case <-humanDone:
	case <-time.After(10 * time.Second):
		t.Fatal("control: the stopped turn never ended")
	}
	r1AwaitProvider(t, provider, 2) // the replacement turn is live in its provider call

	newID := requireReplacedForegroundTurn(t, al, rec.SessionID, t1, "control ordinary chat")
	t.Logf("control: foreground turn replaced: %q -> %q", t1, newID)
}

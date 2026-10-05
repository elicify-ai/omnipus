// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the 2026-10-04 founder contract ("an in-flight fence is not a
// landed stop"), pkg/agent side — the two human-inbound revive surfaces the
// tools-side pack (pkg/tools/delegate_stop_fence_test.go) does not cover:
//
//   - AgentLoop.ReviveStoppedSession (steering.go): loads the record and
//     declines only when `!rec.Terminal() && !rec.Stopped()`. An IN-FLIGHT
//     fence — state still running or queued, Stop stamped for the CURRENT
//     generation — makes LifecycleRecord.Stopped() true, so the decline
//     branch is skipped and the revive proceeds: the new instruction is
//     appended, SteerCanceller.Revive mints generation+1 (the live fence
//     becomes inert history), and dispatchSteeredSession runs a turn the
//     still-dying generation's admission then refuses — the helper stranded
//     with nobody running it.
//   - reviveInactiveInbound (steering.go, via its production entry point
//     enqueueSteeringFromMessage) and runInboundTurnWithRevival
//     (revive_inbound.go, via its production entry point processMessage):
//     the human-inbound admission paths branch on the same
//     `Terminal() || Stopped()` predicate, so a human message into a session
//     carrying ONLY an in-flight fence revives it and starts a second turn
//     beside the dying one.
//
// The contract (founder decision, 2026-10-04): a LANDED stop (state
// LifecycleStopped) or a finished helper (terminal) still resumes when a
// newer message arrives; an in-flight fence is NOT a landed stop — the call
// returns a visible error, appends nothing, clears nothing, queues nothing
// and dispatches nothing.
//
// The landed LifecycleStopped row is already pinned here (do not duplicate):
// TestReviveStoppedSession_LandedAndClearedRecordRevives and
// TestReviveInactiveInbound_LandedAndClearedRecordIsRevived in
// steering_landed_stopped_test.go.
//
// Expected RED at this base, deterministic: every test below fails on the
// revive-succeeded observation (visible-error assertion, generation advanced
// to 2, or a model call from the wrongly started turn) — not on setup.
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty).

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// inflightInstruction is the newer instruction the revive attempts to append
// in TestReviveStoppedSession_InFlightStopFence_VisibleErrorAndRecordUntouched.
// The contract says a fenced session must not receive it.
const inflightInstruction = "one more thing"

// seedInFlightFencedChild persists a steered child RUNNING with a stop fence
// stamped for its CURRENT generation — the exact shape SteerCanceller's
// cascade leaves while a stop is still unwinding: the fence is live
// (Stop.Generation == Generation), the stop has not LANDED (state is not
// LifecycleStopped, no StopNote). The scaffold mirrors the landed-and-cleared
// sibling in steering_landed_stopped_test.go; only the record shape differs.
func seedInFlightFencedChild(t *testing.T, al *AgentLoop, steererID, childID string) {
	t.Helper()
	if err := al.GetSessionStore().SetMeta(childID, session.MetaPatch{ParentSessionID: &steererID}); err != nil {
		t.Fatalf("SetMeta(child).ParentSessionID: %v", err)
	}
	adr093Persist(t, al, &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: steererID, RootSessionID: steererID},
		WorkspaceID:    adr093Workspace,
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-inflight-fence"},
		Stop: &session.Stop{
			At:         time.Now(),
			Generation: 1,
			By:         session.Principal{Kind: session.PrincipalKindHuman, ID: "human:tester"},
		},
		// No StopNote: the stop has not LANDED — D2/CRIT-001 requires a note
		// only on a record landing LifecycleStopped.
	})
}

// assertInFlightFenceIntact reloads sessionID's record and fails the test
// unless the seeded in-flight fence is exactly as the contract leaves it:
// generation still 1 (no revive minted a new one), state still running (not
// queued, not stopped), and the fence still live for the current generation.
func assertInFlightFenceIntact(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	got := adr093Load(t, al, sessionID)
	if got.Generation != 1 {
		t.Fatalf("record %s after the revive attempt = generation %d, want 1 — the 2026-10-04 contract: an in-flight fence is not a landed stop, the revive must not mint a new generation", sessionID, got.Generation)
	}
	if got.State != session.LifecycleRunning {
		t.Fatalf("record %s after the revive attempt = state %q, want %q — the contract: the call must not queue, stop or otherwise move the record; the in-flight stop is left to land on its own", sessionID, got.State, session.LifecycleRunning)
	}
	if got.Stop == nil || got.Stop.Generation != got.Generation {
		t.Fatalf("record %s after the revive attempt carries Stop %+v for generation %d — the contract: the live fence must still be stamped for the current generation, not cleared and not kept as inert older-generation history", sessionID, got.Stop, got.Generation)
	}
}

// assertNoModelCallWithin fails the test unless the counting provider stays
// at zero calls for the whole window — no turn (steered redispatch or revived
// ordinary turn) may start for a session the contract refused to revive.
// 2s is the wait the revived-root tests in adr093_gate_gaps_test.go use for a
// turn on this same path to reach the model, so a wrongly dispatched turn has
// had its chance to show up before the window closes.
func assertNoModelCallWithin(t *testing.T, provider *adr093FailingProvider, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if calls := provider.callCount(); calls != 0 {
			t.Fatalf("the model was entered %d time(s) after the refused revive — the 2026-10-04 contract: no turn may be dispatched or re-run for a session whose stop fence is still in flight", calls)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls := provider.callCount(); calls != 0 {
		t.Fatalf("the model was entered %d time(s) after the refused revive — the 2026-10-04 contract: no turn may be dispatched or re-run for a session whose stop fence is still in flight", calls)
	}
}

// TestReviveStoppedSession_InFlightStopFence_VisibleErrorAndRecordUntouched
// pins the in-flight-fence row for steering.go::ReviveStoppedSession itself.
// Oracle: the founder contract of 2026-10-04 — an in-flight fence (state
// running, Stop.Generation == Generation) is not a landed stop; the call
// returns a visible error and does not append the instruction, does not
// clear the fence, does not move the state, and does not dispatch. Expected
// values come from that contract, not from running the current (buggy) code.
func TestReviveStoppedSession_InFlightStopFence_VisibleErrorAndRecordUntouched(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steererID := newTestSteeringSession(t, al, adr093Workspace)
	// The steerer needs its OWN root lifecycle record — the landed-and-cleared
	// sibling (steering_landed_stopped_test.go) documents why: the dispatch
	// path's classifier requires the steering ancestor to resolve to a genuine
	// root.
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))

	childID := newTestSteeringSession(t, al, adr093Workspace)
	seedInFlightFencedChild(t, al, steererID, childID)

	provider := &adr093FailingProvider{}
	adr093UseProvider(t, al, provider)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}
	revived, err := al.ReviveStoppedSession(context.Background(), childID, by, inflightInstruction)
	if err == nil {
		t.Fatalf("ReviveStoppedSession(%s) on an in-flight stop fence (state running, Stop.Generation == Generation) returned no error (revived=%t) — "+
			"the 2026-10-04 contract: an in-flight fence is not a landed stop; reviving it strands the helper, so the call must refuse with a visible error",
			childID, revived)
	}
	if revived {
		t.Errorf("ReviveStoppedSession(%s) on an in-flight stop fence reported revived=true alongside its error — a refusal is (false, <visible error>), never a success report", childID)
	}

	// The new instruction must not be in the transcript the revived turn
	// would read: appendSteeredInstruction's entry (ID
	// "<sessionID>-instruction-<uuid>", Role user, Content = instruction)
	// must be absent, by ID scheme and by content.
	entries, rerr := al.GetSessionStore().ReadTranscript(childID)
	if rerr != nil {
		t.Fatalf("ReadTranscript(%s): %v", childID, rerr)
	}
	for _, e := range entries {
		if e.Role == "user" && strings.Contains(e.ID, "-instruction-") {
			t.Fatalf("child transcript carries a steering-instruction entry (%s) — the contract: a refused revive must not append the new instruction", e.ID)
		}
		if e.Role == "user" && e.Content == inflightInstruction {
			t.Fatalf("child transcript carries the refused instruction as a user entry (%q) — the contract: a refused revive must not append the new instruction, whatever entry id it would use", inflightInstruction)
		}
	}

	assertInFlightFenceIntact(t, al, childID)
	assertNoModelCallWithin(t, provider, 2*time.Second)
}

// TestInboundHumanMessage_InFlightStopFence_VisibleErrorNoReviveNoSecondTurn
// pins the in-flight-fence row for the human-inbound enqueue admission path,
// steering.go::reviveInactiveInbound, exercised through its real caller
// enqueueSteeringFromMessage (the production entry point — the same seam the
// landed-and-cleared sibling uses). Oracle: the founder contract of
// 2026-10-04 — a human message into a session carrying ONLY an in-flight
// fence gets a visible error, revives nothing, and starts no second turn
// beside the dying one.
func TestInboundHumanMessage_InFlightStopFence_VisibleErrorNoReviveNoSecondTurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	// adr093StoppedRoot seeds exactly the contract's shape: state running,
	// Stop stamped for the current generation, no landed stop.
	adr093StoppedRoot(t, al, parentID)

	provider := &adr093FailingProvider{}
	adr093UseProvider(t, al, provider)

	if err := al.enqueueSteeringFromMessage(adr093HumanMessage("Right, carry on with the plan.", parentID)); err == nil {
		t.Fatalf("enqueueSteeringFromMessage into a session with only an in-flight stop fence returned no error — " +
			"the 2026-10-04 contract: an in-flight fence is not a landed stop; the human caller gets a visible error instead of a revive that strands the session beside its dying turn")
	}

	assertInFlightFenceIntact(t, al, parentID)
	assertNoModelCallWithin(t, provider, 2*time.Second)
}

// TestProcessMessage_InFlightStopFence_VisibleErrorNoReviveNoSecondTurn pins
// the same contract row for the other human-inbound admission surface,
// revive_inbound.go::runInboundTurnWithRevival, exercised through its real
// caller processMessage (the ordinary inbound-turn admission — loop.go hands
// every human message turn through it). Oracle: the founder contract of
// 2026-10-04, as for the enqueue path — a visible error, no revive, no second
// turn. The message shape mirrors u2_root_goal_stop_guard_test.go's: explicit
// agent_id metadata so resolveMessageRoute has a target without channel
// binding or default-agent config this minimal harness does not set up.
func TestProcessMessage_InFlightStopFence_VisibleErrorNoReviveNoSecondTurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093StoppedRoot(t, al, parentID)

	provider := &adr093FailingProvider{}
	adr093UseProvider(t, al, provider)

	_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:       "webchat",
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
		ChatID:        parentID,
		Content:       "Right, carry on with the plan.",
		SessionID:     parentID,
		GatewayUserID: "fence-contract-user",
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": testDefaultAgentID},
	})
	if err == nil {
		t.Fatalf("processMessage into a session with only an in-flight stop fence returned no error — " +
			"the 2026-10-04 contract: an in-flight fence is not a landed stop; the turn admission must refuse with a visible error instead of reviving the record and running a second turn beside the dying one")
	}

	assertInFlightFenceIntact(t, al, parentID)
	assertNoModelCallWithin(t, provider, 2*time.Second)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Issue #947 defect 2: a delegate child transitioned to a terminal state must
// leave its chat-transcript meta.json terminal, not active. The delegate tool
// passed a nil UnifiedStore to session.TransitionSession (the single dual-store
// mediator), so the mediator's step-2 mirror (lifecycleToUnifiedStatus) never
// ran for delegate children: the durable lifecycle record said cancelled while
// sessions/<id>/meta.json stayed status=active — the record the UI and
// follow_up/Play trust. A later follow_up then hit the "terminal record is
// immutable" warn (symptom 1) against a session the user could still see as
// Active in GET /api/v1/sessions.
//
// RED under the pre-fix code (transitionLifecycle passes nil): the lifecycle
// record lands cancelled but the mirror is skipped, so meta.json status stays
// active.
//
// Sub-agent control plane ADR D4/MAJ-009 note: LifecycleStopped (what
// droppedQueuedResult always lands) no longer mirrors to a distinct coarse
// status at all — stopped stays active by design. This specific fixture can
// therefore no longer tell "mirror skipped because the store was nil" apart
// from "mirror correctly ran and wrote nothing" by status value alone; both
// now leave meta.json at active. The assertion below still pins the correct
// CURRENT behavior (status stays active), but the original defect-947
// regression coverage this test existed for is weakened for this specific
// fixture.
//
// qa-lead follow-up (issue #1161): traced lifecycleToUnifiedStatus
// (pkg/session/lifecycle_bridge.go) — for LifecycleStopped it returns
// (_, ok=false) and short-circuits BEFORE any SetMeta call, so there is
// genuinely zero observable side effect from step 2 alone for this target
// state; retargeting this specific fixture at LifecycleFailed/Completed (as
// originally proposed here) would test a different production call site
// (droppedQueuedResult always lands Stopped — see its own doc comment), not
// strengthen THIS test, so that proposal is withdrawn. What DOES close the
// gap for THIS test: pairing the "Mirror half" assertion below with the
// "Lifecycle half" rec.State assertion immediately above it. Both are driven
// by the SAME transitionLifecycle -> session.TransitionSession call
// (delegate_run.go::droppedQueuedResult), so deleting that call, or the
// delegate's whole transitionLifecycle wiring, fails THIS test via rec.State staying
// LifecycleQueued — not via the Mirror-half assertion, which (same as
// TestRequestCancel_TransitionsLifecycleRecordToCancelled in
// pkg/agent/cancel_lifecycle_bridge_test.go) cannot see that deletion in
// isolation. The two together are the proof the mediator ran; the
// Mirror-half line still independently catches lifecycleToUnifiedStatus
// mis-mapping Stopped to the wrong status.
func TestDelegateChildTransition_MirrorsTerminalStatusToUnifiedMeta(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	us, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("unified store: %v", err)
	}
	tool.SetUnifiedStore(us)

	// Mint the child's chat-transcript meta the way the launcher mints it for
	// every delegate child (launchOrdinaryRoot: NewSession / launchSteered:
	// CreateSessionWithID) — status starts active.
	meta, err := us.NewSession(session.SessionTypeDelegate, "", "worker-1")
	if err != nil {
		t.Fatalf("mint child meta: %v", err)
	}
	childID := meta.ID

	// Seed the stranded queued child the cancel backstop drops
	// (delegate_run.go::droppedQueuedResult — a production
	// transitionLifecycle caller). In production this point is reached only
	// after cancelHard/cancelSoft (al.cancelDelegatedSubtree) already ran
	// steer_cancel.go::stampStop, which stamps StopNote onto the record in
	// the SAME mutation that sets the Stop fence — while State is still
	// queued/running, not yet stopped (D2/CRIT-001). Seed that StopNote here
	// so this direct call to droppedQueuedResult (which itself passes
	// note=nil and relies on an already-landed note, delegate_run.go:673-677)
	// matches that real precondition instead of skipping it.
	if perr := lc.Persist(&session.LifecycleRecord{
		SessionID: childID, Generation: 1, State: session.LifecycleQueued,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		AgentID:        "worker-1",
		CreatedAt:      time.Now().UTC().Add(-time.Hour),
		StopNote:       &session.StopNote{At: time.Now().UTC(), By: "human:qa-lead", Seq: 1, Cause: session.StopCauseStop},
	}); perr != nil {
		t.Fatalf("seed lifecycle record: %v", perr)
	}

	// Act: the production queued-drop transition path.
	if res := tool.droppedQueuedResult(childID, ""); res == nil {
		t.Fatalf("droppedQueuedResult returned nil for a queued record — the drop path did not fire")
	}

	// Lifecycle half: the durable record must be terminal cancelled.
	rec, err := lc.Load(childID)
	if err != nil {
		t.Fatalf("load lifecycle record: %v", err)
	}
	if rec.State != session.LifecycleStopped {
		t.Errorf("lifecycle state = %q, want cancelled", rec.State)
	}

	// Mirror half: sessions/<id>/meta.json must stay active (ADR D4/MAJ-009 —
	// stopped is coarse-active; see the note above this test).
	got, err := us.GetMeta(childID)
	if err != nil {
		t.Fatalf("get meta: %v", err)
	}
	if got.Status != session.StatusActive {
		t.Errorf("meta.json status = %q, want %q — a stopped delegate child stays coarse-active (ADR D4)",
			got.Status, session.StatusActive)
	}
}

// The unwired tolerance: a tool with no UnifiedStore (pre-#947 wiring, or a
// harness that never constructed the shared store) must still land the
// lifecycle transition and must not panic — TransitionSession's nil tolerance
// ("no chat-transcript meta") is preserved; it just must not be the production
// shape anymore.
func TestDelegateChildTransition_UnwiredUnifiedStore_StillTransitions(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	// Same D2/CRIT-001 precondition as the sibling test above: production
	// only reaches droppedQueuedResult after a prior stampStop already
	// landed StopNote on this generation.
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-947-unwired", Generation: 1, State: session.LifecycleQueued,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		AgentID:        "worker-1",
		CreatedAt:      time.Now().UTC().Add(-time.Hour),
		StopNote:       &session.StopNote{At: time.Now().UTC(), By: "human:qa-lead", Seq: 1, Cause: session.StopCauseStop},
	}); err != nil {
		t.Fatalf("seed lifecycle record: %v", err)
	}

	if res := tool.droppedQueuedResult("child-947-unwired", ""); res == nil {
		t.Fatalf("droppedQueuedResult returned nil for a queued record — the drop path did not fire")
	}

	rec, err := lc.Load("child-947-unwired")
	if err != nil {
		t.Fatalf("load lifecycle record: %v", err)
	}
	if rec.State != session.LifecycleStopped {
		t.Errorf("lifecycle state = %q, want cancelled (the unwired store must not block the transition)", rec.State)
	}
}

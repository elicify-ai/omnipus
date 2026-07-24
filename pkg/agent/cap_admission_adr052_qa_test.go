// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cap_admission_adr052_qa_test.go is ADR-052 Wave 2's qa-go coverage for
// SC-011 ("the 17th concurrent plan/goal/loop is refused or queued... including
// a RESTARTED plan"). The two halves of this guarantee already have solid
// dedicated coverage in plan_engine_test.go:
//   - TestGlobalCap_AdmitBoundary_15_16_17 proves PlanEngine.Admit itself
//     denies the 17th admission attempt once 16 plans are running.
//   - TestPlanEngine_ApprovedStaysCapWaitingWhenCapFull proves a 17th
//     `approved` plan stays `approved` (queued, not promoted) across one
//     Tick while the cap is full.
//
// What neither existing test closes is the FULL SC-011 narrative in one
// place: a queued plan is actually promoted once a slot frees — "when a cap
// slot frees the engine promotes it to running" (spec BDD "execute_plan
// queues behind a full cap", DS-2). This file adds exactly that composition,
// plus a direct Admit()-level release/re-admit check as a tight complementary
// unit assertion.
//
// Traces to: autonomous-agent-plan-execution-spec.md SC-011 (line 710), BDD
// "execute_plan queues behind a full cap" (line 298-305), Test 5
// (TestExecutePlan_CapQueue, line 527 — the tool-level counterpart, not yet
// testable against MAIN: the execute_plan agent tool/REST wiring is owned by
// parallel wave2 worktrees not present in this checkout).

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestCapAdmission_SixteenRunningSeventeenthQueuedThenPromotedWhenSlotFrees
// is the full SC-011 composition: 16 plans running (cap full) + a 17th
// `approved` plan stays queued across a Tick (the existing
// TestPlanEngine_ApprovedStaysCapWaitingWhenCapFull assertion, reproduced
// here as the setup precondition) — THEN one running filler plan completes
// (freeing a cap slot) and the NEXT Tick promotes the previously-queued 17th
// plan to `running`, closing the loop the existing tests each only cover
// half of.
func TestCapAdmission_SixteenRunningSeventeenthQueuedThenPromotedWhenSlotFrees(t *testing.T) {
	h := newTestPlanEngine(t)

	fillerIDs := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("running-%02d", i)
		fillerIDs = append(fillerIDs, id)
		mustCreateRunningPlan(t, h.plans, id, "owner")
		// One still-in-flight member per filler plan so Tick's own
		// processPlan pass doesn't see a vacuously-complete (zero-task) plan
		// and race the cap check by finishing a filler before the "waiting"
		// plan's admission is checked (same precaution
		// TestPlanEngine_ApprovedStaysCapWaitingWhenCapFull takes).
		mustCreateTask(t, h.tasks, &task.Task{
			Title: "filler", WorkspaceID: "ws", PlanID: id, Status: task.StatusInProgress,
		})
	}
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "waiting-17th", Title: "Waiting", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateApproved,
	})

	// --- Precondition: cap full, the 17th stays queued (approved) ---
	h.pe.Tick(context.Background())
	gotWaiting, err := h.plans.Get("waiting-17th")
	if err != nil {
		t.Fatal(err)
	}
	if gotWaiting.State != plan.StateApproved {
		t.Fatalf("precondition failed: state = %q, want approved (cap-waiting) with 16 plans running",
			gotWaiting.State)
	}
	ok, active, capOut := h.pe.Admit("plan")
	if ok || active != 16 || capOut != 16 {
		t.Fatalf("precondition failed: Admit at 16 running = (ok=%v active=%d cap=%d), want (false, 16, 16)",
			ok, active, capOut)
	}

	// --- A slot frees: one filler plan completes (running -> done) ---
	doneState := plan.StateDone
	if _, updateErr := h.plans.Update(fillerIDs[0], plan.Patch{State: &doneState}); updateErr != nil {
		t.Fatalf("free a cap slot (filler plan -> done): %v", updateErr)
	}

	// --- The NEXT Tick must now promote the previously-queued 17th plan ---
	h.pe.Tick(context.Background())
	gotWaitingAfter, err := h.plans.Get("waiting-17th")
	if err != nil {
		t.Fatal(err)
	}
	if gotWaitingAfter.State != plan.StateRunning {
		t.Fatalf("SC-011 VIOLATION: after a cap slot freed, state = %q, want running — "+
			"the engine must promote a queued plan once admission succeeds", gotWaitingAfter.State)
	}
	if gotWaitingAfter.LastActivityAt == "" {
		t.Error("expected LastActivityAt to be stamped when the queued plan finally starts")
	}
}

// TestCapAdmission_DirectAdmitDeniesAtSixteenAllowsAfterRelease is a tight,
// non-Tick-dependent complement: PlanEngine.Admit itself must flip from
// denying to admitting the instant a slot is released — proven directly
// against the SAME store-backed active-count computation
// TestGlobalCap_AdmitBoundary_15_16_17 exercises, one level lower than the
// Tick-driven test above (isolates the admission boundary from the dispatch
// loop's own scheduling).
func TestCapAdmission_DirectAdmitDeniesAtSixteenAllowsAfterRelease(t *testing.T) {
	h := newTestPlanEngine(t)

	var ids []string
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("p-%02d", i)
		ids = append(ids, id)
		mustCreateRunningPlan(t, h.plans, id, "owner")
	}

	// The 17th admission attempt is refused while all 16 are still running.
	ok, active, capOut := h.pe.Admit("plan")
	if ok || active != 16 || capOut != 16 {
		t.Fatalf("Admit at 16/16 = (ok=%v active=%d cap=%d), want (false, 16, 16) — the 17th must be refused",
			ok, active, capOut)
	}

	// Release exactly one slot (running -> failed, a legal terminal
	// transition) and confirm admission immediately succeeds again — no
	// stale/cached count, no off-by-one.
	failedState := plan.StateFailed
	if _, err := h.plans.Update(ids[0], plan.Patch{
		State: &failedState, FailedReason: ptrFailedReason(plan.FailedReasonIdleExpired),
	}); err != nil {
		t.Fatalf("release a slot: %v", err)
	}

	ok, active, capOut = h.pe.Admit("plan")
	if !ok || active != 15 || capOut != 16 {
		t.Fatalf("Admit after releasing one slot = (ok=%v active=%d cap=%d), want (true, 15, 16)",
			ok, active, capOut)
	}
}

func ptrFailedReason(r plan.FailedReason) *plan.FailedReason { return &r }

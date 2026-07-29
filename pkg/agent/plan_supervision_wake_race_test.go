// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_supervision_wake_race_test.go is the regression suite for a confirmed
// production race, observed live with real LLM turns: a plan parked at
// awaiting_supervision could get MULTIPLE OVERLAPPING PlanSupervisor wakes for
// the SAME park, dispatched for DIFFERENT reasons (a stall diagnosis and a
// DoD-unmet verdict), racing plan_correct against the same plan. See
// wakeSupervisor's doc comment (plan_engine.go) for the full mechanism: a
// plan-level judge round completing UNMET transitions the plan THROUGH
// plan.PhaseJudging on its way back to a supervision-eligible phase, which
// made the OLD newPark computation read as "a brand-new park" even while a
// PRIOR supervision turn (e.g. a stall wake) was still being reasoned about.
//
// Every test here uses newPlanWakeHarness — a REAL AgentLoop with a
// recordingTurnProvider installed on the PlanSupervisor agent — so "a second
// supervision turn was dispatched" means AN LLM CALL WAS ACTUALLY MADE, not
// that some bookkeeping field changed. That is the same evidentiary bar
// plan_wake_delivery_test.go's own package doc insists on.
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// unmetJudgeResultFn is a fakePlanJudge.resultFn that always returns an UNMET
// verdict — used to force a plan-level judge round into the DoD-unmet
// supervision wake path (applyJudgeRoundOutcomeLocked -> wakeSupervisor).
func unmetJudgeResultFn(in JudgeCriteriaInput) JudgeCriteriaResult {
	return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
		Met:          false,
		PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "not yet"}},
	}}
}

// stalledPlanFixture is setupStalledPlanWithDoD's return value: the external
// (non-member) blocker and the plan member stuck behind it.
type stalledPlanFixture struct {
	outside *task.Task
	stuck   *task.Task
}

// setupStalledPlanWithDoD creates a running plan with DoD criteria and one
// member stuck behind an external (non-member) blocker, so the FIRST
// processPlan pass surfaces a STALL (not all-terminal) rather than a
// DoD-unmet verdict. Mirrors TestSupervisionDeadline_ArmsAndBoundsAStalledPlan's
// fixture exactly: the blocker is deliberately OUTSIDE the plan (no PlanID),
// so the plan engine's own per-tick promoteReadyMembers sweep — which only
// ever advances a MEMBER's own done-ness — can never clear it organically;
// only an explicit resolution (as production's task_executor.onTaskComplete
// would do reactively) unsticks it.
func setupStalledPlanWithDoD(t *testing.T, h *planWakeHarness, planID string) stalledPlanFixture {
	t.Helper()
	mustCreatePlan(t, h.plans, runningPlanWithDoD(planID, "telegram", "chat-1"))
	outside := mustCreateTask(t, h.tasks, &task.Task{
		Title: "outside blocker", WorkspaceID: "ws", Status: task.StatusInbox,
	})
	stuck := mustCreateTask(t, h.tasks, &task.Task{
		Title: "stuck member", WorkspaceID: "ws", PlanID: planID,
		Status: task.StatusBlocked, BlockedBy: []string{outside.ID},
	})
	return stalledPlanFixture{outside: outside, stuck: stuck}
}

// resolveStuckMemberForUnmetRound resolves the stuck member the ONLY legal
// way pkg/task's lifecycle matrix allows (`blocked` may only be left via the
// internal recompute hatch — a direct blocked->done Update is rejected with
// ErrIllegalTransition): complete the external blocker, advance the
// dependent via the SAME store API production's task_executor.onTaskComplete
// calls reactively (AdvanceBlockedDependents, blocked->next), then complete
// the now-unblocked member (next->done, a legal terminal transition) —
// mirroring what a real worker turn finishing would do. Also arms the judge
// to return UNMET, so the NEXT processPlan pass (now all-terminal) takes the
// DoD-unmet path rather than re-surfacing the stall.
func resolveStuckMemberForUnmetRound(t *testing.T, h *planWakeHarness, fx stalledPlanFixture) {
	t.Helper()
	done := task.StatusDone
	if _, err := h.tasks.Update(fx.outside.ID, task.Patch{Status: &done}); err != nil {
		t.Fatalf("complete the external blocker: %v", err)
	}
	if _, err := h.tasks.AdvanceBlockedDependents(fx.outside.ID); err != nil {
		t.Fatalf("advance blocked dependents: %v", err)
	}
	if _, err := h.tasks.Update(fx.stuck.ID, task.Patch{Status: &done}); err != nil {
		t.Fatalf("complete the now-unblocked member: %v", err)
	}
	h.pe.judge.(*fakePlanJudge).resultFn = unmetJudgeResultFn
}

// --- #1: the race, reproduced ----------------------------------------------

// TestSupervisionWake_ConcurrentReasonsAreSuppressedNotRaced is the
// reproduction + regression test for the confirmed production race.
//
// It drives EXACTLY the sequence observed live:
//  1. The plan stalls (a member is stuck) -> the stall wake dispatches
//     supervision turn A. Turn A is gated so it is GENUINELY still in flight
//     (an LLM call in progress) when step 2 happens — mirroring "the first
//     was reasoning about a stall" from the observed t2/t3 runs.
//  2. While A is still running, the stuck member resolves (mirroring a
//     reactive task_status_changed handler landing mid-stall-turn) and the
//     plan becomes all-terminal with an UNMET judge verdict — a DIFFERENT
//     wake reason (DoD-unmet), exactly as observed.
//
// Before the fix, applyJudgeRoundOutcomeLocked's newPark computation reads
// the plan's freshly-reloaded phase as plan.PhaseJudging (set by
// beginPlanJudgeRound moments earlier) — NOT supervision-eligible — so it
// treats this as a brand-new park and wakeSupervisor mints and dispatches a
// SECOND, independent PlanSupervisor session while A is still alive. That is
// the exact live defect: two adjudicators racing plan_correct against the
// same plan, the second one always losing with "the plan is already in a
// failed state, not running" once the first's correction lands.
//
// The fix must suppress the second dispatch entirely: the plan's session_id
// stays A's, and no second LLM call is made while A is in flight.
func TestSupervisionWake_ConcurrentReasonsAreSuppressedNotRaced(t *testing.T) {
	h := newPlanWakeHarness(t)
	h.supervisor.gate = make(chan struct{})
	defer close(h.supervisor.gate) // always unblock, even on early t.Fatal

	const planID = "p1"
	fx := setupStalledPlanWithDoD(t, h, planID)

	// Tick 1: the plan is not all-terminal -> stall wake -> supervision turn A,
	// which blocks INSIDE the provider call (a genuine in-flight LLM turn).
	h.pe.processPlan(context.Background(), planID)

	select {
	case <-h.supervisor.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the stall supervision turn never started; there is nothing for the race to overlap with")
	}

	parkedAfterStall, err := h.plans.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if parkedAfterStall.PlanPhase != plan.PhaseStalled {
		t.Fatalf("plan_phase = %q, want stalled after tick 1", parkedAfterStall.PlanPhase)
	}
	sessionA := parkedAfterStall.Supervision.SessionID
	if sessionA == "" {
		t.Fatal("the stall wake did not record a supervision session id")
	}
	if got := h.supervisor.callCount(); got != 1 {
		t.Fatalf("precondition: exactly 1 supervision turn must be in flight after the stall wake, got %d", got)
	}

	// While turn A is STILL BLOCKED (genuinely in flight), the stuck member
	// resolves and the judge is armed to return UNMET — the DIFFERENT wake
	// reason from the observed race.
	resolveStuckMemberForUnmetRound(t, h, fx)

	// Tick 2 (the reactive path, modeled directly): all-terminal now, so this
	// runs a full plan-level judge round synchronously in its own goroutine
	// and, on UNMET, calls wakeSupervisor from inside that goroutine before it
	// returns — so judgeWG.Wait() below observes the COMPLETE synchronous
	// portion of that call, including whichever outcome the CAS gate produced.
	h.pe.processPlan(context.Background(), planID)
	h.pe.judgeWG.Wait()

	// Give a second dispatch a fair, bounded window to actually reach the
	// provider before asserting it did not happen — mirrors the existing
	// "prove nothing new fired" idiom used elsewhere in this package
	// (plan_wake_delivery_test.go's TestOwnerWake_ReachesATurnAndTheOriginChat).
	time.Sleep(300 * time.Millisecond)

	// --- THE ASSERTIONS -----------------------------------------------------

	// (1) The session_id must be UNCHANGED. This is deterministic (the mint,
	// if it happens at all, happens synchronously inside the same locked
	// section judgeWG.Wait() already waited out) — a changed session id means
	// a second, independent adjudication session was minted for the SAME park
	// while the first was still live.
	parkedAfterUnmet, err := h.plans.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if parkedAfterUnmet.Supervision.SessionID != sessionA {
		t.Fatalf("supervision.session_id changed from %q to %q while turn A was still in flight — "+
			"a second, independent PlanSupervisor session was minted for the SAME park "+
			"(the exact production race: two adjudicators racing plan_correct on one plan)",
			sessionA, parkedAfterUnmet.Supervision.SessionID)
	}

	// (2) At most ONE supervision LLM call may be in flight. This is the
	// direct, "an LLM call was actually made" evidence the observed bug
	// reported (real sessions, real turns, real cost).
	if got := h.supervisor.callCount(); got != 1 {
		t.Fatalf("supervision turns in flight = %d, want 1 — a second wake (DoD-unmet) was dispatched "+
			"while the stall turn (session %q) was still reasoning about plan %q", got, sessionA, planID)
	}
}

// --- #2: no permanent lockout -----------------------------------------------

// TestSupervisionWake_NewConditionWakesAgainAfterFirstTurnCompletes is the
// other half of "done": the mutual-exclusion fix above must not become a
// permanent lockout. Once the FIRST supervision turn genuinely completes
// (releasing its claim), a genuinely NEW condition (the same stall ->
// DoD-unmet transition as test #1, but this time the first turn is allowed to
// finish before the second condition arrives) must still be able to wake the
// supervisor with a fresh turn.
func TestSupervisionWake_NewConditionWakesAgainAfterFirstTurnCompletes(t *testing.T) {
	h := newPlanWakeHarness(t)
	// No gate this time: turn A is allowed to run to completion.

	const planID = "p1"
	fx := setupStalledPlanWithDoD(t, h, planID)

	h.pe.processPlan(context.Background(), planID)
	waitForTurn(t, h.supervisor, 1, "stall wake")
	h.pe.wakeWG.Wait() // turn A fully SETTLED — its CAS claim is released

	parkedAfterStall, err := h.plans.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	sessionA := parkedAfterStall.Supervision.SessionID
	if sessionA == "" {
		t.Fatal("the stall wake did not record a supervision session id")
	}

	// A genuinely new condition, arriving only AFTER turn A has settled.
	resolveStuckMemberForUnmetRound(t, h, fx)
	h.pe.processPlan(context.Background(), planID)
	h.pe.judgeWG.Wait()

	// THE ASSERTION: the supervisor IS woken again — suppression must not
	// have become a lockout that survives the holder's own completion.
	waitForTurn(t, h.supervisor, 2, "DoD-unmet wake after the stall turn concluded")

	got, err := h.plans.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervision == nil || got.Supervision.SessionID == "" {
		t.Fatal("no supervision session recorded for the second, genuinely-new wake")
	}
}

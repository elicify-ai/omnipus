// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_restart_continuity_adr052_qa_test.go is ADR-052 Wave 2's qa-go
// coverage for spec Test 11 ("TestPlanRestart_Continuation") / DS-5: an
// end-to-end ORCHESTRATION test proving the plan-level restart guard
// (pkg/plan.Store.Update's reason-aware failed[stopped_by_user]->approved
// transition, already unit-tested in pkg/plan/store_test.go) and the
// task-level reset primitive (pkg/task.Store.RestartReset, already
// unit-tested in pkg/task/coverage_test.go) COMPOSE correctly when driven
// together over a plan with a REALISTIC mix of member outcomes — done (with
// evidence), user-cancelled, genuinely-failed, and cancelled-but-blocked-on-
// an-unmet-dependency.
//
// Wave 2's `POST /plans/{id}/restart` HTTP handler (spec FR-026/B2) has since
// SHIPPED: handlePlanRestart (pkg/gateway/rest_plans.go) and its standalone-
// task counterpart handleTaskRestart (pkg/gateway/rest_tasks.go) are both
// live, with their own full HTTP-level coverage in
// pkg/gateway/rest_plan_task_restart_test.go (TestPlanRestart_HappyPath_
// ResetsNonDoneMembersAndReenters and siblings). This test predates that
// handler landing and was written to drive the exact store-level sequence
// the handler would go on to perform (plan.Store.Update restart transition +
// per-member task.Store.RestartReset, skipping `done` members) directly —
// proving the primitives the handler calls compose correctly BELOW the REST
// layer, a property the gateway-level tests don't independently re-verify.
// It remains valuable coverage alongside the shipped handler's own tests,
// not a stand-in for them.
//
// Traces to: autonomous-agent-plan-execution-spec.md Test 11 (line 533),
// DS-5 (line 600-606), FR-016/017 (line 672-673), SC-007 (line 706).

package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestRestartContinuity_DoneMembersPreservedNonDoneReset drives the full
// DS-5 mix in one plan: 2 done members (each with its OWN evidence record,
// proving preservation is per-member, not a blanket copy), 1 user-cancelled
// member with no blockers, 1 user-cancelled member blocked behind a
// still-not-done dependency, and 1 genuinely-failed (non-cancelled) member —
// DS-5 row 2's "any-reason task un-freeze". After restart:
//   - both done members are BYTE-FOR-BYTE unchanged (status, evidence)
//   - the two non-blocked-by-anything-outstanding cancelled/failed members
//     land on `next` with attempt_count 0 and CancelReason cleared
//   - the dependency-blocked cancelled member lands on `blocked` (not `next`)
//     with attempt_count 0
//   - the plan itself: state=approved (NOT running — FR-016), FailedReason
//     cleared, JudgeRounds reset to 0
func TestRestartContinuity_DoneMembersPreservedNonDoneReset(t *testing.T) {
	dir := t.TempDir()
	planStore := plan.New(filepath.Join(dir, "plans"))
	taskStore := task.New(filepath.Join(dir, "tasks"))
	evidenceStore := task.NewEvidenceStore(dir, nil)

	p := &plan.Plan{
		ID: "p-restart-continuity", Title: "p-restart-continuity", WorkspaceID: "ws",
		OwnerAgentID: "owner", State: plan.StateFailed,
		FailedReason: plan.FailedReasonStoppedByUser,
		JudgeRounds:  3,
		HandoverText: "stopped mid-run by the operator",
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// D1/D2: two DONE members, each with its OWN distinct evidence record —
	// proves preservation is per-member and content-specific, not a
	// coincidental pass.
	d1 := &task.Task{
		ID: "d1", Title: "member-done-1", WorkspaceID: "ws", PlanID: p.ID, Status: task.StatusDone,
	}
	d2 := &task.Task{
		ID: "d2", Title: "member-done-2", WorkspaceID: "ws", PlanID: p.ID, Status: task.StatusDone,
	}
	if err := taskStore.Create(d1); err != nil {
		t.Fatalf("create d1: %v", err)
	}
	if err := taskStore.Create(d2); err != nil {
		t.Fatalf("create d2: %v", err)
	}
	ev1, err := evidenceStore.Record(d1.ID, "c1", 1, "echo d1-check", "d1 output: distinct content A", 0, false, false)
	if err != nil {
		t.Fatalf("record evidence d1: %v", err)
	}
	ev2, err := evidenceStore.Record(d2.ID, "c1", 1, "echo d2-check", "d2 output: distinct content B", 0, false, false)
	if err != nil {
		t.Fatalf("record evidence d2: %v", err)
	}

	// M1: user-cancelled, no blockers — must land on `next`.
	m1 := &task.Task{
		ID: "m1", Title: "member-cancelled-unblocked", WorkspaceID: "ws", PlanID: p.ID,
		Status: task.StatusFailed, CancelReason: task.CancelReasonStoppedByUser, AttemptCount: 2,
		Result: "user-cancel marker: stopped_by_user",
	}
	// M3: an independent, still-`next` (not done) dependency M2 will block on.
	m3 := &task.Task{
		ID: "m3", Title: "member-still-next-dependency", WorkspaceID: "ws", PlanID: p.ID, Status: task.StatusNext,
	}
	// M2: user-cancelled AND blocked on M3 (not yet done) — must land on
	// `blocked`, not `next` (DS-5's "reset to next/blocked").
	m2 := &task.Task{
		ID: "m2", Title: "member-cancelled-blocked", WorkspaceID: "ws", PlanID: p.ID,
		Status: task.StatusFailed, CancelReason: task.CancelReasonStoppedByUser, AttemptCount: 1,
		BlockedBy: []string{"m3"},
	}
	// M4: genuinely failed (attempt-limit exhaustion, NOT a user cancel) — DS-5
	// row 2: restart un-freezes ANY failed reason, not just cancellations.
	m4 := &task.Task{
		ID: "m4", Title: "member-genuinely-failed", WorkspaceID: "ws", PlanID: p.ID,
		Status: task.StatusFailed, AttemptCount: 3, Result: "attempt limit exhausted (genuine failure)",
	}
	for _, tk := range []*task.Task{m3, m1, m2, m4} {
		if err := taskStore.Create(tk); err != nil {
			t.Fatalf("create %s: %v", tk.ID, err)
		}
	}

	// --- Drive the restart: the store-level primitives a future
	// POST /plans/{id}/restart handler will orchestrate ---

	approved := plan.StateApproved
	restartedPlan, err := planStore.Update(p.ID, plan.Patch{State: &approved})
	if err != nil {
		t.Fatalf("plan restart transition: %v", err)
	}

	// Reset every NON-done member. An orchestrator must SKIP done members —
	// RestartReset itself refuses to touch one (ErrNotRestartable), which
	// this loop relies on implicitly by only listing failed/cancelled ids.
	for _, id := range []string{m1.ID, m2.ID, m4.ID} {
		if _, err := taskStore.RestartReset(id); err != nil {
			t.Fatalf("RestartReset(%s): %v", id, err)
		}
	}

	// --- Assertions: plan side (FR-016/017) ---

	if restartedPlan.State != plan.StateApproved {
		t.Fatalf("plan state = %q, want %q (restart re-enters at approved, NOT running)",
			restartedPlan.State, plan.StateApproved)
	}
	if restartedPlan.FailedReason != "" {
		t.Errorf("plan FailedReason = %q, want cleared (a restarted plan is no longer stopped_by_user)",
			restartedPlan.FailedReason)
	}
	if restartedPlan.JudgeRounds != 0 {
		t.Errorf("plan JudgeRounds = %d, want 0 (restart resets the judge-round counter)", restartedPlan.JudgeRounds)
	}

	// --- Assertions: done members preserved BYTE-FOR-BYTE, with evidence ---

	gotD1, err := taskStore.Get(d1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotD1.Status != task.StatusDone {
		t.Errorf("d1 status = %q, want done (restart must never touch a done member)", gotD1.Status)
	}
	gotD2, err := taskStore.Get(d2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotD2.Status != task.StatusDone {
		t.Errorf("d2 status = %q, want done (restart must never touch a done member)", gotD2.Status)
	}

	evD1, err := evidenceStore.List(d1.ID)
	if err != nil {
		t.Fatalf("list evidence d1: %v", err)
	}
	if len(evD1) != 1 || evD1[0].ID != ev1.ID || evD1[0].Output != "d1 output: distinct content A" {
		t.Fatalf("d1 evidence not preserved intact: %+v (want ID=%s, distinct content A)", evD1, ev1.ID)
	}
	evD2, err := evidenceStore.List(d2.ID)
	if err != nil {
		t.Fatalf("list evidence d2: %v", err)
	}
	if len(evD2) != 1 || evD2[0].ID != ev2.ID || evD2[0].Output != "d2 output: distinct content B" {
		t.Fatalf("d2 evidence not preserved intact: %+v (want ID=%s, distinct content B)", evD2, ev2.ID)
	}
	// Differentiation: the two done members' evidence must NOT have been
	// cross-contaminated or collapsed into one shared record.
	if evD1[0].Output == evD2[0].Output {
		t.Fatal("d1 and d2 evidence output must remain distinct per-member, got identical content")
	}

	// --- Assertions: non-done members reset (attempt_count 0, cancel reason
	// cleared, correct next/blocked landing state) ---

	gotM1, err := taskStore.Get(m1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotM1.Status != task.StatusNext {
		t.Errorf("m1 (cancelled, unblocked) status = %q, want next", gotM1.Status)
	}
	if gotM1.AttemptCount != 0 {
		t.Errorf("m1 attempt_count = %d, want 0", gotM1.AttemptCount)
	}
	if gotM1.CancelReason != "" {
		t.Errorf("m1 cancel_reason = %q, want cleared", gotM1.CancelReason)
	}

	gotM2, err := taskStore.Get(m2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotM2.Status != task.StatusBlocked {
		t.Errorf("m2 (cancelled, blocked on not-done m3) status = %q, want blocked "+
			"(DS-5: reset to next/blocked — dependency-aware)", gotM2.Status)
	}
	if gotM2.AttemptCount != 0 {
		t.Errorf("m2 attempt_count = %d, want 0", gotM2.AttemptCount)
	}
	if gotM2.CancelReason != "" {
		t.Errorf("m2 cancel_reason = %q, want cleared", gotM2.CancelReason)
	}

	gotM4, err := taskStore.Get(m4.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotM4.Status != task.StatusNext {
		t.Errorf("m4 (genuinely failed, non-cancelled) status = %q, want next "+
			"(DS-5 row 2: restart un-freezes ANY failed reason)", gotM4.Status)
	}
	if gotM4.AttemptCount != 0 {
		t.Errorf("m4 attempt_count = %d, want 0", gotM4.AttemptCount)
	}

	// m3 (never failed, never touched by restart) must be untouched.
	gotM3, err := taskStore.Get(m3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotM3.Status != task.StatusNext {
		t.Errorf("m3 status = %q, want next (untouched by restart)", gotM3.Status)
	}
}

// TestRestartContinuity_RejectsGenuineFailureReason is DS-1's negative
// companion assembled at the SAME orchestration level as the happy-path test
// above: a plan that is `failed` for a GENUINE reason (judge_rounds_exhausted
// or idle_expired, not a user Stop) must have its restart attempt rejected by
// the store-level guard — proving the future restart handler's very first
// step (the plan-side transition) fails closed before any task-level
// RestartReset would ever be attempted, so no member state is touched.
func TestRestartContinuity_RejectsGenuineFailureReason(t *testing.T) {
	cases := []plan.FailedReason{
		plan.FailedReasonJudgeRoundsExhausted,
		plan.FailedReasonIdleExpired,
	}
	for _, reason := range cases {
		reason := reason
		t.Run(string(reason), func(t *testing.T) {
			dir := t.TempDir()
			planStore := plan.New(filepath.Join(dir, "plans"))
			taskStore := task.New(filepath.Join(dir, "tasks"))

			p := &plan.Plan{
				ID: "p-genuine-fail-" + string(reason), Title: "p", WorkspaceID: "ws",
				OwnerAgentID: "owner", State: plan.StateFailed, FailedReason: reason, JudgeRounds: 5,
			}
			if err := planStore.Create(p); err != nil {
				t.Fatalf("create plan: %v", err)
			}
			member := &task.Task{
				ID: "m-genuine-fail", Title: "m", WorkspaceID: "ws", PlanID: p.ID,
				Status: task.StatusFailed, AttemptCount: 3,
			}
			if err := taskStore.Create(member); err != nil {
				t.Fatalf("create member: %v", err)
			}

			approved := plan.StateApproved
			if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err == nil {
				t.Fatalf("expected the restart transition to be rejected for FailedReason=%q, got no error", reason)
			}

			// The plan must remain exactly as it was — no partial mutation.
			gotPlan, gerr := planStore.Get(p.ID)
			if gerr != nil {
				t.Fatal(gerr)
			}
			if gotPlan.State != plan.StateFailed || gotPlan.FailedReason != reason || gotPlan.JudgeRounds != 5 {
				t.Fatalf("plan was mutated by a rejected restart attempt: state=%q reason=%q rounds=%d",
					gotPlan.State, gotPlan.FailedReason, gotPlan.JudgeRounds)
			}

			// Because the plan-side transition failed, an orchestrator must
			// never reach the member-reset step — confirm the member is
			// still exactly as it was (this test does not call RestartReset
			// at all, mirroring the real handler's early-return contract).
			gotMember, merr := taskStore.Get(member.ID)
			if merr != nil {
				t.Fatal(merr)
			}
			if gotMember.Status != task.StatusFailed || gotMember.AttemptCount != 3 {
				t.Fatalf("member was unexpectedly mutated despite the plan-side restart rejection: %+v", gotMember)
			}
		})
	}
}

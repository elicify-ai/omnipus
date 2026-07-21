// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_stop_test.go covers PlanEngine.StopPlan/StopTask (ADR-052 US-6/US-7,
// FR-009/010/013/025/029/037/041), the verdict-application state re-check
// (FR-014), and the FR-041 "member-cancel plan outcome" rule — all using the
// SAME lightweight fake-collaborator harness plan_engine_test.go's other
// tests use (newTestPlanEngine), plus a fake sessionCanceller.

package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// fakeSessionCanceller is a controllable sessionCanceller double. It records
// every RequestCancelForSession call so tests can assert exactly which
// sessions the Stop fan-out reached.
type fakeSessionCanceller struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSessionCanceller) RequestCancelForSession(_ context.Context, sessionID, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionID)
	return true, nil
}

func (f *fakeSessionCanceller) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeSessionCanceller) contains(sessionID string) bool {
	for _, c := range f.callList() {
		if c == sessionID {
			return true
		}
	}
	return false
}

// --- US-6: StopPlan fan-out (Test 8 / DS-4) --------------------------------

func TestPlanEngine_StopPlan_FanoutCancelsMembersAndPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	canceller := &fakeSessionCanceller{}
	h.pe.canceller = canceller

	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress, SessionID: "sess-m1",
	})
	m2 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m2", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress, SessionID: "sess-m2",
	})
	m3Done := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m3-already-done", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone, SessionID: "sess-m3",
	})
	// A plan-level verifier session, registered as if a plan-judge round
	// were mid-flight (R3-7/FR-037's registry contract).
	h.pe.VerifierRegistry().Register("p1", "verifier-plan")
	// A member-level verifier session for m1, registered the same way
	// (mirrors judge.go's runVerifierAdjudication registering BEFORE
	// dispatching the verifier's turn).
	h.pe.VerifierRegistry().Register(m1.ID, "verifier-m1")

	updated, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web")
	if err != nil {
		t.Fatalf("StopPlan: %v", err)
	}
	if updated.State != plan.StateFailed || updated.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("plan state=%q reason=%q, want failed/stopped_by_user", updated.State, updated.FailedReason)
	}

	for _, sess := range []string{"sess-m1", "sess-m2", "verifier-plan", "verifier-m1"} {
		if !canceller.contains(sess) {
			t.Errorf("expected session %q to be cancelled by the fan-out; calls=%v", sess, canceller.callList())
		}
	}
	// m3's session was never in_progress and has no registered verifier —
	// it must NOT be touched (it was already terminal before Stop).
	if canceller.contains("sess-m3") {
		t.Errorf("did not expect m3's (already-done) session to be cancelled; calls=%v", canceller.callList())
	}

	for _, id := range []string{m1.ID, m2.ID} {
		got, gerr := h.tasks.Get(id)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if got.Status != task.StatusFailed {
			t.Errorf("member %s status = %q, want failed (cancelled)", id, got.Status)
		}
		if !isCancelledMember(got) {
			t.Errorf("member %s Result = %q, want the cancel marker prefix", id, got.Result)
		}
	}
	// m3 was already done before Stop — Stop must not touch a terminal member.
	gotM3, gerr := h.tasks.Get(m3Done.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if gotM3.Status != task.StatusDone {
		t.Fatalf("m3 status = %q, want done (unchanged by Stop)", gotM3.Status)
	}
}

func TestPlanEngine_StopPlan_RejectsWhenNotRunning(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateDraft,
	})

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err == nil {
		t.Fatal("expected StopPlan on a non-running plan to be rejected")
	}
}

func TestPlanEngine_StopPlan_UnknownPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	if _, err := h.pe.StopPlan(context.Background(), "does-not-exist", "tester", "web"); err == nil {
		t.Fatal("expected StopPlan on an unknown plan id to error")
	}
}

// --- US-7: StopTask / member-Stop (Test 17, A5) ----------------------------

func TestPlanEngine_StopTask_MemberOnlyPlanContinues(t *testing.T) {
	h := newTestPlanEngine(t)
	canceller := &fakeSessionCanceller{}
	h.pe.canceller = canceller

	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress, SessionID: "sess-m1",
	})
	m2 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m2", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress, SessionID: "sess-m2",
	})
	h.pe.VerifierRegistry().Register(m1.ID, "verifier-m1")

	updated, err := h.pe.StopTask(context.Background(), m1.ID, "tester", "web")
	if err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	if updated.Status != task.StatusFailed || !isCancelledMember(updated) {
		t.Fatalf("m1 status=%q result=%q, want failed+cancel-marker", updated.Status, updated.Result)
	}

	// Only m1's sessions were cancelled — m2's worker session must be
	// untouched (A5: member-Stop clears ONLY that member's goal).
	if !canceller.contains("sess-m1") || !canceller.contains("verifier-m1") {
		t.Errorf("expected m1's worker+verifier sessions cancelled; calls=%v", canceller.callList())
	}
	if canceller.contains("sess-m2") {
		t.Errorf("member-Stop must not touch m2's session; calls=%v", canceller.callList())
	}

	// The plan itself is untouched by a member-level Stop.
	gotPlan, perr := h.plans.Get("p1")
	if perr != nil {
		t.Fatal(perr)
	}
	if gotPlan.State != plan.StateRunning {
		t.Fatalf("plan state = %q, want running (member-Stop does not touch the plan)", gotPlan.State)
	}

	// m2 is unaffected.
	gotM2, terr := h.tasks.Get(m2.ID)
	if terr != nil {
		t.Fatal(terr)
	}
	if gotM2.Status != task.StatusInProgress {
		t.Fatalf("m2 status = %q, want in_progress (untouched)", gotM2.Status)
	}
}

func TestPlanEngine_StopTask_RejectsWhenNotInProgress(t *testing.T) {
	h := newTestPlanEngine(t)
	tk := mustCreateTask(t, h.tasks, &task.Task{
		Title: "idle", WorkspaceID: "ws", Status: task.StatusNext,
	})
	if _, err := h.pe.StopTask(context.Background(), tk.ID, "tester", "web"); err == nil {
		t.Fatal("expected StopTask on a non-in_progress task to be rejected")
	}
}

func TestPlanEngine_StopTask_StandaloneNoPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	canceller := &fakeSessionCanceller{}
	h.pe.canceller = canceller

	tk := mustCreateTask(t, h.tasks, &task.Task{
		Title: "standalone", WorkspaceID: "ws", Status: task.StatusInProgress, SessionID: "sess-solo",
	})
	updated, err := h.pe.StopTask(context.Background(), tk.ID, "tester", "web")
	if err != nil {
		t.Fatalf("StopTask on a standalone task: %v", err)
	}
	if updated.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed", updated.Status)
	}
	if !canceller.contains("sess-solo") {
		t.Fatalf("expected the standalone task's session to be cancelled; calls=%v", canceller.callList())
	}
}

// --- FR-014: stale verdict dropped after a concurrent Stop (Test 7) -------

func TestPlanEngine_StopPlan_DropsStaleVerdictAfterConcurrentJudgeRound(t *testing.T) {
	h := newTestPlanEngine(t)
	canceller := &fakeSessionCanceller{}
	h.pe.canceller = canceller

	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		DoD: []task.AcceptanceCriterion{planProseCriterion("done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})

	registered := make(chan struct{})
	proceed := make(chan struct{})
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		// Mimic judge.go's runVerifierAdjudication (FR-011/037): register
		// the real verifier session id BEFORE the verifier's own turn would
		// run, then simulate that turn still being in flight when Stop
		// lands.
		h.pe.VerifierRegistry().Register(in.PlanID, "verifier-sess-1")
		close(registered)
		<-proceed
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: true}}
	}

	h.pe.processPlan(context.Background(), "p1") // starts the round goroutine, then returns

	<-registered
	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}
	close(proceed) // let the now-stale MET verdict return
	h.pe.judgeWG.Wait()

	if !canceller.contains("verifier-sess-1") {
		t.Fatalf("expected the registered plan-level verifier session to be cancelled; calls=%v", canceller.callList())
	}

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("state=%q reason=%q, want failed/stopped_by_user — the stale MET verdict must NOT "+
			"overwrite the Stop outcome (FR-014)", got.State, got.FailedReason)
	}
}

// --- Concurrent dispatch vs. Stop (Test 10 / SC-005, "no escape") ----------

// TestPlanEngine_StopPlan_NoEscapeUnderConcurrentDispatch races
// processPlan's dispatch against StopPlan many times. h.disp.onDispatch is
// scripted to mimic the REAL TaskExecutor.ExecuteTask post-M1 contract
// exactly: claim (next->in_progress) AND persist a SessionID, both
// SYNCHRONOUSLY within the one dispatch call — so this test exercises
// PlanEngine's OWN locking/enumeration guarantee (both dispatch and Stop run
// under planDecisionMu) rather than re-testing TaskExecutor's own M1
// mechanics (covered separately in task_executor_session_sync_test.go).
// Mirrors spec Test 10's >=100-iteration stress bar (SC-005: 0 escapes).
func TestPlanEngine_StopPlan_NoEscapeUnderConcurrentDispatch(t *testing.T) {
	const iterations = 100
	for i := 0; i < iterations; i++ {
		h := newTestPlanEngine(t)
		canceller := &fakeSessionCanceller{}
		h.pe.canceller = canceller

		planID := fmt.Sprintf("p-%d", i)
		mustCreateRunningPlan(t, h.plans, planID, "owner")
		tk := mustCreateTask(t, h.tasks, &task.Task{
			Title: "m1", WorkspaceID: "ws", PlanID: planID, Status: task.StatusNext,
		})

		h.disp.onDispatch = func(taskID string) error {
			inProgress := task.StatusInProgress
			sess := "sess-" + taskID
			_, err := h.tasks.Update(taskID, task.Patch{Status: &inProgress, SessionID: &sess})
			return err
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			h.pe.processPlan(context.Background(), planID)
		}()
		go func() {
			defer wg.Done()
			if _, err := h.pe.StopPlan(context.Background(), planID, "tester", "web"); err != nil {
				t.Errorf("iteration %d: StopPlan: %v", i, err)
			}
		}()
		wg.Wait()

		final, err := h.tasks.Get(tk.ID)
		if err != nil {
			t.Fatalf("iteration %d: get task: %v", i, err)
		}
		if final.Status == task.StatusInProgress {
			t.Fatalf("iteration %d: task left in_progress after concurrent Stop+dispatch (escape!)", i)
		}
		if final.Status == task.StatusFailed && !canceller.contains("sess-"+tk.ID) {
			t.Fatalf("iteration %d: task cancelled but its session was never signalled; calls=%v",
				i, canceller.callList())
		}

		gotPlan, perr := h.plans.Get(planID)
		if perr != nil {
			t.Fatalf("iteration %d: get plan: %v", i, perr)
		}
		if gotPlan.State != plan.StateFailed || gotPlan.FailedReason != plan.FailedReasonStoppedByUser {
			t.Fatalf("iteration %d: plan state=%q reason=%q, want failed/stopped_by_user",
				i, gotPlan.State, gotPlan.FailedReason)
		}
	}
}

// --- FR-041: member-cancel plan outcome (Test 29, DS-4) --------------------

func TestPlanEngine_FR041_BlockedBehindCancelledMember_PlanFailsImmediately(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1-cancelled", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})
	// Cancel m1 the same way StopTask does (marker + failed).
	if _, err := h.pe.cancelMemberLocked(m1.ID, "tester"); err != nil {
		t.Fatalf("cancelMemberLocked: %v", err)
	}
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m2-done", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m3-blocked-behind-m1", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusBlocked,
		BlockedBy: []string{m1.ID},
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("state=%q reason=%q, want failed/stopped_by_user (FR-041 should have fired)",
			got.State, got.FailedReason)
	}
	if h.judge.callCount() != 0 {
		t.Fatalf("judge was called %d time(s); FR-041 must skip judge rounds entirely", h.judge.callCount())
	}
}

func TestPlanEngine_FR041_IndependentRunnableMember_DoesNotFire(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1-cancelled", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})
	if _, err := h.pe.cancelMemberLocked(m1.ID, "tester"); err != nil {
		t.Fatalf("cancelMemberLocked: %v", err)
	}
	// m2 is independent (no blocked_by relationship to m1 at all) and still
	// actively runnable — progress remains possible, so FR-041 must NOT
	// fire; the plan stays running.
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m2-independent-running", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want running (an independent runnable member exists — FR-041 must not fire)",
			got.State)
	}
}

// TestPlanEngine_FR041_TransitiveBlockChain_PlanFails covers a 2-hop
// transitive chain (m3 blocked behind m2, m2 blocked behind the cancelled
// m1) — "directly or transitively" per FR-041's own text.
func TestPlanEngine_FR041_TransitiveBlockChain_PlanFails(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1-cancelled", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress,
	})
	if _, err := h.pe.cancelMemberLocked(m1.ID, "tester"); err != nil {
		t.Fatalf("cancelMemberLocked: %v", err)
	}
	m2 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m2-blocked-behind-m1", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusBlocked,
		BlockedBy: []string{m1.ID},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m3-blocked-behind-m2", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusBlocked,
		BlockedBy: []string{m2.ID},
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("state=%q reason=%q, want failed/stopped_by_user (transitive dead-end chain)",
			got.State, got.FailedReason)
	}
}

// TestPlanEngine_FR041_NoCancellationAtAll_NeverFires proves the top-level
// gate: with zero cancelled members, FR-041 must never fire even if a
// member is genuinely `failed` and another is blocked behind it — that
// combination is ordinary (pre-existing, unchanged-by-this-feature) plan
// behavior, not a Stop-triggered outcome, so it must not short-circuit
// through this new rule (regression requirement: unaffected for a plan with
// no cancellations).
func TestPlanEngine_FR041_NoCancellationAtAll_NeverFires(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1-genuinely-failed", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusFailed,
		Result: "ran out of attempts (not a user cancel)",
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m2-blocked-behind-m1", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusBlocked,
		BlockedBy: []string{m1.ID},
	})

	h.pe.processPlan(context.Background(), "p1")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("state = %q, want running (no member was ever cancelled — FR-041's gate must not open)",
			got.State)
	}
}

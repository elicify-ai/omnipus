// task_executor_judge_test.go: tests for claim, verdict and completion contract for a finished attempt

package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- moved from task_executor.go tests 2026-09-15 ---

func TestWriteJudgeVerdictTranscript_EmitsLiveEventWithTaskSessionID(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")
	tk := &task.Task{
		Title: "live event judge verdict", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	events, cleanup := newEventCollector(t, al)
	defer cleanup()

	verdict := &task.JudgeVerdict{
		ID: "verdict-1", Scope: task.VerdictScopeTask, TaskID: tk.ID,
		Round: 2, Met: true, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: true, Reason: "evidenced"}},
	}
	al.taskExecutor.writeJudgeVerdictTranscript(tk, sessionID, verdict)
	cleanup() // stop the collector goroutine before reading c.events

	live := judgeVerdictPayloadsFor(events, sessionID)
	if len(live) != 1 {
		t.Fatalf("%d live judge_verdict events for %q; want exactly 1", len(live), sessionID)
	}
	if live[0].Verdict.ID != verdict.ID {
		t.Errorf("live event verdict id = %q; want %q", live[0].Verdict.ID, verdict.ID)
	}
	if live[0].Verdict.Round != 2 {
		t.Errorf("live event verdict round = %d; want 2", live[0].Verdict.Round)
	}
}

func TestWriteJudgeVerdictTranscript_NonexistentSession_NoLiveEvent(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	tk := &task.Task{
		Title: "live event judge verdict negative", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	events, cleanup := newEventCollector(t, al)
	defer cleanup()

	verdict := &task.JudgeVerdict{Round: 1, Met: false, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "unmet"}}}
	al.taskExecutor.writeJudgeVerdictTranscript(tk, u26NonexistentSessionID, verdict)
	cleanup()

	// A failed transcript write must never fire the live event — a card
	// with nothing durable behind it is worse than no card (mirrors
	// recordGoalOutcome's "NO frame is sent" rule, goal_outcome.go).
	if live := judgeVerdictPayloadsFor(events, u26NonexistentSessionID); len(live) != 0 {
		t.Errorf("%d live judge_verdict events fired for a failed transcript write; want 0", len(live))
	}
}

// --- Primitive-level gate follow-up (DoD items 2-4) -------------------------

// TestAdvanceBlockedTasks_StoppedPlanMemberNotDispatched is DoD item 2: a
// blocked dependent whose full BlockedBy set becomes satisfied by a
// completing plan member must NOT be dispatched once the plan has already
// been stopped (failed(stopped_by_user)) — even though
// AdvanceBlockedDependents itself promotes blocked->next unconditionally and
// plan-agnostically, exactly as it always has.
//
// Root-cause repro (PRIYA-D8-race's advanceBlockedTasks sibling): depA is an
// in-flight plan member (in_progress) when a Stop lands and transitions the
// plan to failed(stopped_by_user) — the Stop fan-out only cancels members
// already in_progress AT THAT INSTANT, but depA's own completion (racing the
// cancel) reaches onTaskComplete a moment later anyway. Before the
// primitive-level fix, onTaskComplete -> advanceBlockedTasks -> ExecuteTask
// had NO plan-state gate at all (only CheckQueuedTasks did), so depB — newly
// unblocked by depA's completion — would dispatch and run to completion over
// a plan the user had already stopped. This is the exact leak the S1 fix's
// CheckQueuedTasks-only gate placement reopened for every non-heartbeat
// caller.
func TestAdvanceBlockedTasks_StoppedPlanMemberNotDispatched(t *testing.T) {
	provider := newClaimingWorker(turnClaimMet("verified the change directly"))
	al := newNativeTaskCompletionTestLoop(t, provider)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)

	p := newPlanGateTestPlan(t, planStore, plan.StateRunning)

	depA := newPlanGateTestTask(t, al, p.ID)
	now := time.Now().UTC().Format(time.RFC3339)
	claimedA, err := al.taskStore.Update(depA.ID, task.Patch{
		Status: ptrStatus(task.StatusInProgress), StartedAt: &now,
	})
	if err != nil {
		t.Fatalf("claim depA in_progress: %v", err)
	}
	depA = claimedA

	depB := newPlanGateTestTask(t, al, p.ID)
	blockedByA := []string{depA.ID}
	if _, blockErr := al.taskStore.Update(depB.ID, task.Patch{BlockedBy: &blockedByA}); blockErr != nil {
		t.Fatalf("set depB blocked_by: %v", blockErr)
	}
	gotB, err := al.taskStore.Get(depB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotB.Status != task.StatusBlocked {
		t.Fatalf("setup: depB status = %q, want blocked (unmet dep at recompute time)", gotB.Status)
	}

	// The Stop lands BEFORE depA's in-flight completion is processed — the
	// exact PRIYA-D8-race timing window.
	stateFailed := plan.StateFailed
	if _, stopErr := planStore.Update(p.ID, plan.Patch{
		State: &stateFailed, FailedReason: ptrFailedReason(plan.FailedReasonStoppedByUser),
	}); stopErr != nil {
		t.Fatalf("stop plan: %v", stopErr)
	}

	doneA, err := al.taskStore.Update(depA.ID, task.Patch{
		Status: ptrStatus(task.StatusDone), CompletedAt: &now,
	})
	if err != nil {
		t.Fatalf("complete depA: %v", err)
	}

	al.taskExecutor.onTaskComplete(doneA)

	final, err := al.taskStore.Get(depB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == task.StatusInProgress {
		t.Fatalf("depB status = %q, must NOT be in_progress — a blocked dependent must not dispatch "+
			"once its plan has been stopped", final.Status)
	}
	if final.Status != task.StatusNext {
		t.Fatalf("depB status = %q, want next (AdvanceBlockedDependents promotes it plan-agnostically, "+
			"but the plan gate inside ExecuteTask must then refuse to dispatch it)", final.Status)
	}
	if calls := provider.turnsStarted(); calls != 0 {
		t.Fatalf("provider was called %d time(s) — depB must never reach the LLM once its plan was stopped", calls)
	}
}

// TestCompleteTaskWithResult_DoneOverwriteGuard_DropsOutcomeAfterConcurrentStop
// is interleaving (b): a stale MET claim resolving to `done` must never
// overwrite a task the user Stopped. Also proves the returned `applied`
// bool correctly reports false on a dropped write (callers gate
// wakeOwnerAttemptsExhausted on this).
func TestCompleteTaskWithResult_DoneOverwriteGuard_DropsOutcomeAfterConcurrentStop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)

	tk := &task.Task{
		ID: "t-done-overwrite-guard", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "done overwrite guard",
		Status: task.StatusInProgress,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	stale := *tk
	simulateConcurrentStop(t, taskStore, tk.ID)

	applied := al.taskExecutor.completeTaskWithResult(&stale, "", task.StatusInProgress, true, "claims success", nil)
	if applied {
		t.Fatal("completeTaskWithResult must report applied=false on a CAS conflict")
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("status=%q cancel_reason=%q, want the Stop outcome UNCHANGED — a stale MET claim must never "+
			"silently complete a cancelled task as done", final.Status, final.CancelReason)
	}
	if final.Result == "claims success" {
		t.Error("the stale claim's Result must never overwrite the Stop's own Result")
	}
}

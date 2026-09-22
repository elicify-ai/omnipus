// task_executor_judge_test.go: tests for claim, verdict and completion contract for a finished attempt

package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

type lifecycleOrderingDeliverer struct {
	lifecycle       *session.LifecycleStore
	stateAtDelivery session.LifecycleState
	outcome         steer.Outcome
}

func (d *lifecycleOrderingDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	rec, err := d.lifecycle.Load(event.ChildSessionID)
	if err != nil {
		return steer.Delivery{}, err
	}
	d.stateAtDelivery = rec.State
	d.outcome = event.Outcome
	return steer.Delivery{MessageID: "observed", Outcome: steer.DeliveryWoke}, nil
}

func TestCompleteTask_EmptyAnswerFailsAndDeliversBeforeLifecycleTerminal(t *testing.T) {
	al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	al.taskExecutor.SetLifecycleStore(lifecycle)
	const taskSessionID = "task-empty-session"
	seedParentAndChild(t, lifecycle, "parent-1", taskSessionID)
	deliverer := &lifecycleOrderingDeliverer{lifecycle: lifecycle}
	al.SetSteerAudienceDeps(nil, nil, deliverer)
	tk := &task.Task{Title: "empty", Prompt: "x", Action: task.ActionLLM, AgentID: "native-agent", WorkspaceID: "default", Priority: 3, Status: task.StatusInProgress, SessionID: taskSessionID}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatal(err)
	}
	if !al.taskExecutor.completeTaskWithResult(tk, taskSessionID, task.StatusInProgress, true, "  ", nil) {
		t.Fatal("completion was not applied")
	}
	final, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || deliverer.outcome != steer.OutcomeEmptyAnswer {
		t.Fatalf("task status/outcome = %q/%q, want failed/empty_answer", final.Status, deliverer.outcome)
	}
	if deliverer.stateAtDelivery != session.LifecycleRunning {
		t.Fatalf("lifecycle state at upward delivery = %q, want running (inbox before terminal write)", deliverer.stateAtDelivery)
	}
	rec, err := lifecycle.Load(taskSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != session.LifecycleFailed {
		t.Fatalf("final lifecycle state = %q, want failed", rec.State)
	}
}

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

// TestWriteJudgeVerdictTranscript_NotMet_DeliversGoalStatusUpward covers
// FR-B-017/AS-12 (TDD plan test 31): when the deciding session is a steered
// child (this task's run session is steered by a parent) and the Judge rules
// "not met" on some of its criteria, writeJudgeVerdictTranscript — in
// addition to its existing FR-056 transcript write and live event — MUST
// also deliver a goal_status SessionMessage upward through I-5's
// steer.UpwardDeliverer, with direction session_to_parent, condition
// not_met, and one evidence entry per judged criterion.
func TestWriteJudgeVerdictTranscript_NotMet_DeliversGoalStatusUpward(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")

	// Wire I-5's steer deps and steer this task's run session by a parent —
	// the "C ran with a goal, steered by B" shape AS-12 describes.
	const parentID = "steering-parent"
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	seedParentAndChild(t, lifecycle, parentID, sessionID)
	deliverer := NewSteerUpwardDeliverer()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{}, deliverer)

	// This task's run session carries an active /goal — activeGoalForSession
	// resolves the goal_id a SessionMessageGoalStatus must carry (GOAL-FR-013,
	// the one session-bound lookup shared by chat-owned and task-owned goals).
	if err := resolveGoalRecordStore().Create(&goal.Goal{
		GoalID: "g-verdict-1", Prompt: "ship the feature", MaxRounds: 3,
		OwnerKind: generated.GoalOwnerKindSession, OwnerID: sessionID, Source: generated.GoalSourceChatCompiled,
		State: generated.GoalStateActive, ActiveSessionID: sessionID,
		DoD: []task.AcceptanceCriterion{planProseCriterion("ship it")},
	}); err != nil {
		t.Fatalf("seed active goal record: %v", err)
	}

	tk := &task.Task{
		Title: "not-met judge verdict upward delivery", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	verdict := &task.JudgeVerdict{
		ID: "verdict-2", Scope: task.VerdictScopeTask, TaskID: tk.ID,
		Round: 2, Met: false, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: true, Reason: "done"},
			{CriterionID: "c2", Met: false, Reason: "still failing"},
			{CriterionID: "c3", Met: false, Reason: "missing coverage"},
		},
	}
	al.taskExecutor.writeJudgeVerdictTranscript(tk, sessionID, verdict)

	msgs, _, _, derr := inbox.Drain(parentID, sessionID, "", 10)
	if derr != nil {
		t.Fatalf("Drain: %v", derr)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 goal_status entry in the parent's inbox, got %d", len(msgs))
	}
	kind, _ := msgs[0].Discriminator()
	if kind != "goal_status" {
		t.Fatalf("kind = %q, want goal_status", kind)
	}
	gs, gerr := msgs[0].AsSessionMessageGoalStatus()
	if gerr != nil {
		t.Fatalf("AsSessionMessageGoalStatus: %v", gerr)
	}
	if gs.Condition != generated.SessionMessageGoalStatusConditionNotMet {
		t.Errorf("condition = %q, want not_met", gs.Condition)
	}
	if gs.Direction != generated.SessionMessageGoalStatusDirectionSessionToParent {
		t.Errorf("direction = %q, want session_to_parent", gs.Direction)
	}
	if gs.GoalId != "g-verdict-1" {
		t.Errorf("goal_id = %q, want g-verdict-1 (activeGoalForSession's own record)", gs.GoalId)
	}
	if gs.SessionId != sessionID {
		t.Errorf("session_id = %q, want the task's own run session %q", gs.SessionId, sessionID)
	}
	if gs.Evidence == nil || len(*gs.Evidence) != 3 {
		t.Fatalf("expected 3 evidence items (one per judged criterion), got %v", gs.Evidence)
	}
}

// TestWriteJudgeVerdictTranscript_Met_DeliversGoalStatus covers FR-C-009's
// positive verdict half: the parent receives the Judge's actual decision,
// not an inferred handback-only success.
func TestWriteJudgeVerdictTranscript_Met_DeliversGoalStatus(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")

	const parentID = "steering-parent-2"
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	seedParentAndChild(t, lifecycle, parentID, sessionID)
	deliverer := NewSteerUpwardDeliverer()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{}, deliverer)
	if err := resolveGoalRecordStore().Create(&goal.Goal{
		GoalID: "g-verdict-2", Prompt: "ship the feature", MaxRounds: 3,
		OwnerKind: generated.GoalOwnerKindSession, OwnerID: sessionID, Source: generated.GoalSourceChatCompiled,
		State: generated.GoalStateActive, ActiveSessionID: sessionID,
		DoD: []task.AcceptanceCriterion{planProseCriterion("ship it")},
	}); err != nil {
		t.Fatalf("seed active goal record: %v", err)
	}

	tk := &task.Task{
		Title: "met judge verdict upward delivery", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	verdict := &task.JudgeVerdict{
		ID: "verdict-3", Scope: task.VerdictScopeTask, TaskID: tk.ID,
		Round: 1, Met: true, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: true, Reason: "done"}},
	}
	al.taskExecutor.writeJudgeVerdictTranscript(tk, sessionID, verdict)

	msgs, _, _, derr := inbox.Drain(parentID, sessionID, "", 10)
	if derr != nil {
		t.Fatalf("Drain: %v", derr)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected one goal_status entry for a MET verdict, got %d", len(msgs))
	}
	gs, gerr := msgs[0].AsSessionMessageGoalStatus()
	if gerr != nil {
		t.Fatalf("AsSessionMessageGoalStatus: %v", gerr)
	}
	if gs.Condition != generated.SessionMessageGoalStatusConditionMet {
		t.Fatalf("condition = %q, want met", gs.Condition)
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

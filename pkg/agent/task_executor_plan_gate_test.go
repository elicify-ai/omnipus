// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_plan_gate_test.go covers the S1 UAT fix
// ("PRIYA-GATE-never-executed" / "PRIYA-D8-race"): CheckQueuedTasks (the
// heartbeat's unconditional ~1-minute auto-dispatch drain) must never
// auto-dispatch a plan member task whose parent plan has not actually been
// approved/is not running — a member task's own Status is fully
// player-settable (a Kanban drag straight from Inbox to Next in the SPA)
// independent of the plan's own lifecycle, so task.StatusNext alone is not
// proof the Execute approval gate was ever passed.
//
// Every test here drives the REAL CheckQueuedTasks against a real
// *task.Store and a real *plan.Store (wired via TaskExecutor.SetPlanStore),
// using the same newNativeTaskCompletionTestLoop harness the completion-
// contract tests use so dispatch — when it does happen — exercises the real
// ExecuteTask -> processTaskDirect -> provider.Chat path, not a stub.
//
// PRIMITIVE-LEVEL FOLLOW-UP: the tests below the original four cover the
// fix that moved this SAME gate (requirePlanExecuting, task_executor.go)
// down into ExecuteTask and StartTaskNow themselves — the two primitives
// every OTHER dispatch path (advanceBlockedTasks, StartTaskNow's REST
// caller, PlanEngine.dispatchReadyMembers) funnels through, which the
// original CheckQueuedTasks-only placement left completely ungated.

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newPlanGateTestPlan creates and persists a minimal, valid Plan in state
// for the given planStore, owned by "native-agent" on workspace "default"
// (matching newCompletionContractTask's task fixtures).
func newPlanGateTestPlan(t *testing.T, planStore *plan.Store, state plan.State) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Title:        "plan gate test plan",
		WorkspaceID:  "default",
		OwnerAgentID: "native-agent",
		State:        state,
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan (state=%s): %v", state, err)
	}
	return p
}

// newPlanGateTestTask creates a dispatchable `next` task for "native-agent",
// optionally attached to planID (empty = standalone, no PlanID).
func newPlanGateTestTask(t *testing.T, al *AgentLoop, planID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       "plan gate test task",
		Prompt:      "do the task",
		Action:      task.ActionLLM,
		AgentID:     "native-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		PlanID:      planID,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tk
}

// scriptedProviderCallCount reads p's call count under its own mutex — the
// struct exposes the field directly (no exported getter) since it lives in
// this same test package.
func scriptedProviderCallCount(p *scriptedProvider) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// successMarkerBody is a scripted LLM response that completes a (criteria-
// less) task to Done via the TASK_STATUS/TASK_SUMMARY marker, carrying the
// FR-035 evidence-marker line so the gate doesn't re-prompt it.
const successMarkerBody = "Did the work.\n" +
	"[goal:evidence] verified the change directly\n" +
	"TASK_STATUS: success\n" +
	"TASK_SUMMARY: done via plan gate test."

// TestCheckQueuedTasks_DraftPlanMemberNotDispatched is DoD item 1: a `next`
// member of a plan that was NEVER approved (still StateDraft, approved_at
// empty) must not be picked up by the heartbeat drain. Root-cause repro:
// "PRIYA-GATE-never-executed" — a plan member dragged Inbox->Next while the
// Execute button was never clicked.
func TestCheckQueuedTasks_DraftPlanMemberNotDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)

	p := newPlanGateTestPlan(t, planStore, plan.StateDraft)
	if p.ApprovedAt != "" {
		t.Fatalf("test fixture bug: fresh draft plan has non-empty approved_at %q", p.ApprovedAt)
	}
	tk := newPlanGateTestTask(t, al, p.ID)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q after CheckQueuedTasks, want %q (unchanged) — a DRAFT plan's member "+
			"must never auto-dispatch (S1: Execute approval gate bypass)", got.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — a draft plan's member task must never reach the LLM", calls)
	}

	// The parent plan itself must also be untouched by the drain.
	gotPlan, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if gotPlan.State != plan.StateDraft {
		t.Fatalf("plan state = %q after CheckQueuedTasks, want %q (unchanged)", gotPlan.State, plan.StateDraft)
	}
}

// TestCheckQueuedTasks_TerminalPlanMemberNotDispatched is DoD item 2: a
// `next` member of a plan that has already reached a terminal state (Stop ->
// failed(stopped_by_user), or any other terminal) must not be picked up by
// the heartbeat drain afterwards. Root-cause repro: "PRIYA-D8-race" — a
// member was `next` at the moment of Stop and the heartbeat dispatched it
// AFTER the stop, running it to completion over a plan the user had already
// terminated.
func TestCheckQueuedTasks_TerminalPlanMemberNotDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)

	p := newPlanGateTestPlan(t, planStore, plan.StateFailed)
	tk := newPlanGateTestTask(t, al, p.ID)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q after CheckQueuedTasks, want %q (unchanged) — a TERMINAL (stopped/failed) "+
			"plan's member must never auto-dispatch", got.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — a terminal plan's member task must never reach the LLM", calls)
	}
}

// TestCheckQueuedTasks_StandaloneTaskStillDispatched is the DoD's explicit
// regression guard: a task with NO PlanID (unrelated to any plan) must
// continue to be dispatched by the heartbeat exactly as before this fix —
// the plan-gate must only ever apply when PlanID is non-empty.
func TestCheckQueuedTasks_StandaloneTaskStillDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	// Deliberately do NOT call SetPlanStore — a standalone task must dispatch
	// even with no plan store wired at all (the common case: most task_executor
	// test harnesses never wire one, and production tasks outside any plan
	// must not regress just because a plan store also exists in the process).
	tk := newPlanGateTestTask(t, al, "" /* no PlanID */)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	// ExecuteTask's ClaimForRun (next->in_progress) runs synchronously inside
	// CheckQueuedTasks' own call to it, so the task must have already left
	// `next` by the time CheckQueuedTasks returns if — and only if — it was
	// actually dispatched.
	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status == task.StatusNext {
		t.Fatalf("status = %q immediately after CheckQueuedTasks, want it to have left %q — "+
			"a standalone (no PlanID) task must still auto-dispatch", got.Status, task.StatusNext)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — standalone task did not actually dispatch")
	}
}

// TestCheckQueuedTasks_ApprovedPlanMemberIsDispatched proves the gate is not
// over-broad: an APPROVED plan (DoD/owner locked in — the point at which a
// human granted permission for autonomous execution, even if the engine
// hasn't yet promoted it to `running`, e.g. cap-waiting) must still let its
// `next` member dispatch via the heartbeat drain.
func TestCheckQueuedTasks_ApprovedPlanMemberIsDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)

	p := newPlanGateTestPlan(t, planStore, plan.StateApproved)
	tk := newPlanGateTestTask(t, al, p.ID)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status == task.StatusNext {
		t.Fatalf("status = %q immediately after CheckQueuedTasks, want it to have left %q — "+
			"an APPROVED plan's member must dispatch", got.Status, task.StatusNext)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
}

// TestCheckQueuedTasks_RunningPlanMemberIsDispatched mirrors the Approved
// case above for StateRunning — the engine's normal in-flight state.
func TestCheckQueuedTasks_RunningPlanMemberIsDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)

	p := newPlanGateTestPlan(t, planStore, plan.StateRunning)
	tk := newPlanGateTestTask(t, al, p.ID)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status == task.StatusNext {
		t.Fatalf("status = %q immediately after CheckQueuedTasks, want it to have left %q — "+
			"a RUNNING plan's member must dispatch", got.Status, task.StatusNext)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
}

// TestCheckQueuedTasks_PausedRunningPlanMemberNotDispatched is DoD item 6
// (paused-plan follow-up): a plan with State==running but a non-empty
// PausedReason must NOT have its `next` members dispatched by the heartbeat
// drain. plan.PlanState is closed at 5 values with no separate "paused"
// state — FR-065 pauses a plan by setting PausedReason WITHOUT moving it out
// of StateRunning (plan_engine.go's processPlan checks PausedReason
// independently of State) — so a predicate keyed on State alone (the
// map[plan.State]bool this gate used to be before PermitsMemberDispatch)
// would keep dispatching a paused plan's members every ~60s tick forever,
// directly contradicting FR-065 in the very code meant to enforce the
// control. This test fails before the fix (State==running alone was
// sufficient to dispatch) and passes after it (PermitsMemberDispatch also
// checks PausedReason).
func TestCheckQueuedTasks_PausedRunningPlanMemberNotDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)

	p := newPlanGateTestPlan(t, planStore, plan.StateRunning)
	pausedReason := "owner_disabled"
	if _, err := planStore.Update(p.ID, plan.Patch{PausedReason: &pausedReason}); err != nil {
		t.Fatalf("pause plan: %v", err)
	}
	tk := newPlanGateTestTask(t, al, p.ID)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q after CheckQueuedTasks, want %q (unchanged) — a PAUSED running plan's "+
			"member must never auto-dispatch (FR-065)", got.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — a paused plan's member must never reach the LLM", calls)
	}
}

// TestCheckQueuedTasks_NoPlanStoreWired_PlanMemberFailsClosed proves the
// no-store-wired path (a boot sequence not yet past gateway.go's SetPlanStore
// call, or a minimal test harness) fails CLOSED for a plan member task — it
// must never be treated as if the gate did not apply, only a standalone
// (PlanID=="") task gets that treatment.
func TestCheckQueuedTasks_NoPlanStoreWired_PlanMemberFailsClosed(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	// Deliberately do NOT call SetPlanStore.
	tk := newPlanGateTestTask(t, al, "some-plan-id-with-no-store-to-resolve-it")

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q after CheckQueuedTasks, want %q (unchanged) — a plan member task must "+
			"fail closed (not dispatch) when no plan store is wired to verify its parent plan's state",
			got.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — must not dispatch with no plan store wired", calls)
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
	provider := &scriptedProvider{responseBody: successMarkerBody}
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
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — depB must never reach the LLM once its plan was stopped", calls)
	}
}

// TestExecuteTask_StandaloneTaskStillDispatches is DoD item 3 (ExecuteTask
// half): a standalone task (PlanID == "") dispatched DIRECTLY via ExecuteTask
// (not through CheckQueuedTasks) must be completely unaffected by the S1
// primitive-level plan-state gate — requirePlanExecuting returns nil
// immediately for PlanID == "" without ever touching the plan store, so this
// must succeed even with NO plan store wired at all.
func TestExecuteTask_StandaloneTaskStillDispatches(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	// Deliberately do NOT wire a plan store.
	tk := newPlanGateTestTask(t, al, "" /* no PlanID */)

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask on a standalone task must succeed, got: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — standalone task did not actually dispatch via ExecuteTask")
	}
}

// TestStartTaskNow_StandaloneTaskStillDispatches is DoD item 3 (StartTaskNow
// half): mirrors the above for StartTaskNow — the OTHER primitive the S1
// gate now sits inside, with no bypass of its own.
func TestStartTaskNow_StandaloneTaskStillDispatches(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newPlanGateTestTask(t, al, "" /* no PlanID */)

	// StartTaskNow's contract (see its own doc comment) is that the caller has
	// ALREADY transitioned the task to in_progress via a PATCH before calling
	// it — mirror that here (bypassing ClaimForRun, which requires `next`),
	// exactly like the REST handler and this package's own existing
	// StartTaskNow tests (start_task_now_test.go's createInProgressTask).
	inProgress, err := al.taskStore.Update(tk.ID, task.Patch{Status: ptrStatus(task.StatusInProgress)})
	if err != nil {
		t.Fatalf("advance to in_progress: %v", err)
	}

	sessID, err := al.taskExecutor.StartTaskNow(context.Background(), inProgress.ID)
	if err != nil {
		t.Fatalf("StartTaskNow on a standalone task must succeed, got: %v", err)
	}
	if sessID == "" {
		t.Fatal("StartTaskNow must return a non-empty session id")
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — standalone task did not actually dispatch via StartTaskNow")
	}
}

// TestDispatchReadyMembers_BypassesRedundantPlanGate_EvenWithTaskExecutorPlanStoreUnset
// is DoD item 4: PlanEngine.dispatchReadyMembers' documented bypass
// (executeTaskPlanVerified) must let a legitimately-admitted plan member
// dispatch even when TaskExecutor's OWN plan store is left completely
// unwired — proving the bypass is REAL, not merely inert. Without it, this
// dispatch would fail closed on "no plan store wired" (planForGate's
// contract) despite the plan engine itself having already verified, via ITS
// OWN correctly-wired plan store under planDecisionMu, that the plan permits
// dispatch — exactly the boot-ordering risk executeTaskPlanVerified's doc
// comment describes.
func TestDispatchReadyMembers_BypassesRedundantPlanGate_EvenWithTaskExecutorPlanStoreUnset(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	// Deliberately do NOT call al.taskExecutor.SetPlanStore — the crux of
	// this test: TaskExecutor's own planStore field stays nil throughout.
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	pe := NewPlanEngine(al, planStore, al.taskStore, al.taskExecutor)

	p := newPlanGateTestPlan(t, planStore, plan.StateRunning)
	tk := newPlanGateTestTask(t, al, p.ID)

	dispatched := pe.dispatchReadyMembers(context.Background(), p.ID, []task.Task{*tk})
	if !dispatched {
		t.Fatal("dispatchReadyMembers reported no dispatch — the plan-verified bypass must let a " +
			"legitimately-admitted member through even when TaskExecutor's own plan store is unset")
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — dispatchReadyMembers' bypass path did not actually dispatch")
	}
}

// TestDispatchReadyMembers_BypassDoesNotWeakenExecuteTask_ForOtherCallers is a
// negative control proving the bypass is scoped to dispatchReadyMembers
// alone: a DIRECT ExecuteTask call for the SAME task, via the SAME
// TaskExecutor (whose own plan store is still unset), is refused exactly as
// requirePlanExecuting's fail-closed "no plan store wired" contract demands
// — the bypass is a documented, narrow exception (executeTaskPlanVerified),
// never a general weakening of ExecuteTask's own gate.
func TestDispatchReadyMembers_BypassDoesNotWeakenExecuteTask_ForOtherCallers(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newNativeTaskCompletionTestLoop(t, provider)
	// TaskExecutor's own plan store is deliberately left unset, and this task
	// is dispatched via plain ExecuteTask — NOT through the plan engine's
	// bypass.
	tk := newPlanGateTestTask(t, al, "some-plan-id-with-no-store-on-executor")

	err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID)
	if err == nil {
		t.Fatal("ExecuteTask must fail closed when its own plan store is unset for a plan member task")
	}
	if !errors.Is(err, ErrPlanStateUnresolvable) {
		t.Fatalf("ExecuteTask error = %v, want errors.Is(_, ErrPlanStateUnresolvable)", err)
	}

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q, want unchanged %q — must not dispatch", got.Status, task.StatusNext)
	}
}

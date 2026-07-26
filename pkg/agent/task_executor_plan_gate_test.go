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

package agent

import (
	"context"
	"path/filepath"
	"testing"

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

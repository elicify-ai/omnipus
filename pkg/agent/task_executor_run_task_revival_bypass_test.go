// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_run_task_revival_bypass_test.go closes the LAST known bypass
// of requireRunTaskAutoDispatchApproved (task_executor.go) — the run_task
// ask-policy gate that guards every UNATTENDED (heartbeat-driven) dispatch
// path.
//
// The gate previously trusted a non-empty StartedAt AND SessionID pair as
// proof that a task's CURRENT run had already been claimed/dispatched at
// least once, and therefore did not need re-approval on the next unattended
// tick (task_executor_run_task_policy_gate_test.go covers that fix). That
// premise is still false in one remaining case: only `done` is frozen
// against status edits (pkg/task/store.go's validateTransition) — a
// `failed` task may be legally PATCHed straight back to `next` via a plain
// Update (handleTaskPatch, pkg/gateway/rest_tasks.go) OR via the task_update
// tool (pkg/tools/task.go, callable by the owning agent itself). NEITHER
// path clears StartedAt/SessionID — updateLocked only ever touches those
// fields when the caller's patch explicitly sets them, and neither of those
// two callers does. A task that ran once for real (StartedAt/SessionID/
// CompletedAt all genuinely stamped by that concluded run), reached
// `failed`, and is then revived with a bare `status: next` therefore still
// carries a genuine-looking, non-empty StartedAt/SessionID pair — and the
// very next CheckQueuedTasks tick would dispatch it as an "already-approved
// continuation" even though the run it is "continuing" already concluded
// and nobody re-approved anything.
//
// Fix: requireRunTaskAutoDispatchApproved now ALSO requires
// t.CompletedAt == "". Every terminal writer (completeTaskWithResult,
// failTask) stamps CompletedAt in the SAME write as the terminal status; the
// goal loop's own in-flight continuation writes (consumeAttemptOrExhaust,
// rejectBareEvidenceClaim) never touch it, because the task is not yet
// terminal while they run. This mirrors the existing SpawnReset/RestartReset
// precedent (pkg/task/store.go), which already clears CompletedAt alongside
// StartedAt/SessionID for exactly this "a fresh non-terminal state must not
// inherit a concluded run's claim" invariant.
//
// These tests are deliberately BLACK-BOX, following the sibling gate-test
// file's own convention: they drive only pre-existing, package-private
// surface (CheckQueuedTasks, ExecuteTask, taskStore.Update) so a mutation
// test that reverts ONLY the production change reproduces the ORIGINAL bug
// BEHAVIORALLY (the revived task actually dispatches and reaches the LLM),
// not merely a compile failure.
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newRunTaskGateRevivedFailedTask creates a task that already looks EXACTLY
// like a standalone task whose first (and only) run genuinely concluded:
// StartedAt, SessionID, and CompletedAt are all stamped, mirroring what
// completeTaskWithResult/failTask leave behind on a real terminal write.
// Status is created directly as `failed` — pkg/task's Create has no "from"
// state to validate against, so this is a faithful, minimal way to seed the
// precondition without going through a real (and here, irrelevant) dispatch
// cycle.
func newRunTaskGateRevivedFailedTask(t *testing.T, al *AgentLoop) *task.Task {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	tk := &task.Task{
		Title:       "run_task revival gate test task",
		Prompt:      "do the task",
		Action:      task.ActionLLM,
		AgentID:     "native-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusFailed,
		StartedAt:   now,
		SessionID:   "concluded-run-session",
		CompletedAt: now,
		Result:      "attempts exhausted without a judge-confirmed success",
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tk
}

// --- 1. Reproduction: the reported defect -----------------------------------

// TestCheckQueuedTasks_AskPolicyRevivedFailedTaskStillGated is the
// reproduction/regression test for the revival bypass described in this
// file's header comment: a `failed` task carrying a genuine StartedAt/
// SessionID/CompletedAt triple from its concluded first run, revived to
// `next` via a bare status-only Update (exactly what handleTaskPatch's plain
// PATCH {"status":"next"} — and pkg/tools/task.go's task_update tool —
// produce; neither path clears any of those three fields), must NOT be
// auto-dispatched by the heartbeat drain under an "ask" run_task policy.
func TestCheckQueuedTasks_AskPolicyRevivedFailedTaskStillGated(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)
	tk := newRunTaskGateRevivedFailedTask(t, al)

	// The revival: a bare status-only Update, exactly what a plain
	// PATCH {"status":"next"} (rest_tasks.go) or the task_update tool
	// (pkg/tools/task.go) apply — StartedAt/SessionID/CompletedAt are left
	// completely untouched, because neither caller's patch names them.
	next := task.StatusNext
	revived, err := al.taskStore.Update(tk.ID, task.Patch{Status: &next})
	if err != nil {
		t.Fatalf("revive failed->next: %v", err)
	}
	if revived.Status != task.StatusNext {
		t.Fatalf("setup: revived status = %q, want %q", revived.Status, task.StatusNext)
	}
	if revived.StartedAt == "" || revived.SessionID == "" || revived.CompletedAt == "" {
		t.Fatalf("setup: revival must leave StartedAt/SessionID/CompletedAt untouched (got %q/%q/%q) — "+
			"this is the exact stale-claim precondition the gate must catch",
			revived.StartedAt, revived.SessionID, revived.CompletedAt)
	}

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q after CheckQueuedTasks, want %q (unchanged) — a revived task carrying a "+
			"CONCLUDED prior run's StartedAt/SessionID must not be mistaken for an already-approved "+
			"continuation of the CURRENT run, and must not bypass the run_task ask-policy gate",
			got.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — a revived failed task must never reach the LLM "+
			"without a fresh, explicit run_task approval", calls)
	}
}

// --- 2. Regression guard: the legitimate goal-loop continuation is unaffected --

// TestExecuteTask_AskPolicyGoalLoopContinuationLeavesCompletedAtEmpty pins the
// OTHER half of the invariant this fix relies on: the goal loop's own
// in-flight retry write shape (an UpdateIfStatus CAS from in_progress to
// next, touching AttemptCount/Status only — exactly what
// consumeAttemptOrExhaust and rejectBareEvidenceClaim do) never stamps
// CompletedAt, so the new AND-clause does not accidentally re-gate a
// still-running goal loop. This is a narrower, write-shape-level companion to
// TestExecuteTask_AskPolicyRetryContinuesWithoutReapproval (which proves the
// end-to-end dispatch outcome); this one pins the specific field this fix
// added a dependency on.
func TestExecuteTask_AskPolicyGoalLoopContinuationLeavesCompletedAtEmpty(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)
	tk := newRunTaskGateTestTask(t, al, time.Now().UTC().Format(time.RFC3339), "prior-claim-session", "")

	// Mirror consumeAttemptOrExhaust's own CAS write shape exactly: it
	// transitions in_progress -> next touching ONLY AttemptCount and Status.
	inProgress := task.StatusInProgress
	if _, err := al.taskStore.Update(tk.ID, task.Patch{Status: &inProgress}); err != nil {
		t.Fatalf("setup: advance to in_progress: %v", err)
	}
	next := task.StatusNext
	attempt := 1
	continued, err := al.taskStore.UpdateIfStatus(tk.ID, task.StatusInProgress, task.Patch{
		AttemptCount: &attempt, Status: &next,
	})
	if err != nil {
		t.Fatalf("goal-loop-shaped continuation write: %v", err)
	}
	if continued.CompletedAt != "" {
		t.Fatalf("setup invariant broken: an in-flight goal-loop continuation stamped CompletedAt (%q) — "+
			"it must stay empty until a REAL terminal write (completeTaskWithResult/failTask)",
			continued.CompletedAt)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask on a genuine in-flight continuation must succeed even under an ask "+
			"policy, got: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — the legitimate continuation did not actually dispatch")
	}
}

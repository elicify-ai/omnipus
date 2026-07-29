// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_run_task_policy_gate_test.go covers the fix for a fail-open
// defect: a human DENYING an ask-gated run_task tool call
// (pkg/tools/run_task.go, gated in pkg/agent/loop.go's runTurn ask-policy
// branch) had NO durable effect on the underlying task's own eligibility for
// AUTOMATIC (unattended) dispatch — CheckQueuedTasks' ~60s heartbeat drain
// (and every other passive dispatch path funnelling through executeTask)
// never consulted the SAME tool-policy decision that gated the explicit
// call, so a denied standalone task simply ran anyway on the very next tick,
// regardless of what the human just said.
//
// The fix (task_executor.go: ErrRunTaskApprovalRequired,
// requireRunTaskAutoDispatchApproved, isRoutineAutoDispatchRefusal) mirrors
// the existing S1 plan-state-gate pattern (requirePlanExecuting): it moves
// the check down into executeTask, the ONE primitive every automatic
// dispatch path shares, and consults
// AgentLoop.ResolveApprovalToolPolicy(agentID, "run_task") — the same
// authoritative resolver the gateway's own WS approval hook and the runTurn
// ask-gate already resolve through.
//
// These tests are deliberately BLACK-BOX: they drive only pre-existing,
// package-private surface (ExecuteTask, StartTaskNow, CheckQueuedTasks,
// advanceBlockedTasks via onTaskComplete, real AgentConfig/AgentLoop wiring)
// — never the new requireRunTaskAutoDispatchApproved/
// ErrRunTaskApprovalRequired symbols directly. That means a mutation test
// that reverts ONLY the production change reproduces the ORIGINAL bug
// BEHAVIORALLY (a task that should have stayed queued actually dispatches
// and reaches the LLM), rather than merely failing to compile.
package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newRunTaskPolicyGateTestLoop mirrors newNativeTaskCompletionTestLoop
// (task_completion_contract_test.go) but sets an explicit run_task tool
// policy on the single test agent ("native-agent"), so these tests exercise
// the REAL tools.EffectiveToolPolicy resolution chain
// (AgentLoop.ResolveApprovalToolPolicy) for whichever policy the test under
// it needs to prove against — not a stub.
func newRunTaskPolicyGateTestLoop(t *testing.T, provider providers.LLMProvider, runTaskPolicy config.ToolPolicy) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: t.TempDir(), ModelName: "test-model"},
			List: []config.AgentConfig{
				{
					ID:   "native-agent",
					Name: "Native Agent",
					Type: config.AgentTypeWorker,
					Home: workspace,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{"run_task": runTaskPolicy},
						},
					},
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })
	return al
}

// newRunTaskGateTestTask creates and persists a dispatchable `next` task for
// "native-agent". startedAt seeds Task.StartedAt directly: empty simulates a
// task that has never been claimed before; non-empty simulates a task whose
// CURRENT run has already been claimed at least once (exactly what
// consumeAttemptOrExhaust/rejectBareEvidenceClaim leave behind when they flip
// a task back to `next` for one more goal-loop attempt — neither ever clears
// StartedAt). planID empty means standalone.
func newRunTaskGateTestTask(t *testing.T, al *AgentLoop, startedAt, planID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       "run_task policy gate test task",
		Prompt:      "do the task",
		Action:      task.ActionLLM,
		AgentID:     "native-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		StartedAt:   startedAt,
		PlanID:      planID,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tk
}

// --- 1. Reproduction: the reported defect -----------------------------------

// TestCheckQueuedTasks_AskPolicyStandaloneTaskNotAutoDispatched is the
// reproduction for the reported defect: a standalone task assigned to an
// agent whose run_task tool policy is "ask" must NOT be auto-dispatched by
// the heartbeat drain. This mirrors the real scenario end to end: a human
// denies an ask-gated run_task tool call (which never even reaches
// TaskRunTool.Execute — loop.go `continue`s past a denial), the task stays
// `next`, and the heartbeat's very next ~60s tick must not silently run it
// anyway.
func TestCheckQueuedTasks_AskPolicyStandaloneTaskNotAutoDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)
	tk := newRunTaskGateTestTask(t, al, "" /* never claimed */, "" /* standalone */)

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q after CheckQueuedTasks, want %q (unchanged) — an ask-policy agent's "+
			"standalone task must never auto-dispatch without an explicit, approved run_task call",
			got.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — the task must never reach the LLM without an "+
			"explicit run_task approval", calls)
	}
}

// --- 2. Regression guard: allow-policy dispatch is unaffected ---------------

// TestCheckQueuedTasks_AllowPolicyStandaloneTaskStillDispatched is the DoD's
// explicit regression guard: the common "allow" policy (no approval friction
// at all) must continue to auto-dispatch exactly as before this fix.
func TestCheckQueuedTasks_AllowPolicyStandaloneTaskStillDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAllow)
	tk := newRunTaskGateTestTask(t, al, "", "")

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status == task.StatusNext {
		t.Fatalf("status = %q immediately after CheckQueuedTasks, want it to have left %q — "+
			"an allow-policy agent's standalone task must still auto-dispatch", got.Status, task.StatusNext)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — an allow-policy task did not actually dispatch")
	}
}

// --- 3. Documented scope limitation: "deny" is deliberately NOT gated here --

// TestCheckQueuedTasks_DenyPolicyStandaloneTaskStillDispatched_KnownGap pins
// the DELIBERATE scope decision documented on ErrRunTaskApprovalRequired:
// this gate only ever fires for policy=="ask", never "deny". A "deny" policy
// never produces a human denial EVENT in the first place (the tool call
// fails fast with no approval prompt at all, so there is nothing analogous
// to "the denial must stick" to enforce), and gating on bare policy!="allow"
// would misfire on every test harness in this package that never configures
// tool-policy coverage at all (tools.EffectiveToolPolicy's documented
// fail-closed default for a missing-coverage gap is ALSO "deny", logged at
// Error) — a real production "deny" is structurally indistinguishable from
// that artifact at this call site. This test is a deliberate, named residual
// -risk pin, not an oversight: if it starts failing, either the gate widened
// to also cover "deny" (update this test to match) or something else
// regressed.
func TestCheckQueuedTasks_DenyPolicyStandaloneTaskStillDispatched_KnownGap(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyDeny)
	tk := newRunTaskGateTestTask(t, al, "", "")

	al.taskExecutor.CheckQueuedTasks(context.Background())

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status == task.StatusNext {
		t.Fatalf("status = %q immediately after CheckQueuedTasks; this test expects it to have left "+
			"%q — a deny-policy agent's standalone task is a KNOWN, documented gap this fix does not "+
			"close (see ErrRunTaskApprovalRequired's doc comment)", got.Status, task.StatusNext)
	}
}

// --- 4. Continuation carve-out: an in-flight retry is not re-gated ---------

// TestExecuteTask_AskPolicyRetryContinuesWithoutReapproval proves the
// requireRunTaskAutoDispatchApproved carve-out (StartedAt != ""): a task
// whose CURRENT run has already been claimed at least once (simulated here
// by seeding StartedAt directly — exactly what
// consumeAttemptOrExhaust/rejectBareEvidenceClaim leave behind when they
// flip a task back to `next` for one more attempt; neither ever clears
// StartedAt) must dispatch via ExecuteTask WITHOUT re-consulting the
// run_task ask policy, even though the agent's policy is "ask". Re-gating
// every retry would silently break any multi-attempt task for an ask-policy
// agent even though a human (or an approved run_task call) already
// authorized the run in the first place.
func TestExecuteTask_AskPolicyRetryContinuesWithoutReapproval(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)
	tk := newRunTaskGateTestTask(t, al, time.Now().UTC().Format(time.RFC3339), "")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask on an already-started (retry) task must succeed even under an ask "+
			"policy, got: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := scriptedProviderCallCount(provider); calls == 0 {
		t.Fatal("provider was never called — the retry did not actually dispatch")
	}
}

// --- 5. Plan members are exempt: run_task policy never governs them --------

// TestExecuteTask_AskPolicyPlanMemberNotGatedByRunTaskPolicy proves the gate
// is scoped to standalone tasks only: a plan member (PlanID != "") dispatched
// via ExecuteTask must be entirely unaffected by the agent's run_task
// policy — plan members are governed by requirePlanExecuting / execute_plan's
// own approval flow, and run_task itself refuses any task naming a PlanID
// (pkg/tools/run_task.go's G4/A3 check), so its tool policy is simply
// irrelevant to a plan member's dispatch.
func TestExecuteTask_AskPolicyPlanMemberNotGatedByRunTaskPolicy(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	al.taskExecutor.SetPlanStore(planStore)
	p := &plan.Plan{
		Title: "run_task gate test plan", WorkspaceID: "default",
		OwnerAgentID: "native-agent", State: plan.StateRunning,
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	tk := newRunTaskGateTestTask(t, al, "", p.ID)

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask on a RUNNING plan's member must succeed regardless of the agent's "+
			"run_task policy, got: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
}

// --- 6. Explicit dispatch (an approved run_task call) is never re-gated ----

// TestStartTaskNow_AskPolicyTaskDispatchesDirectly proves StartTaskNow — the
// primitive TaskRunTool.Execute calls, reached ONLY after loop.go's
// ask-policy gate already approved the exact tool call — is not re-gated by
// requireRunTaskAutoDispatchApproved. This is DoD item 2: an APPROVED
// run_task call must still dispatch normally even though the agent's static
// policy is "ask" (approving one call does not change the policy setting
// itself, so re-checking policy here would incorrectly bounce every approved
// call straight back).
func TestStartTaskNow_AskPolicyTaskDispatchesDirectly(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)
	tk := newRunTaskGateTestTask(t, al, "", "")

	// StartTaskNow's contract: the caller has already transitioned the task
	// to in_progress via a PATCH before calling it (mirrors the REST handler
	// and pkg/tools/run_task.go's own Execute, and this package's existing
	// StartTaskNow tests, e.g. task_executor_plan_gate_test.go's
	// TestStartTaskNow_StandaloneTaskStillDispatches).
	inProgress, err := al.taskStore.Update(tk.ID, task.Patch{Status: ptrStatus(task.StatusInProgress)})
	if err != nil {
		t.Fatalf("advance to in_progress: %v", err)
	}

	sessID, err := al.taskExecutor.StartTaskNow(context.Background(), inProgress.ID)
	if err != nil {
		t.Fatalf("StartTaskNow (an approved run_task call's own dispatcher) must succeed under an "+
			"ask policy, got: %v", err)
	}
	if sessID == "" {
		t.Fatal("StartTaskNow must return a non-empty session id")
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
}

// --- 7. Other automatic dispatch paths are equally gated --------------------

// TestAdvanceBlockedTasks_AskPolicyStandaloneTaskNotDispatched proves the
// gate covers advanceBlockedTasks too (reached via onTaskComplete when a
// dependency finishes) — not merely CheckQueuedTasks. Both funnel through
// the SAME executeTask primitive, so this is mostly a belt-and-braces proof
// that the shared placement actually reaches this caller, mirroring
// task_executor_plan_gate_test.go's own analogous coverage for the plan-state
// gate.
func TestAdvanceBlockedTasks_AskPolicyStandaloneTaskNotDispatched(t *testing.T) {
	provider := &scriptedProvider{responseBody: successMarkerBody}
	al := newRunTaskPolicyGateTestLoop(t, provider, config.ToolPolicyAsk)

	dep := newRunTaskGateTestTask(t, al, "", "")
	now := time.Now().UTC().Format(time.RFC3339)
	claimedDep, err := al.taskStore.Update(dep.ID, task.Patch{
		Status: ptrStatus(task.StatusInProgress), StartedAt: &now,
	})
	if err != nil {
		t.Fatalf("claim dep in_progress: %v", err)
	}

	blocked := newRunTaskGateTestTask(t, al, "", "")
	blockedByDep := []string{dep.ID}
	if _, blockErr := al.taskStore.Update(blocked.ID, task.Patch{BlockedBy: &blockedByDep}); blockErr != nil {
		t.Fatalf("set blocked_by: %v", blockErr)
	}
	gotBlocked, err := al.taskStore.Get(blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotBlocked.Status != task.StatusBlocked {
		t.Fatalf("setup: blocked task status = %q, want %q (unmet dep at recompute time)",
			gotBlocked.Status, task.StatusBlocked)
	}

	doneDep, err := al.taskStore.Update(claimedDep.ID, task.Patch{
		Status: ptrStatus(task.StatusDone), CompletedAt: &now,
	})
	if err != nil {
		t.Fatalf("complete dep: %v", err)
	}

	al.taskExecutor.onTaskComplete(doneDep)

	final, err := al.taskStore.Get(blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == task.StatusInProgress {
		t.Fatalf("status = %q, must NOT be in_progress — an ask-policy agent's now-unblocked "+
			"standalone task must not auto-dispatch via advanceBlockedTasks either", final.Status)
	}
	if final.Status != task.StatusNext {
		t.Fatalf("status = %q, want %q (AdvanceBlockedDependents promotes it policy-agnostically, but "+
			"the run_task gate inside ExecuteTask must then refuse to dispatch it)",
			final.Status, task.StatusNext)
	}
	if calls := scriptedProviderCallCount(provider); calls != 0 {
		t.Fatalf("provider was called %d time(s) — the now-unblocked task must never reach the LLM "+
			"without an explicit run_task approval", calls)
	}
}

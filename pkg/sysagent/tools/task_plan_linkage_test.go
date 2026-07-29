// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// ADR-052 FR-002 (task<->plan linkage): create_task_in_workspace gains an
// optional plan_id arg, mirroring the plain create_task tool
// (pkg/tools/plan_test.go). These tests prove the same-workspace FK, the
// terminal-plan rejection, the fail-closed-when-unwired discipline, and that
// an ordinary (no plan_id) call is unaffected.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/plan"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// seedPlanForLinkage creates a plan.Store rooted under home/plans and seeds
// a single draft plan in wsID, returning the store and the plan's ID.
func seedPlanForLinkage(t *testing.T, home, wsID string) (*plan.Store, string) {
	t.Helper()
	store := plan.New(home + "/plans")
	p := &plan.Plan{Title: "Linkage Plan", WorkspaceID: wsID, OwnerAgentID: "jim", CreatedBy: "jim"}
	require.NoError(t, store.Create(p))
	return store, p.ID
}

// TestCreateTaskInWorkspace_PlanLinkage_Happy proves plan_id links a new
// cross-workspace task to a plan in the same workspace.
func TestCreateTaskInWorkspace_PlanLinkage_Happy(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	planStore, planID := seedPlanForLinkage(t, home, testWorkspaceID)
	deps.PlanStore = planStore

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "member task",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"plan_id":      planID,
		"criteria":     workspaceCriteriaArg(),
	})
	require.False(t, result.IsError, "create_task_in_workspace with plan_id: %s", result.ForLLM)

	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &out))

	taskStore := task.New(home + "/tasks")
	got, err := taskStore.Get(out.ID)
	require.NoError(t, err)
	assert.Equal(t, planID, got.PlanID)
}

// TestCreateTaskInWorkspace_PlanLinkage_CrossWorkspaceRejected proves a
// plan_id naming a plan in a DIFFERENT workspace is rejected.
func TestCreateTaskInWorkspace_PlanLinkage_CrossWorkspaceRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	seedWorkspace(t, home, "other-workspace-0001")
	planStore, planID := seedPlanForLinkage(t, home, "other-workspace-0001")
	deps.PlanStore = planStore

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "member task",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"plan_id":      planID,
		"criteria":     workspaceCriteriaArg(),
	})
	assert.True(t, result.IsError, "expected rejection for a cross-workspace plan_id")
}

// TestCreateTaskInWorkspace_PlanLinkage_TerminalPlanRejected proves linking
// to a terminal (done) plan is rejected.
func TestCreateTaskInWorkspace_PlanLinkage_TerminalPlanRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	planStore, planID := seedPlanForLinkage(t, home, testWorkspaceID)
	deps.PlanStore = planStore

	approved := plan.StateApproved
	running := plan.StateRunning
	done := plan.StateDone
	_, err := planStore.Update(planID, plan.Patch{State: &approved})
	require.NoError(t, err)
	_, err = planStore.Update(planID, plan.Patch{State: &running})
	require.NoError(t, err)
	_, err = planStore.Update(planID, plan.Patch{State: &done})
	require.NoError(t, err)

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "member task",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"plan_id":      planID,
		"criteria":     workspaceCriteriaArg(),
	})
	assert.True(t, result.IsError, "expected rejection for a terminal plan")
}

// TestCreateTaskInWorkspace_PlanLinkage_NonDraftPlanRejected is the
// create_task_in_workspace-surface regression test for the 5th run_task
// ask-policy gate bypass (this is the sysagent-tool sibling of
// pkg/tools/plan_test.go's TestTaskCreate_PlanLinkage_NonDraftPlanRejected —
// see validateTaskPlanLinkageWorkspace's doc comment in task.go for the full
// writeup). create_task_in_workspace is an agent tool — reachable via the
// privileged cross-workspace/Orchestrator surface — with no human review
// step equivalent to the REST/Kanban UI's "attach, then explicitly click Run
// Now" flow. Before this fix, a single call could mint a brand-new,
// already-`next`, plan-linked task for ANY agent (including one with an
// "ask" run_task policy) naming ANY plan already past draft (Approved,
// Running, or Running-paused): pkg/agent/task_executor.go's
// requireRunTaskAutoDispatchApproved exempts EVERY task naming a non-empty
// PlanID from the ask-policy check unconditionally, so the very next
// unattended dispatch tick would run the grafted task with no approval and
// no log trace. validateTaskPlanLinkageWorkspace now requires the named plan
// to still be StateDraft, mirroring validateTaskPlanLinkage exactly.
func TestCreateTaskInWorkspace_PlanLinkage_NonDraftPlanRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	planStore, planID := seedPlanForLinkage(t, home, testWorkspaceID)
	deps.PlanStore = planStore

	approved := plan.StateApproved
	_, err := planStore.Update(planID, plan.Patch{State: &approved})
	require.NoError(t, err)

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "opportunistic member",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"plan_id":      planID,
		"criteria":     workspaceCriteriaArg(),
	})
	require.True(t, result.IsError, "expected rejection for a plan that has already left draft (approved); got: %s", result.ForLLM)

	taskStore := task.New(home + "/tasks")
	all, err := taskStore.List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	assert.Empty(t, all, "no task should have been persisted for the rejected create — this is the "+
		"attach half of the reproduction: nothing must reach disk carrying the forged PlanID for the "+
		"unattended heartbeat (requireRunTaskAutoDispatchApproved's unconditional PlanID!=\"\" exemption, "+
		"pkg/agent/task_executor.go) to later auto-dispatch with no run_task approval ever consulted")
}

// TestCreateTaskInWorkspace_PlanLinkage_RunningPlanRejected extends the
// non-draft rejection to the concrete attack shape described in the bug
// report: attaching to a plan that is ALREADY Executing (StateRunning, i.e.
// PermitsMemberDispatch()==true, not merely Approved-but-not-yet-running).
func TestCreateTaskInWorkspace_PlanLinkage_RunningPlanRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	planStore, planID := seedPlanForLinkage(t, home, testWorkspaceID)
	deps.PlanStore = planStore

	approved := plan.StateApproved
	running := plan.StateRunning
	_, err := planStore.Update(planID, plan.Patch{State: &approved})
	require.NoError(t, err)
	_, err = planStore.Update(planID, plan.Patch{State: &running})
	require.NoError(t, err)

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "opportunistic plan-graft attack",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"plan_id":      planID,
		"criteria":     workspaceCriteriaArg(),
	})
	require.True(t, result.IsError, "expected rejection for attaching to an already-Executing (running) plan; got: %s", result.ForLLM)

	taskStore := task.New(home + "/tasks")
	all, err := taskStore.List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	assert.Empty(t, all, "no task should have been persisted — the attach must fail before the task "+
		"ever acquires the exempting PlanID")
}

// TestCreateTaskInWorkspace_PlanLinkage_UnwiredStore_FailsClosed proves a
// plan_id arg with no PlanStore wired fails closed.
func TestCreateTaskInWorkspace_PlanLinkage_UnwiredStore_FailsClosed(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	// deps.PlanStore intentionally left nil.

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "member task",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"plan_id":      "some-plan-id",
		"criteria":     workspaceCriteriaArg(),
	})
	assert.True(t, result.IsError, "expected fail-closed rejection when the plan store is unwired")
}

// TestCreateTaskInWorkspace_NoPlanID_Unaffected proves an ordinary call with
// no plan_id is entirely unaffected by the plan store being unwired.
func TestCreateTaskInWorkspace_NoPlanID_Unaffected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	// deps.PlanStore intentionally left nil.

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "no plan task",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"criteria":     workspaceCriteriaArg(),
	})
	assert.False(t, result.IsError, "plan-less create_task_in_workspace must be unaffected by an unwired plan store: %s", result.ForLLM)
}

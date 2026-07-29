// rest_tasks_plan_attach_ask_policy_bypass_test.go — REST-level regression
// test for the 4th bypass of pkg/agent/task_executor.go's
// requireRunTaskAutoDispatchApproved run_task ask-policy gate (code review
// finding, follows the StartedAt/SessionID pair and the CompletedAt fixes
// covered by rest_tasks_run_task_gate_test.go and
// rest_tasks_run_task_revival_bypass_test.go).
//
// requireRunTaskAutoDispatchApproved exempts EVERY task naming a non-empty
// PlanID from the ask-policy check outright, on the premise that "being a
// plan member" is itself an act of authorization (a human clicked Execute,
// or plan_correct's own supervisor-only gate ran). That premise held only as
// long as Task.PlanID could only be acquired through the plan engine's own
// vetted flows. It broke the moment a task could acquire a NEW PlanID
// through an ordinary, generic write path: validateTaskPlanID
// (pkg/gateway/rest_tasks.go, used by both POST /tasks and
// PATCH /tasks/{id}) only ever checked the same-workspace FK — never
// anything about the caller's relationship to the plan, or whether this
// specific task was ever vetted as one of its members.
//
// Attack: agent has run_task: ask; a standalone `next` task assigned to that
// agent sits with PlanID=="". Any caller with ordinary task-write REST
// access sends PATCH /api/v1/tasks/{id} with {"plan_id": "<an already-
// Executing plan's ID>"}. Before the fix, this succeeded unconditionally
// (200), and the very next CheckQueuedTasks heartbeat tick (or the plan
// engine's own Tick) would auto-dispatch it with NO approval and NO log
// trace — requireRunTaskAutoDispatchApproved's PlanID != "" exemption never
// even consults the policy.
//
// Fix: validateTaskPlanID now rejects the attach (400) when the named plan
// has already left `draft` (i.e. it already permits, or will imminently
// permit, autonomous member dispatch) AND the task's assigned agent's
// resolved run_task policy is "ask". Attaching while the plan is still draft
// remains unrestricted (nothing to gate — draft never auto-dispatches), and
// attaching a NON-ask-policy agent's task to a live plan remains unrestricted
// (unchanged — see TestHandleTaskPatch_InProgress_ApprovedPlanMember_
// StillLaunches, rest_tasks_plan_gate_test.go, same package).
package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestHandleTaskPatch_PlanAttach_AskPolicyAgent_RejectedOnRunningPlan is the
// end-to-end reproduction/regression test for the bypass described in this
// file's header comment.
//
// BDD:
//
//	Given a standalone `next` task assigned to an agent whose run_task
//	  policy is "ask", and a plan already State==running in the same
//	  workspace,
//	When PATCH /api/v1/tasks/{id} is sent with {"plan_id": "<the running
//	  plan's ID>"},
//	Then the PATCH is rejected (400) and plan_id is never persisted;
//	And when the next CheckQueuedTasks tick runs,
//	Then the task must still NOT have auto-dispatched (status still `next`,
//	  no session ever created) — the attach never succeeded, so there is
//	  nothing for the PlanID exemption to (mis)fire on.
func TestHandleTaskPatch_PlanAttach_AskPolicyAgent_RejectedOnRunningPlan(t *testing.T) {
	api := newTestRestAPIWithAskPolicyAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{runTaskGateTestAgentID})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateRunning)

	// The attack precondition: an ordinary standalone `next` task assigned to
	// the ask-policy agent — exactly what an attacker with plain task-write
	// access would produce, no plan involvement yet.
	tsk := createTaskViaAPI(t, api, "opportunistic plan-graft attack", wsID)
	advanceTaskToNext(t, api, tsk.Id)
	wAgent := patchTask(t, api, tsk.Id, `{"agent_id":"`+runTaskGateTestAgentID+`"}`)
	require.Equal(t, http.StatusOK, wAgent.Code, "assign agent_id must succeed; body=%s", wAgent.Body.String())

	// The attack: attach this standalone `next` task onto the ALREADY-
	// Executing plan.
	wAttach := patchTask(t, api, tsk.Id, `{"plan_id":"`+p.ID+`"}`)
	assert.Equal(t, http.StatusBadRequest, wAttach.Code,
		"attaching an ask-policy agent's task to an already-running plan must be rejected; body=%s",
		wAttach.Body.String())

	afterAttack, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Empty(t, afterAttack.PlanID, "the rejected attach must not have persisted plan_id")
	assert.Equal(t, task.StatusNext, afterAttack.Status, "the rejected attach must not have changed status")

	// Strongest possible proof: drive the SAME unattended dispatch path the
	// real heartbeat drives, and confirm nothing runs.
	api.taskExecutor.CheckQueuedTasks(context.Background())

	final, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, final.Status,
		"task must remain queued at next — no unapproved unattended dispatch must occur")
	assert.Empty(t, final.SessionID, "no session must have been created — the task was never dispatched")
}

// TestHandleTaskPatch_PlanAttach_AskPolicyAgent_AllowedWhileDraft proves the
// fix is not over-broad on the "author before Execute" half: attaching an
// ask-policy agent's task to a DRAFT plan is entirely unaffected — draft
// never auto-dispatches (PermitsMemberDispatch is false), so there is
// nothing to gate yet, and this is the plan engine's own supported flow for
// authoring members before a human clicks Execute.
func TestHandleTaskPatch_PlanAttach_AskPolicyAgent_AllowedWhileDraft(t *testing.T) {
	api := newTestRestAPIWithAskPolicyAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{runTaskGateTestAgentID})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)

	tsk := createTaskViaAPI(t, api, "legit draft member", wsID)
	advanceTaskToNext(t, api, tsk.Id)
	wAgent := patchTask(t, api, tsk.Id, `{"agent_id":"`+runTaskGateTestAgentID+`"}`)
	require.Equal(t, http.StatusOK, wAgent.Code, "assign agent_id must succeed; body=%s", wAgent.Body.String())

	wAttach := patchTask(t, api, tsk.Id, `{"plan_id":"`+p.ID+`"}`)
	assert.Equal(t, http.StatusOK, wAttach.Code,
		"attaching to a DRAFT plan must succeed regardless of the assigned agent's run_task policy; body=%s",
		wAttach.Body.String())

	got, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.PlanID, "task must be attached to the draft plan")
}

// TestCheckQueuedTasks_GenuinePlanMember_AskPolicyAgent_StillAutoDispatched
// proves the fix preserves the legitimate case end to end, through the exact
// mechanism the bypass abused: a task attached to a plan WHILE IT WAS STILL
// DRAFT (the plan engine's own vetted "author before Execute" path), for an
// agent whose run_task policy is "ask", must still auto-dispatch via the
// unattended heartbeat once the plan is admitted (draft->approved->running,
// mirroring a real Execute click) — with NO run_task approval ever
// consulted. Closing the bypass must never also wedge a real plan's
// genuinely-authored ask-policy-agent member.
func TestCheckQueuedTasks_GenuinePlanMember_AskPolicyAgent_StillAutoDispatched(t *testing.T) {
	api := newTestRestAPIWithAskPolicyAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{runTaskGateTestAgentID})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)

	tsk := createTaskViaAPI(t, api, "genuine draft-authored member", wsID)
	advanceTaskToNext(t, api, tsk.Id)
	wAgent := patchTask(t, api, tsk.Id, `{"agent_id":"`+runTaskGateTestAgentID+`"}`)
	require.Equal(t, http.StatusOK, wAgent.Code, "assign agent_id must succeed; body=%s", wAgent.Body.String())
	wAttach := patchTask(t, api, tsk.Id, `{"plan_id":"`+p.ID+`"}`)
	require.Equal(t, http.StatusOK, wAttach.Code, "attach while draft must succeed; body=%s", wAttach.Body.String())

	// Admit the plan (draft -> approved -> running), exactly like a real
	// Execute click — entirely independent of the REST plan_id validator
	// under test.
	drivePlanToRunning(t, api, p.ID)

	// The SAME unattended path the reported bypass abused.
	api.taskExecutor.CheckQueuedTasks(context.Background())

	taskID := tsk.Id
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			s := getTaskStatus(t, api, taskID)
			return s == gen.TaskStatusDone || s == gen.TaskStatusFailed
		}, 10*time.Second, 20*time.Millisecond, "genuine plan member must reach a terminal state before teardown")
	})

	require.Eventually(t, func() bool {
		got, err := api.taskStore.Get(taskID)
		return err == nil && got.SessionID != ""
	}, 2*time.Second, 20*time.Millisecond,
		"genuine plan member must have been dispatched (session created) with no run_task approval ever consulted")
}

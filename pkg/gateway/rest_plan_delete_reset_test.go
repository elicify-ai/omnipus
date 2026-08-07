// rest_plan_delete_reset_test.go — S1 release-blocker regression coverage
// (UAT round 2): DELETE /api/v1/plans/{id} used to clear plan_id on every
// former member task and change nothing else, so a `next` member came out
// of the cascade standalone AND still `next` — exactly the state the ~60s
// heartbeat drain (TaskExecutor.CheckQueuedTasks, task.Filter{Status: next})
// auto-dispatches. That laundered an unapproved/cancelled plan member into
// an autonomously-executed standalone task, bypassing the Execute approval
// gate (5d77f26a) entirely. See handlePlanDelete's and
// detachMemberOnPlanDelete's doc comments (rest_plans.go) for the full fix
// rationale.
//
// Reproduces both UAT variants:
//   - Variant A: a never-executed DRAFT plan, member triaged to `next`,
//     plan Cleared (deleted) via the UI's ⋯ menu.
//   - Variant B (worse): a plan the user explicitly Stopped
//     (failed/stopped_by_user), members left `next` by the direct Stop path
//     (which correctly holds them), then Cleared.
//
// Traces to: pkg/gateway/rest_plans.go's handlePlanDelete +
// detachMemberOnPlanDelete.
package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestPlanDelete_DraftPlan_NextMember_DetachedAndReset_NotDispatched is the
// Variant A regression: DELETE on a never-executed DRAFT plan must reset its
// `next` member to `inbox` (not leave it standalone-and-`next`), and a
// subsequent drain pass (CheckQueuedTasks) must not dispatch it.
func TestPlanDelete_DraftPlan_NextMember_DetachedAndReset_NotDispatched(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "DraftMember", wsID, p.ID)

	// Precondition: member is `next`, carries the plan's ID, plan never
	// approved/executed (progress 0, State draft).
	require.Equal(t, gen.TaskStatusNext, getTaskStatus(t, api, tsk.Id))

	wDel := deletePlan(t, api, p.ID)
	require.Equal(t, 204, wDel.Code, "delete draft plan must succeed; body=%s", wDel.Body.String())

	// THE FIX under test: the member must be detached AND reset to inbox in
	// the same cycle — never left standalone-and-next. This assertion fails
	// on the pre-fix code, which only cleared plan_id and left status alone.
	after := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusInbox, after,
		"a next member of a deleted plan must be reset to inbox, not left standalone-and-next (S1 laundering bug)")

	reloaded, gErr := api.taskStore.Get(tsk.Id)
	require.NoError(t, gErr)
	assert.Empty(t, reloaded.PlanID, "plan_id must be cleared on the former member")

	// Strongest possible proof of no auto-dispatch: a real drain pass must
	// not even pick this task up (task.Filter{Status: next} no longer
	// matches it once reset to inbox).
	api.taskExecutor.CheckQueuedTasks(context.Background())
	postDrain, gErr2 := api.taskStore.Get(tsk.Id)
	require.NoError(t, gErr2)
	assert.Equal(t, task.StatusInbox, postDrain.Status,
		"a full drain pass must not dispatch the detached member (status must remain inbox)")
	assert.Empty(t, postDrain.SessionID, "no session must have been created — task was never dispatched")
}

// TestPlanDelete_StoppedPlan_NextMembers_NoneBecomeRunnable is the Variant B
// regression (worse than A): a plan the user explicitly Stopped
// (failed/stopped_by_user) can still have members sitting in `next` — the
// direct Stop path correctly holds them there rather than dispatching them.
// Deleting that plan must not resurrect that cancelled work into runnable
// standalone tasks either.
func TestPlanDelete_StoppedPlan_NextMembers_NoneBecomeRunnable(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	// Attach the members while the plan is still draft, THEN Stop it — the
	// 4th-run_task-ask-policy-bypass fix (validateTaskPlanID's terminal-plan
	// check, parity with pkg/tools/plan.go's validateTaskPlanLinkage) now
	// rejects attaching a NEW member to an ALREADY-terminal plan, so members
	// must exist (exactly as they would in production — the direct Stop path
	// acts on members that are already there) before the plan reaches its
	// terminal failed(stopped_by_user) state.
	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	var members []gen.Task
	for i := 0; i < 3; i++ {
		tsk := assignPlanMemberTask(t, api, fmt.Sprintf("StoppedMember%d", i), wsID, p.ID)
		require.Equal(t, gen.TaskStatusNext, getTaskStatus(t, api, tsk.Id))
		members = append(members, tsk)
	}
	drivePlanToFailed(t, api, p.ID, plan.FailedReasonStoppedByUser)

	wDel := deletePlan(t, api, p.ID)
	require.Equal(t, 204, wDel.Code, "delete stopped plan must succeed; body=%s", wDel.Body.String())

	for _, m := range members {
		after := getTaskStatus(t, api, m.Id)
		assert.Equal(t, gen.TaskStatusInbox, after,
			"member %s of a Stopped-then-deleted plan must be reset to inbox, never left standalone-and-next", m.Id)
	}

	// A full drain pass afterward must resurrect none of the cancelled work.
	api.taskExecutor.CheckQueuedTasks(context.Background())
	for _, m := range members {
		reloaded, gErr := api.taskStore.Get(m.Id)
		require.NoError(t, gErr)
		assert.Equal(t, task.StatusInbox, reloaded.Status,
			"member %s must not be dispatched by a drain pass after the stopped plan was deleted", m.Id)
		assert.Empty(t, reloaded.SessionID)
	}
}

// TestPlanDelete_TerminalAndBlockedMembers_HandledCorrectly proves the fix is
// not over-broad: a `done` member's status/history must never be rewritten,
// and a `blocked` member (a store-DERIVED side-state that can only be left
// via the store's own internal recompute hatch, never a public Status patch)
// must be left for recomputeBlockedStateLocked to resolve rather than
// hand-written by the REST layer — in both cases plan_id must still be
// cleared.
func TestPlanDelete_TerminalAndBlockedMembers_HandledCorrectly(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	planStore := wirePlanStore(t, api)
	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	planID := p.ID

	// `done` member.
	doneTask := createTaskViaAPI(t, api, "DoneMember", wsID)
	_, uerr := api.taskStore.Update(doneTask.Id, task.Patch{PlanID: &planID})
	require.NoError(t, uerr)
	doneStatus := task.StatusDone
	_, uerr = api.taskStore.Update(doneTask.Id, task.Patch{Status: &doneStatus})
	require.NoError(t, uerr)

	// `blocked` member: still has an unmet blocker, so it must remain
	// blocked (not resurrected to inbox) after detach.
	blockerTask := createTaskViaAPI(t, api, "BlockerMember", wsID)
	blockedTaskID := "01JXBLOCKEDMEMBERTASK00001"
	seedBlockedTask(t, api, blockedTaskID, "BlockedMember", wsID, []string{blockerTask.Id})
	_, uerr = api.taskStore.Update(blockedTaskID, task.Patch{PlanID: &planID})
	require.NoError(t, uerr)

	wDel := deletePlan(t, api, p.ID)
	require.Equal(t, 204, wDel.Code, "body=%s", wDel.Body.String())

	reloadedDone, gErr := api.taskStore.Get(doneTask.Id)
	require.NoError(t, gErr)
	assert.Equal(t, task.StatusDone, reloadedDone.Status, "a done member's status must never be rewritten")
	assert.Empty(t, reloadedDone.PlanID, "plan_id must still be cleared on a terminal member")

	reloadedBlocked, gErr := api.taskStore.Get(blockedTaskID)
	require.NoError(t, gErr)
	assert.Equal(t, task.StatusBlocked, reloadedBlocked.Status,
		"a blocked member with a still-unmet blocker must remain blocked, not be hand-written to inbox")
	assert.Empty(t, reloadedBlocked.PlanID, "plan_id must still be cleared on a blocked member")
}

// Sibling of rest_plan_delete_reset_test.go. That file covers laundering via
// DELETE /plans/{id}; this one covers the SECOND door found in UAT round 2 —
// PATCH /api/v1/tasks/{id} with {"plan_id": ""}.
//
// Detaching a member without touching its status turned a `next` member of a
// draft/stopped plan into a STANDALONE `next` task. requirePlanExecuting
// rightly permits that (PlanID == "" means "not a plan member"), so
// CheckQueuedTasks' ~60s drain then auto-dispatched work the Execute gate never
// approved. Reachable from the UI: TaskDetailPanel's Plan dropdown -> "No plan"
// sends exactly this patch (src/components/command-center/TaskDetailPanel.tsx).
//
// Traces to: pkg/gateway/rest_tasks.go handleTaskPatch plan-detach branch.

package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

// TestTaskDetach_NextMemberOfDraftPlan_ResetToInbox is the core regression:
// the exact two-step a user performs in the UI (member of an unapproved plan →
// "No plan"). Before the fix the task came out standalone AND `next`, which is
// precisely what the heartbeat drain dispatches.
func TestTaskDetach_NextMemberOfDraftPlan_ResetToInbox(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "detach me", wsID, p.ID)
	// Read the precondition from the store rather than the returned DTO: this
	// must fail loudly if the fixture ever stops actually attaching the task,
	// otherwise the test below would "pass" against a task that was never a
	// plan member and prove nothing.
	before, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	require.Equal(t, p.ID, before.PlanID, "fixture: task must start attached to the plan")
	require.Equal(t, "next", string(before.Status), "fixture: member must start `next`")

	w := patchTask(t, api, tsk.Id, `{"plan_id":""}`)
	require.Equal(t, http.StatusOK, w.Code, "detach must succeed; body=%s", w.Body.String())

	got, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Empty(t, got.PlanID, "plan_id must be cleared")
	assert.Equal(t, "inbox", string(got.Status),
		"a detached non-terminal member must return to triage, NOT stay `next` "+
			"(a standalone `next` task is auto-dispatched by the drain, which is "+
			"exactly the Execute-gate bypass this closes)")
	assert.Empty(t, got.SessionID, "detaching must never start a session")
}

// TestTaskDetach_ExplicitStatusWins proves the reset only applies when the
// caller did NOT set a status itself. Setting both is an explicit, deliberate
// "make this a standalone runnable task" — the same thing creating a standalone
// task and triaging it to `next` does, and not a plan-member bypass.
func TestTaskDetach_ExplicitStatusWins(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "explicit status", wsID, p.ID)

	w := patchTask(t, api, tsk.Id, `{"plan_id":"","status":"next"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Empty(t, got.PlanID)
	assert.Equal(t, "next", string(got.Status),
		"an explicitly-requested status must win over the detach reset")
}

// TestTaskDetach_ReparentDoesNotResetStatus proves moving a member from plan A
// to plan B is untouched — only clearing to "" resets. Re-parenting a `next`
// member must keep it `next` so the destination plan can dispatch it.
func TestTaskDetach_ReparentDoesNotResetStatus(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	pA := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	pB := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "reparent", wsID, pA.ID)

	w := patchTask(t, api, tsk.Id, `{"plan_id":"`+pB.ID+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Equal(t, pB.ID, got.PlanID, "task must be re-parented to plan B")
	assert.Equal(t, "next", string(got.Status),
		"re-parenting must NOT reset status — plan B still needs to dispatch it")
}

// TestTaskDetach_TerminalMemberStillDetaches proves the optimistic
// `status: inbox` never turns a legitimate detach into a 400. `done` is frozen,
// so the store refuses the combined patch; the retry must apply the plan_id
// clear alone and leave history intact.
func TestTaskDetach_TerminalMemberStillDetaches(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "finished work", wsID, p.ID)

	wDone := patchTask(t, api, tsk.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wDone.Code, "fixture: mark done; body=%s", wDone.Body.String())

	w := patchTask(t, api, tsk.Id, `{"plan_id":""}`)
	require.Equal(t, http.StatusOK, w.Code,
		"detaching a terminal member must still succeed, not 400; body=%s", w.Body.String())

	got, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Empty(t, got.PlanID, "plan_id must be cleared")
	assert.Equal(t, "done", string(got.Status),
		"a terminal member's history must never be rewritten by the detach reset")
}

// rest_task_restart_domain_test.go — ADR-052 Fix-Wave-2 agent G tests
// (§6.7/§6.8, spec FR-019/FR-025/FR-026). Split into its own file rather
// than rest_plan_task_restart_test.go (owned by a different fix agent in
// this wave) to keep concurrent edits disjoint.
//
// Covers:
//   - task.ValidateStandaloneRestart is exercised end-to-end through the
//     handler (regression: the 409/200 outcomes are unchanged after the
//     gate was centralized into pkg/task — see rest_tasks.go's
//     handleTaskRestart and pkg/task/store.go's ValidateStandaloneRestart).
//   - the failed->in_progress PATCH transition (the SPA's ▶ "Run" button on
//     a standalone task, board-drag path) stays legal — a pinning
//     regression test so pkg/task's permissive-except-two-invariants
//     transition policy (validateTransition, store.go) can't silently
//     start rejecting it. Ground truth: validateTransition only freezes
//     `done` and gates `blocked` entry/exit; failed->in_progress hits
//     neither guard and has always been legal — the ▶ Run button on a
//     genuinely-failed standalone task was never a dead button.
//
// Traces to: docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md
//            docs/internal/specs/autonomous-agent-plan-execution-spec.md

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestTaskPatch_FailedToInProgress_Legal pins the ▶ Run route for a
// GENUINELY-failed standalone task (status=failed, no cancel_reason — e.g.
// attempts exhausted): PATCH {"status":"in_progress"} must succeed (200),
// distinct from the reason-gated POST /tasks/{id}/restart route (which
// 409s for this exact task — see TestTaskRestart_WrongReasonRejected409 in
// rest_plan_task_restart_test.go). No agent_id is assigned, so the
// StartTaskNow launch branch in handleTaskPatch is not engaged — this test
// isolates the transition-legality question from agent dispatch.
func TestTaskPatch_FailedToInProgress_Legal(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Failed InProgress Pin WS")
	taskID := mustCreateTask(t, api, wsID, "genuinely failed, no cancel reason", "")

	failedStatus := task.StatusFailed
	_, err := api.taskStore.Update(taskID, task.Patch{Status: &failedStatus})
	require.NoError(t, err)

	w := patchTask(t, api, taskID, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"failed->in_progress PATCH (the SPA Run/drag path) must stay legal; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatusInProgress, updated.Status)

	reloaded, gerr := api.taskStore.Get(taskID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusInProgress, reloaded.Status)
}

// TestTaskPatch_CancelledFailedToInProgress_Legal is the counterpart case: a
// task stopped by the user (cancel_reason=stopped_by_user, the case the
// dedicated restart route DOES accept) must ALSO remain reachable via the
// direct PATCH path — the two routes to "run it again" (restart vs. drag/
// Run) must not diverge on this input.
func TestTaskPatch_CancelledFailedToInProgress_Legal(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Cancelled InProgress Pin WS")
	taskID := mustCreateTask(t, api, wsID, "user-cancelled", "")

	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	_, err := api.taskStore.Update(taskID, task.Patch{Status: &failedStatus, CancelReason: &cancelReason})
	require.NoError(t, err)

	w := patchTask(t, api, taskID, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code, "failed(stopped_by_user)->in_progress PATCH must stay legal; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatusInProgress, updated.Status)
}

// TestHandleTaskRestart_GateDelegatesToTaskPackage is a regression check
// that centralizing the restart reason-gate into task.ValidateStandaloneRestart
// (pkg/task/store.go) did not change the handler's observable behavior: a
// genuinely-failed task (no cancel_reason) still 409s via POST
// /tasks/{id}/restart, and its status/cancel_reason are left untouched by
// the rejected call (RestartReset must never run).
func TestHandleTaskRestart_GateDelegatesToTaskPackage(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Restart Gate Delegation WS")
	taskID := mustCreateTask(t, api, wsID, "genuinely failed, attempts exhausted", "")

	failedStatus := task.StatusFailed
	attempts := 3
	_, err := api.taskStore.Update(taskID, task.Patch{Status: &failedStatus, AttemptCount: &attempts})
	require.NoError(t, err)

	w := postTaskAction(t, api, taskID, "restart")
	require.Equal(t, http.StatusConflict, w.Code, "genuinely-failed task must still 409 on restart; body=%s", w.Body.String())

	reloaded, gerr := api.taskStore.Get(taskID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusFailed, reloaded.Status, "rejected restart must leave status untouched")
	assert.Equal(t, 3, reloaded.AttemptCount, "rejected restart must not have run RestartReset (attempt_count untouched)")
	assert.Empty(t, reloaded.CancelReason)
}

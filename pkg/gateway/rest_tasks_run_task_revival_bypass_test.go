// rest_tasks_run_task_revival_bypass_test.go — REST-level regression test for
// the LAST known bypass of pkg/agent/task_executor.go's
// requireRunTaskAutoDispatchApproved (the run_task ask-policy gate).
//
// rest_tasks_run_task_gate_test.go already closed the "forge a bare
// started_at" bypass by requiring StartedAt AND SessionID. This file closes
// the remaining one: only `done` is frozen against status edits
// (validateTransition, pkg/task/store.go) — a `failed` task may be legally
// PATCHed straight back to `next` via a plain status-only Update, and
// handleTaskPatch (rest_tasks.go) applies that Update WITHOUT clearing
// StartedAt/SessionID/CompletedAt (it only ever touches fields the request
// itself names). A task that ran once for real — StartedAt, SessionID, and
// CompletedAt all genuinely stamped by that CONCLUDED run — reached `failed`,
// and is then revived with a bare `{"status":"next"}` PATCH therefore still
// carries a genuine-looking, non-empty StartedAt/SessionID pair. Before the
// fix, the very next CheckQueuedTasks heartbeat tick would treat that as an
// "already-approved continuation" and dispatch with no fresh run_task
// approval, even though the run it is "continuing" already concluded.
//
// Fix: requireRunTaskAutoDispatchApproved now ALSO requires
// t.CompletedAt == "" — a field only ever stamped by a genuine terminal
// write (completeTaskWithResult/failTask), never by the goal loop's own
// in-flight continuation writes. See that function's doc comment and
// pkg/agent/task_executor_run_task_revival_bypass_test.go for the paired
// package-internal proof.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestHandleTaskPatch_FailedToNextRevivalDoesNotBypassRunTaskAskGate is the
// end-to-end reproduction/regression test: a bare PATCH {"status":"next"} —
// no other field — reviving a `failed` task that carries a GENUINE prior
// run's StartedAt/SessionID/CompletedAt must not let it slip past
// requireRunTaskAutoDispatchApproved on the next unattended dispatch tick.
//
// BDD:
//
//	Given a standalone task assigned to an agent whose run_task policy is
//	  "ask", genuinely dispatched once already and now `failed` — StartedAt,
//	  SessionID, and CompletedAt all stamped by that concluded run (seeded
//	  directly here, mirroring the store-seeded `failed` precondition
//	  TestTaskPatch_FailedToInProgress_Legal already uses — the exact
//	  bypass under test is the revival PATCH itself, driven through the
//	  real handler, not this setup step),
//	When PATCH /api/v1/tasks/{id} is sent with ONLY {"status":"next"},
//	Then the PATCH succeeds (200): failed->next is a legal transition and
//	  StartedAt/SessionID/CompletedAt are left untouched (the request names
//	  none of them);
//	And when the next CheckQueuedTasks tick runs,
//	Then the task must NOT have auto-dispatched (status still `next`) — a
//	  revived task's stale, concluded-run claim must not be mistaken for an
//	  already-approved continuation of a NEW run.
func TestHandleTaskPatch_FailedToNextRevivalDoesNotBypassRunTaskAskGate(t *testing.T) {
	api := newTestRestAPIWithAskPolicyAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{runTaskGateTestAgentID})

	tsk := createTaskViaAPI(t, api, "failed->next revival attack", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	wAgent := patchTask(t, api, tsk.Id, `{"agent_id":"`+runTaskGateTestAgentID+`"}`)
	require.Equal(t, http.StatusOK, wAgent.Code,
		"assign agent_id must succeed; body=%s", wAgent.Body.String())

	// Seed the "concluded prior run" precondition directly — a genuine
	// StartedAt/SessionID/CompletedAt triple exactly as a real terminal
	// write (completeTaskWithResult/failTask) leaves behind. This mirrors
	// the established precedent (rest_task_restart_domain_test.go's
	// TestTaskPatch_FailedToInProgress_Legal) of seeding a `failed`
	// precondition via the store directly — the bypass under test is the
	// REVIVAL PATCH below, driven through the real handler, not this setup.
	now := time.Now().UTC().Format(time.RFC3339)
	failedStatus := task.StatusFailed
	sessionID := "concluded-run-session"
	_, seedErr := api.taskStore.Update(tsk.Id, task.Patch{
		Status:      &failedStatus,
		StartedAt:   &now,
		SessionID:   &sessionID,
		CompletedAt: &now,
	})
	require.NoError(t, seedErr, "seed failed precondition")

	// The attack: a bare status-only PATCH reviving the task — no
	// started_at, no other field, exactly what a board "requeue" action
	// sends.
	wRevive := patchTask(t, api, tsk.Id, `{"status":"next"}`)
	require.Equal(t, http.StatusOK, wRevive.Code,
		"failed->next PATCH must succeed (it is a legal transition — only `done` is frozen); body=%s",
		wRevive.Body.String())

	var revived gen.Task
	require.NoError(t, json.Unmarshal(wRevive.Body.Bytes(), &revived))
	require.Equal(t, gen.TaskStatusNext, revived.Status)
	require.NotNil(t, revived.SessionId,
		"the revival PATCH must leave the prior run's session_id untouched — TaskUpdateRequest has no "+
			"session_id field to clear it with, exactly the stale-claim precondition the gate must catch")
	require.NotNil(t, revived.StartedAt, "the revival PATCH must leave the prior run's started_at untouched")

	// Simulate the next unattended dispatch tick (what the heartbeat drives
	// in production).
	api.taskExecutor.CheckQueuedTasks(context.Background())

	final := getTaskStatus(t, api, tsk.Id)
	require.Equal(t, gen.TaskStatusNext, final,
		"task must NOT have auto-dispatched — a revived failed task carrying a CONCLUDED prior run's "+
			"StartedAt/SessionID must still require a fresh, explicit run_task approval under the "+
			"agent's ask policy")
}

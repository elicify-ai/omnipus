// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// alreadyRunningLauncher is a steer.SessionLauncher whose Launch fails with
// the typed already-running sentinel the executor's launch-slot claim
// produces for a concurrent loser (task_executor.go::StartTaskNow). It is
// the deterministic seam for the REST caller behaviour under test: what
// rest_tasks.go::launchIfStarted does when StartTaskNow reports
// already-running. Everything upstream (the real claim race) is covered by
// pkg/agent's launcher-race test; this test pins the caller mapping.
type alreadyRunningLauncher struct{}

func (alreadyRunningLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, fmt.Errorf(
		"task_executor: StartTaskNow: claim: task %q: %w", req.Origin.TaskID, task.ErrAlreadyRunning)
}

func (alreadyRunningLauncher) Dispatch(_ context.Context, _ string, gen int) (steer.DispatchResult, error) {
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}

// TestHandleTaskPatch_InProgress_AlreadyRunningSentinel_NoRevert proves the
// double-click loser outcome: when StartTaskNow reports already-running (a
// concurrent start of this exact task is in flight), the PATCH must NOT
// revert the task's live state (status in_progress, StartedAt stamp) and
// must answer 200 — PATCH is idempotent and the loser's own status patch
// already applied. Red on the pre-fix code, which reverted status+StartedAt
// under a live run and answered 500.
func TestHandleTaskPatch_InProgress_AlreadyRunningSentinel_NoRevert(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

	tsk := createTaskViaAPI(t, api, "AgentTaskSentinel", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, wAssign.Code,
		"assigning agent_id=mia must return 200; body=%s", wAssign.Body.String())

	api.taskExecutor.SetSessionLauncher(alreadyRunningLauncher{})

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusOK, w.Code,
		"an already-running start must answer 200 (idempotent PATCH), not 500; body=%s", w.Body.String())

	// No revert: the loser must not clobber the in_progress state under a
	// live run by another caller.
	assert.Equal(t, gen.TaskStatusInProgress, getTaskStatus(t, api, tsk.Id),
		"task must stay in_progress after an already-running start (no revert)")

	// StartedAt must survive: the in_progress transition stamped it, and the
	// loser must not wipe the winner's live stamp.
	startedAtAfter := getTaskStartedAt(t, api, tsk.Id)
	assert.NotNil(t, startedAtAfter,
		"StartedAt must survive an already-running start (loser must not wipe the winner's stamp)")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// BDD (delta review R1 #2, architect ruling): Given a task executor that is
// draining for shutdown, When PATCH status=in_progress starts a task, Then the
// answer is 503 Service Unavailable with the fixed shutdown text (not the generic
// 500), and the task goes back to its prior status so it is startable after the
// restart. The shutdown cause carries no raw error text.
func TestHandleTaskPatch_RunTask_DrainingReturns503(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

	tsk := createTaskViaAPI(t, api, "DrainTest", wsID)
	advanceTaskToNext(t, api, tsk.Id)
	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, wAssign.Code, "assign agent_id must succeed; body=%s", wAssign.Body.String())

	te := api.taskExecutor
	require.NotNil(t, te)
	te.Drain(time.Millisecond) // intake closed: new dispatch is refused with ErrExecutorDraining

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "a draining executor answers 503; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "shutting down", "the answer is the fixed shutdown text")
	assert.NotContains(t, w.Body.String(), "draining for shutdown", "no raw executor error text reaches the client")

	assert.Equal(t, gen.TaskStatusNext, getTaskStatus(t, api, tsk.Id), "the task reverts to 'next' so it is startable after restart")
}

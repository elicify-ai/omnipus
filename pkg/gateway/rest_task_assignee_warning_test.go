// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_task_assignee_warning_test.go pins the REST half of the founder
// decision of 2026-09-15. The human task form may assign a task to an agent
// that cannot finish it — an operator may assign first and fix the agent's
// permissions afterwards (ADR-049 D2 rule 5: agent tool paths reject, the UI
// warns) — so the API saves it and answers with Task.assignee_warning: the
// plain reason naming the fix, routed to the agent_id field. Starting such a
// task ends it Failed at once with that same text, before any model call.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const blockedAgentWarning = "Blocked Agent isn't allowed to report tasks as done. " +
	"Allow 'goal_claim' for it in Agents → Tools, or assign another agent."

// readinessCallCounter is a provider that records how many requests reached it.
type readinessCallCounter struct {
	mu    sync.Mutex
	calls int
}

func (p *readinessCallCounter) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "working on it"}, nil
}

func (p *readinessCallCounter) GetDefaultModel() string { return "test-model" }

func (p *readinessCallCounter) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func postTaskForAgent(t *testing.T, api *restAPI, workspaceID, agentID string) gen.Task {
	t.Helper()
	body := fmt.Sprintf(`{"title":"readiness","action":"llm","workspace_id":%q,"agent_id":%q,"criteria":%s,"dod":%s}`,
		workspaceID, agentID, validCriteriaJSON, validDoDJSON)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code,
		"an assignment the agent cannot finish is saved, not refused, on this API; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	return tsk
}

func requireBlockedWarning(t *testing.T, tsk gen.Task) {
	t.Helper()
	require.NotNil(t, tsk.AssigneeWarning, "the task must carry assignee_warning")
	assert.Equal(t, blockedAgentWarning, tsk.AssigneeWarning.Message)
	assert.Equal(t, gen.TaskAssigneeWarningFieldAgentId, tsk.AssigneeWarning.Field)
}

func newAssigneeWarningAPI(t *testing.T) (*restAPI, *readinessCallCounter, string) {
	t.Helper()
	provider := &readinessCallCounter{}
	blocked := config.AgentConfig{
		ID: "blocked-agent", Name: "Blocked Agent",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
			Policies: map[string]config.ToolPolicy{"goal_claim": config.ToolPolicyDeny},
		}},
	}
	api := newTestRestAPIAlignedStoresWithProvider(t, provider, blocked)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia", "blocked-agent"})
	t.Cleanup(func() { api.taskExecutor.Drain(10 * time.Second) })
	return api, provider, wsID
}

// Given an agent denied goal_claim
// When the task form assigns it a task, reads the task back, and reassigns it
// to an agent that can finish it
// Then create and read both carry the warning on agent_id, and the reassignment
// clears it; a task assigned to a ready agent never carries one.
func TestTaskREST_AssigneeWarning_WarnsAndClears(t *testing.T) {
	api, _, wsID := newAssigneeWarningAPI(t)

	created := postTaskForAgent(t, api, wsID, "blocked-agent")
	requireBlockedWarning(t, created)
	requireBlockedWarning(t, getTaskFull(t, api, created.Id))

	w := patchTask(t, api, created.Id, `{"agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var reassigned gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reassigned))
	assert.Nil(t, reassigned.AssigneeWarning, "an agent that can finish the task clears the warning")

	ready := postTaskForAgent(t, api, wsID, "mia")
	assert.Nil(t, ready.AssigneeWarning, "a ready agent carries no warning")
}

// Given a saved task whose agent cannot finish it
// When the operator starts it anyway
// Then the task ends Failed at once with the warning's text, no attempt used and
// no model call, and a finished task no longer carries the warning.
func TestTaskREST_StartingATaskItsAgentCannotFinish_EndsFailedAtOnce(t *testing.T) {
	api, provider, wsID := newAssigneeWarningAPI(t)
	created := postTaskForAgent(t, api, wsID, "blocked-agent")
	advanceTaskToNext(t, api, created.Id)

	w := patchTask(t, api, created.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code, "starting is accepted; the run itself ends the task. body=%s", w.Body.String())

	var final gen.Task
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		final = getTaskFull(t, api, created.Id)
		if final.Status == gen.TaskStatusFailed {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.Equal(t, gen.TaskStatusFailed, final.Status, "the task must end failed at once")
	require.NotNil(t, final.Result)
	assert.Equal(t, blockedAgentWarning, *final.Result)
	assert.Nil(t, final.AttemptCount, "no attempt is used")
	assert.Equal(t, 0, provider.count(), "no model call is made")
	assert.Nil(t, final.AssigneeWarning, "a failed task carries no assignee warning")
}

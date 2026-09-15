// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// task_assignee_readiness_test.go pins the create_task_in_workspace /
// update_task_in_workspace half of the founder decision of 2026-09-15: these
// agent tools refuse to assign a task to an agent that cannot finish it, with
// the reason, the agent_id field and nothing written — the same rule the plain
// task tools apply.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	wsReadyAgent    = "agent-ready"
	wsNoClaimAgent  = "agent-no-claim"
	wsNoClaimReason = "Agent No Claim isn't allowed to report tasks as done. " +
		"Allow 'goal_claim' for it in Agents → Tools, or assign another agent."
)

func wsFakeReadiness(agentID string, _ []task.AcceptanceCriterion) string {
	if agentID == wsNoClaimAgent {
		return wsNoClaimReason
	}
	return ""
}

func wsReadinessDeps(t *testing.T) (*systools.Deps, string) {
	t.Helper()
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.ResolveBashPolicy = func(string) (string, bool) { return "allow", true }
	deps.AssigneeCannotFinish = wsFakeReadiness
	return deps, home
}

func wsCreateArgs(agentID string) map[string]any {
	return map[string]any{
		"name":         "readiness",
		"workspace_id": testWorkspaceID,
		"agent_id":     agentID,
		"criteria":     []any{map[string]any{"kind": "prose", "text": "the report exists"}},
		"dod":          workspaceDoDArg(),
	}
}

func requireWorkspaceAssigneeRefusal(t *testing.T, res *tools.ToolResult, wantAgent string) {
	t.Helper()
	require.True(t, res.IsError, "the write must be refused: %s", res.ForLLM)
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			Suggestion string `json:"suggestion"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &body), "the refusal is the tools' JSON error: %s", res.ForLLM)
	assert.False(t, body.Success)
	assert.Equal(t, "INVALID_INPUT", body.Error.Code)
	assert.Equal(t, wsNoClaimReason, body.Error.Message, "the message is the reason, naming the fix")
	assert.Equal(t, "agent_id", body.Error.Suggestion, "the refusal names the agent field, like the other field refusals")
	var refusal *tools.AssigneeCannotFinishError
	require.True(t, errors.As(res.Err, &refusal), "the refusal must carry the structured error, got %v", res.Err)
	assert.Equal(t, "agent_id", refusal.Field)
	assert.Equal(t, wantAgent, refusal.AgentID)
}

// Given an assignee that cannot finish any task
// When create_task_in_workspace assigns it
// Then the create is refused and no task is written; a ready agent is accepted.
func TestCreateTaskInWorkspace_AssigneeCannotFinish_Refused(t *testing.T) {
	deps, home := wsReadinessDeps(t)
	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")

	requireWorkspaceAssigneeRefusal(t, create.Execute(ctx, wsCreateArgs(wsNoClaimAgent)), wsNoClaimAgent)
	store := task.New(filepath.Join(home, "tasks"))
	all, err := store.List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	assert.Empty(t, all, "a refused create writes no task")

	ok := create.Execute(ctx, wsCreateArgs(wsReadyAgent))
	require.False(t, ok.IsError, "a ready assignee is accepted: %s", ok.ForLLM)
}

// Given a task assigned to a ready agent
// When update_task_in_workspace reassigns it to an agent that cannot finish it
// Then the update is refused and the task keeps its agent.
func TestUpdateTaskInWorkspace_ReassignmentToAnAgentThatCannotFinish_Refused(t *testing.T) {
	deps, home := wsReadinessDeps(t)
	create := systools.NewTaskCreateTool(deps)
	update := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	require.False(t, create.Execute(ctx, wsCreateArgs(wsReadyAgent)).IsError)
	store := task.New(filepath.Join(home, "tasks"))
	all, err := store.List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	require.Len(t, all, 1)
	id := all[0].ID

	requireWorkspaceAssigneeRefusal(t,
		update.Execute(ctx, map[string]any{"id": id, "agent_id": wsNoClaimAgent}), wsNoClaimAgent)

	stored, err := store.Get(id)
	require.NoError(t, err)
	assert.Equal(t, wsReadyAgent, stored.AgentID, "a refused reassignment writes nothing")

	renamed := update.Execute(ctx, map[string]any{"id": id, "name": "renamed"})
	require.False(t, renamed.IsError, "an edit that changes neither agent nor judged set is not asked about: %s", renamed.ForLLM)
}

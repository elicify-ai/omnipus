// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// FR-037 provenance on the cross-workspace create surface. These tests pin
// OUTCOMES on disk: what created_by_agent_id a persisted task actually carries
// after each of the two ways this tool is driven, and what a
// created_by_agent_id-filtered query therefore returns.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// createInWorkspace runs create_task_in_workspace with ctx and returns the
// persisted task.
func createInWorkspace(ctx context.Context, t *testing.T, deps *systools.Deps, home string, args map[string]any) *task.Task {
	t.Helper()
	result := systools.NewTaskCreateTool(deps).Execute(ctx, args)
	require.False(t, result.IsError, "create_task_in_workspace: %s", result.ForLLM)

	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &out))
	require.NotEmpty(t, out.ID)

	got, err := task.New(home + "/tasks").Get(out.ID)
	require.NoError(t, err)
	return got
}

// TestCreateTaskInWorkspace_AgentPathStampsProvenance proves a task created by
// a real calling agent carries that agent's id in created_by_agent_id, and is
// therefore findable through a created_by_agent_id-scoped query (the filter
// list_jobs' dispatched half and list_tasks role="delegator" both use).
func TestCreateTaskInWorkspace_AgentPathStampsProvenance(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	ctx := tools.WithAgentID(context.Background(), "jim")
	got := createInWorkspace(ctx, t, deps, home, map[string]any{
		"name":         "delegated by jim",
		"workspace_id": testWorkspaceID,
		"agent_id":     "worker-agent",
		"criteria":     workspaceCriteriaArg(),
	})

	assert.Equal(t, "jim", got.CreatedByAgentID,
		"the agent path must stamp FR-037 provenance")
	assert.True(t, got.CreatedByAgent("jim"))

	rows, err := task.New(home + "/tasks").List(task.Filter{CreatedByAgentID: "jim"})
	require.NoError(t, err)
	require.Len(t, rows, 1, "the created task must be findable by its author")
	assert.Equal(t, "delegated by jim", rows[0].Title)
}

// TestCreateTaskInWorkspace_NoAgentPrincipal_StaysUnattributed proves the
// human/System-Agent-session path (no agent id on the context at all) still
// succeeds and writes an UNATTRIBUTED task — no fabricated agent id — and that
// such a task is never disclosed by an agent-scoped query.
func TestCreateTaskInWorkspace_NoAgentPrincipal_StaysUnattributed(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	// Only a session owner, exactly as a plain human session drives it. No
	// agent_id arg either, so no delegation is involved.
	ctx := tools.WithSessionOwner(context.Background(), "alice")
	got := createInWorkspace(ctx, t, deps, home, map[string]any{
		"name":         "alice's own task",
		"workspace_id": testWorkspaceID,
	})

	assert.Empty(t, got.CreatedByAgentID,
		"a create with no agent principal must stay unattributed, never fabricate an id")
	assert.Equal(t, "alice", got.CreatedBy, "the session owner still lands in the display field")
	assert.False(t, got.CreatedByAgent("alice"),
		"a username must never satisfy an agent-id predicate")

	rows, err := task.New(home + "/tasks").List(task.Filter{CreatedByAgentID: "alice"})
	require.NoError(t, err)
	assert.Empty(t, rows, "an unattributed task must not appear in any agent's roster")
}

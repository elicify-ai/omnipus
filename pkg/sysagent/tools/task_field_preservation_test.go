// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// T3: sysagent field preservation regression for the unified task store (Sprint 2).
//
// BRD bug #404 secondary: the old system.task.update used a minimal 8-field struct
// that silently dropped prompt, priority, milestone_id, session_id, result, and
// owner on every write. The fix: TaskUpdateTool now reads the FULL task.Task
// before modifying fields, so only the fields explicitly present in the args map
// are overwritten.
//
// Rewritten for Sprint 2: boardtask.Task is deleted; we now use task.Task directly.
// Name field → Title field. The system.task.create tool requires workspace_id.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestRegression_SysagentTaskUpdate_PreservesAllFields guards against the bug
// where system.task.update used a minimal struct that silently dropped prompt,
// priority, tags, session_id, result, and owner on every write.
//
// BDD: Given a task on disk with prompt, priority, tags,
//
//	session_id, result, owner, and agent_id populated,
//
// When system.task.update is called with only {"id":..., "name":"New Name"},
// Then reading the task back shows the new title AND all other fields intact.
//
// Also: update with agent_id:"" must NOT clear the existing agent_id.
//
// Traces to: feat/level1-project-task-mgmt — #404 secondary: field-preserving
// read-modify-write in TaskUpdateTool.Execute (Sprint 2: via task.Patch).
func TestRegression_SysagentTaskUpdate_PreservesAllFields(t *testing.T) {
	// Traces to: #404 secondary — system.task.update field-preserving RMW via task.Task
	deps, home := newTestDepsWithHome(t)

	// Create the tasks dir and write the task directly to disk with all fields
	// populated. We write via JSON so we can include fields the create tool does
	// not expose (prompt, priority, milestone_id, session_id, result, owner).
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))

	const taskID = "01JXPRESERVE_TASK_0000001"
	const wantPrompt = "Run the full regression suite with coverage"
	const wantAgentID = "01JXPRESERVE_AGENT000001"
	wantTags := []string{"release-42"}
	const wantSessionID = "preserve-session-42"
	const wantOwner = "alice"
	const wantResult = "42 tests passed, 0 failed"
	wantPriority := 2

	now := time.Now().UTC().Format(time.RFC3339)
	// Write via task.Task (Sprint 2: boardtask.Task deleted, unified store uses task.Task).
	original := task.Task{
		ID:          taskID,
		Title:       "OriginalName", // Sprint 2: Name→Title
		Description: "Original description",
		Prompt:      wantPrompt,
		Priority:    wantPriority,
		Tags:        wantTags,
		SessionID:   wantSessionID,
		Result:      wantResult,
		Status:      task.StatusInbox,
		WorkspaceID: testWorkspaceID,
		AgentID:     wantAgentID,
		Owner:       wantOwner,
		Action:      task.ActionLLM,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	taskBytes, err := json.Marshal(original)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, taskID+".json"), taskBytes, 0o600))

	// Invoke system.task.update with ONLY a name change.
	// Sprint 2: the tool uses "name" arg key (maps to Title internally).
	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":   taskID,
		"name": "Updated Name",
	})
	require.False(t, result.IsError,
		"system.task.update must succeed; got: %s", result.ForLLM)

	// Read back the on-disk file and verify every preserved field.
	rawBytes, err := os.ReadFile(filepath.Join(tasksDir, taskID+".json"))
	require.NoError(t, err)
	var got task.Task
	require.NoError(t, json.Unmarshal(rawBytes, &got))

	assert.Equal(t, "Updated Name", got.Title,
		"title must be updated by system.task.update")

	assert.Equal(t, wantPrompt, got.Prompt,
		"prompt must survive a partial system.task.update (old minimal-struct bug zeroed it) #404")

	assert.Equal(t, wantPriority, got.Priority,
		"priority must survive a partial system.task.update #404")

	assert.Equal(t, wantTags, got.Tags,
		"tags must survive a partial system.task.update #404")

	assert.Equal(t, wantSessionID, got.SessionID,
		"session_id must survive a partial system.task.update #404")

	assert.Equal(t, wantResult, got.Result,
		"result must survive a partial system.task.update #404")

	assert.Equal(t, wantAgentID, got.AgentID,
		"agent_id must survive a partial system.task.update #404")

	assert.Equal(t, wantOwner, got.Owner,
		"owner must survive a partial system.task.update #404")

	assert.Equal(t, "Original description", got.Description,
		"description must survive a partial system.task.update #404")

	// Differentiation test: a SECOND update with only "status" change must also
	// preserve title (= "Updated Name") and all other fields. Two different inputs
	// must produce two different outputs.
	result2 := tool.Execute(context.Background(), map[string]any{
		"id":     taskID,
		"status": "next",
	})
	require.False(t, result2.IsError, "second update must also succeed; got: %s", result2.ForLLM)

	rawBytes2, err := os.ReadFile(filepath.Join(tasksDir, taskID+".json"))
	require.NoError(t, err)
	var got2 task.Task
	require.NoError(t, json.Unmarshal(rawBytes2, &got2))

	assert.Equal(t, "Updated Name", got2.Title,
		"title must still be 'Updated Name' after status-only update (not reverted)")
	assert.Equal(t, task.StatusNext, got2.Status,
		"status must be updated to 'next' by second update")
	assert.Equal(t, wantPrompt, got2.Prompt,
		"prompt must still be intact after second update")
	assert.Equal(t, wantOwner, got2.Owner,
		"owner must still be intact after second update")

	// Guard: agent_id:"" must NOT clear an existing agent_id.
	// The new tool only applies agent_id when the value is non-empty (v != "").
	result3 := tool.Execute(context.Background(), map[string]any{
		"id":       taskID,
		"agent_id": "",
	})
	require.False(t, result3.IsError, "update with empty agent_id must succeed (no-op); got: %s", result3.ForLLM)

	rawBytes3, err := os.ReadFile(filepath.Join(tasksDir, taskID+".json"))
	require.NoError(t, err)
	var got3 task.Task
	require.NoError(t, json.Unmarshal(rawBytes3, &got3))
	assert.Equal(t, wantAgentID, got3.AgentID,
		"system.task.update with agent_id:\"\" must NOT clear existing agent_id (#404 secondary)")
}

// TestRegression_SysagentTaskCreate_FieldsRoundTrip verifies that all fields
// written by system.task.create round-trip correctly through the on-disk JSON
// representation — no fields are silently dropped by the serializer.
//
// This is a differentiation test: two creates with different names/statuses must
// produce files with different content (not hardcoded values).
//
// Sprint 2: system.task.create now requires workspace_id; we seed a workspace
// file before calling the tool.
//
// Traces to: feat/level1-project-task-mgmt — #404 board task field coverage
func TestRegression_SysagentTaskCreate_FieldsRoundTrip(t *testing.T) {
	// Traces to: #404 — create tool field round-trip (Sprint 2: unified task store)
	deps, home := newTestDepsWithHome(t)

	// Seed the workspace so system.task.create can validate workspace_id.
	seedWorkspace(t, home, testWorkspaceID)

	create := systools.NewTaskCreateTool(deps)

	// Create task 1: name="Alpha", status="inbox", agent_id="agent-1"
	r1 := create.Execute(context.Background(), map[string]any{
		"name":         "Alpha",
		"status":       "inbox",
		"agent_id":     "agent-1",
		"workspace_id": testWorkspaceID,
	})
	require.False(t, r1.IsError, "create Alpha must succeed; got: %s", r1.ForLLM)
	var resp1 map[string]any
	require.NoError(t, json.Unmarshal([]byte(r1.ForLLM), &resp1))
	id1, ok := resp1["id"].(string)
	require.True(t, ok, "create must return an id; got: %v", resp1)

	// Create task 2: name="Beta", status="next", agent_id="agent-2"
	r2 := create.Execute(context.Background(), map[string]any{
		"name":         "Beta",
		"status":       "next",
		"agent_id":     "agent-2",
		"workspace_id": testWorkspaceID,
	})
	require.False(t, r2.IsError, "create Beta must succeed; got: %s", r2.ForLLM)
	var resp2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(r2.ForLLM), &resp2))
	id2, ok := resp2["id"].(string)
	require.True(t, ok, "create must return an id; got: %v", resp2)

	// Differentiation: two different inputs must produce different IDs and content.
	assert.NotEqual(t, id1, id2, "two creates must produce different task IDs")

	// Read back from disk and verify all fields.
	// Sprint 2: on-disk format is task.Task; Title replaces Name.
	tasksDir := filepath.Join(home, "tasks")

	rawBytes1, err := os.ReadFile(filepath.Join(tasksDir, id1+".json"))
	require.NoError(t, err)
	var disk1 task.Task
	require.NoError(t, json.Unmarshal(rawBytes1, &disk1))
	assert.Equal(t, "Alpha", disk1.Title, "task1 title must be Alpha on disk")
	assert.Equal(t, task.StatusInbox, disk1.Status, "task1 status must be inbox on disk")
	assert.Equal(t, "agent-1", disk1.AgentID, "task1 agent_id must be agent-1 on disk")
	assert.NotEmpty(t, disk1.ID, "task1 ID must be non-empty on disk")
	assert.NotEmpty(t, disk1.CreatedAt, "task1 created_at must be non-empty on disk")
	assert.Equal(t, testWorkspaceID, disk1.WorkspaceID, "task1 workspace_id must be set on disk")

	rawBytes2, err := os.ReadFile(filepath.Join(tasksDir, id2+".json"))
	require.NoError(t, err)
	var disk2 task.Task
	require.NoError(t, json.Unmarshal(rawBytes2, &disk2))
	assert.Equal(t, "Beta", disk2.Title, "task2 title must be Beta on disk")
	assert.Equal(t, task.StatusNext, disk2.Status, "task2 status must be next on disk")
	assert.Equal(t, "agent-2", disk2.AgentID, "task2 agent_id must be agent-2 on disk")

	// The two tasks must be entirely different.
	assert.NotEqual(t, disk1.Title, disk2.Title,
		"two tasks created with different names must have different titles on disk")
	assert.NotEqual(t, disk1.Status, disk2.Status,
		"two tasks created with different statuses must have different statuses on disk")
}

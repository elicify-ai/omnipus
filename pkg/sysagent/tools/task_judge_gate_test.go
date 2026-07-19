// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// review r2 Chunk 1: update_task_in_workspace (the privileged, cross-workspace
// task tool reachable by Jim/core agents, and by a task's own assignee doing
// a self-run) previously wrote status:"done" straight to disk — with
// AdvanceBlockedDependents firing immediately — for a task WITH acceptance
// criteria, completely bypassing the evidence-ladder judge. These tests prove
// the fix: a done-claim on a criteria task is staged as PendingJudgeClaim
// ONLY when the call is genuinely part of that task's own executor run
// (tools.ToolRunningTaskID(ctx) == the target task id); otherwise it is
// rejected outright, never silently trusted and never staged for nothing to
// adjudicate.

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
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// seedCriteriaTaskWS writes a task WITH one prose acceptance criterion
// directly to disk (mirrors seedTask in task_advance_test.go, which has no
// criteria param).
func seedCriteriaTaskWS(t *testing.T, home, id, title string, status task.Status) {
	t.Helper()
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	tk := task.Task{
		ID:          id,
		Title:       title,
		Status:      status,
		Action:      task.ActionLLM,
		WorkspaceID: testWorkspaceID,
		AgentID:     "agent-a",
		CreatedAt:   now,
		UpdatedAt:   now,
		Criteria: []task.AcceptanceCriterion{
			{
				Kind:   task.KindProse,
				Text:   "the work is verifiably done",
				Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "agent-a"},
				Status: task.CritPending,
			},
		},
	}
	data, err := json.Marshal(tk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), data, 0o600))
}

// diskTaskPendingJudgeClaim reads the on-disk PendingJudgeClaim for id.
func diskTaskPendingJudgeClaim(t *testing.T, home, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "tasks", id+".json"))
	require.NoError(t, err)
	var tk task.Task
	require.NoError(t, json.Unmarshal(data, &tk))
	return tk.PendingJudgeClaim
}

// TestSysagentTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected proves an
// out-of-band update_task_in_workspace(status:"done") call on a criteria task
// — no ToolRunningTaskID in ctx at all — is rejected, not staged, and does
// NOT advance dependents.
func TestSysagentTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXJUDGEGATE_OOB0000001"
	seedCriteriaTaskWS(t, home, taskID, "OutOfBandDoneClaim", task.StatusInProgress)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":     taskID,
		"status": "done",
		"result": "I claim this is done",
	})

	assert.True(t, result.IsError,
		"an out-of-band done-claim on a criteria task must be rejected; got success: %s", result.ForLLM)
	assert.Equal(t, task.StatusInProgress, diskTaskStatus(t, home, taskID),
		"status must not have been written by the rejected out-of-band call")
	assert.Empty(t, diskTaskPendingJudgeClaim(t, home, taskID),
		"no claim should be staged for an out-of-band call — nothing would ever adjudicate it")
}

// TestSysagentTaskUpdate_DoneOnCriteriaTask_WrongRunningTask_Rejected proves
// the gate checks the SPECIFIC running task id, not merely "some task is
// running": a genuine task-run turn for a DIFFERENT task must still be
// rejected when it tries to force-complete taskID.
func TestSysagentTaskUpdate_DoneOnCriteriaTask_WrongRunningTask_Rejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXJUDGEGATE_WRONG000001"
	seedCriteriaTaskWS(t, home, taskID, "WrongRunningTask", task.StatusInProgress)

	tool := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithRunningTaskID(context.Background(), "some-other-task-id")
	result := tool.Execute(ctx, map[string]any{
		"id":     taskID,
		"status": "done",
		"result": "I claim this is done",
	})

	assert.True(t, result.IsError,
		"a done-claim naming a task other than the one actually running must be rejected; got: %s", result.ForLLM)
	assert.Empty(t, diskTaskPendingJudgeClaim(t, home, taskID),
		"must not stage a claim for a task that isn't the running task")
}

// TestSysagentTaskUpdate_DoneOnCriteriaTask_InRun_StagesClaim proves the
// legitimate in-run case still works: a call genuinely part of THAT task's
// own executor run (ToolRunningTaskID == the target task id — exactly what
// task_executor.go's runTask/runTaskFromInProgress stamp) stages a
// PendingJudgeClaim for finishTaskRun to adjudicate, and does NOT write
// status directly (so dependents are not advanced prematurely).
func TestSysagentTaskUpdate_DoneOnCriteriaTask_InRun_StagesClaim(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXJUDGEGATE_INRUN000001"
	seedCriteriaTaskWS(t, home, taskID, "InRunDoneClaim", task.StatusInProgress)

	tool := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithRunningTaskID(context.Background(), taskID)
	result := tool.Execute(ctx, map[string]any{
		"id":     taskID,
		"status": "done",
		"result": "the verifiable work is complete",
	})

	assert.False(t, result.IsError,
		"an in-run done-claim must be staged (not rejected): %s", result.ForLLM)
	assert.Equal(t, task.StatusInProgress, diskTaskStatus(t, home, taskID),
		"status must remain unpatched pending judge adjudication")
	assert.Equal(t, "the verifiable work is complete", diskTaskPendingJudgeClaim(t, home, taskID),
		"PendingJudgeClaim must be staged with the claim text")
}

// TestSysagentTaskUpdate_DoneOnCriteriaTask_NeverAdvancesDependents proves the
// out-of-band rejection also protects a blocked dependent: it must NOT be
// advanced to next, since the blocker never actually reached done.
func TestSysagentTaskUpdate_DoneOnCriteriaTask_NeverAdvancesDependents(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const blockerID = "01JXJUDGEGATE_BLOCKER0001"
	const depID = "01JXJUDGEGATE_DEPEND00001"
	seedCriteriaTaskWS(t, home, blockerID, "Blocker", task.StatusInProgress)
	seedTask(t, home, depID, "Dependent", task.StatusBlocked, []string{blockerID})

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":     blockerID,
		"status": "done",
		"result": "done",
	})

	assert.True(t, result.IsError, "expected rejection: %s", result.ForLLM)
	assert.Equal(t, task.StatusBlocked, diskTaskStatus(t, home, depID),
		"dependent must remain blocked — the blocker was never genuinely completed")
}

// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// update_task_in_workspace is the privileged, cross-workspace twin of
// update_task. Founder decision 2026-09-14: while a task's own run executes,
// no status may be written for it through either tool — completion is claimed
// with goal_claim and decided by the judge. A done write on a criteria task
// from outside its run stays refused, because nothing would ever judge it.

import (
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
// directly to disk.
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

// TestSysagentTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected: a done claim
// from outside the task's run is refused and does not advance dependents.
func TestSysagentTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	const taskID = "01JXJUDGEGATE_OOB0000001"
	seedCriteriaTaskWS(t, home, taskID, "OutOfBandDoneClaim", task.StatusInProgress)

	result := systools.NewTaskUpdateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"id": taskID, "status": "done", "result": "I claim this is done",
	})

	assert.True(t, result.IsError, "out-of-band done on a criteria task must be refused: %s", result.ForLLM)
	assert.Equal(t, task.StatusInProgress, diskTaskStatus(t, home, taskID))
}

// TestSysagentTaskUpdate_StatusOnOwnRunningTask_Refused: done or failed on
// the task the caller's own run is executing is refused, and names goal_claim.
func TestSysagentTaskUpdate_StatusOnOwnRunningTask_Refused(t *testing.T) {
	for _, status := range []string{"done", "failed"} {
		t.Run(status, func(t *testing.T) {
			deps, home := newTestDepsWithHome(t)
			const taskID = "01JXJUDGEGATE_INRUN000001"
			seedCriteriaTaskWS(t, home, taskID, "InRunStatusWrite", task.StatusInProgress)

			ctx := tools.WithRunningTaskID(callerCtx("caller-agent"), taskID)
			result := systools.NewTaskUpdateTool(deps).Execute(ctx, map[string]any{"id": taskID, "status": status})

			require.True(t, result.IsError, "a status write on the caller's own running task must be refused: %s", result.ForLLM)
			assert.Contains(t, result.ForLLM, "goal_claim")
			assert.Equal(t, task.StatusInProgress, diskTaskStatus(t, home, taskID))
		})
	}
}

// TestSysagentTaskUpdate_DoneOnCriteriaTask_NeverAdvancesDependents: the
// refusal also protects a blocked dependent.
func TestSysagentTaskUpdate_DoneOnCriteriaTask_NeverAdvancesDependents(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	const blockerID = "01JXJUDGEGATE_BLOCKER0001"
	const depID = "01JXJUDGEGATE_DEPEND00001"
	seedCriteriaTaskWS(t, home, blockerID, "Blocker", task.StatusInProgress)
	seedTask(t, home, depID, "Dependent", task.StatusBlocked, []string{blockerID})

	result := systools.NewTaskUpdateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"id": blockerID, "status": "done", "result": "done",
	})

	assert.True(t, result.IsError, "expected refusal: %s", result.ForLLM)
	assert.Equal(t, task.StatusBlocked, diskTaskStatus(t, home, depID))
}

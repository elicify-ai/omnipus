// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_running_freeze_test.go — the update_task half of the running-task
// field freeze (operator decision, 2026-09-12).
//
// The rule is FIELD-LEVEL, and both halves of it matter equally:
//
//   - the judged contract (goal definition, acceptance criteria, definition
//     of done, prompt) freezes while a task is in_progress, because the Judge
//     scores the finished work against exactly that set;
//   - everything that records PROGRESS — status above all, plus result and
//     artifacts — stays writable, because the working agent updates those as
//     it goes and a gate that blocked them would break the agent mid-run.
//
// The second test is the one that carries the weight: an implementation that
// simply refused every update_task call on a running task would pass the
// first test on its own.
//
// The REST twin lives in pkg/gateway/rest_tasks_running_freeze_test.go.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedFreezeTask creates a task owned by "agent-a" in the requested status,
// carrying one acceptance criterion so the frozen set is non-empty to begin
// with.
func seedFreezeTask(t *testing.T, store *task.Store, status task.Status) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:            "freeze fixture",
		Prompt:           "do the original work",
		Action:           task.ActionLLM,
		AgentID:          "agent-a",
		CreatedBy:        "agent-a",
		CreatedByAgentID: "agent-a",
		WorkspaceID:      "ws-freeze",
		Status:           status,
		Criteria: []task.AcceptanceCriterion{{
			Kind:   task.KindProse,
			Text:   "the original criterion",
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "agent-a"},
			Status: task.CritPending,
		}},
	}
	require.NoError(t, store.Create(tk), "seedFreezeTask: create")
	return tk
}

func freezeToolCtx() context.Context {
	return WithAgentID(context.Background(), "agent-a")
}

// TestUpdateTaskTool_RunningTask_RefusesFrozenDefinitionFields is side one:
// the contract the work is judged against cannot move under the work.
func TestUpdateTaskTool_RunningTask_RefusesFrozenDefinitionFields(t *testing.T) {
	t.Parallel()

	newCriterion := []any{map[string]any{"text": "a different criterion entirely"}}
	newDoD := []any{map[string]any{"text": "a different definition of done"}}

	cases := []struct {
		name      string
		args      map[string]any
		wantNamed string
	}{
		{
			name:      "criteria alone",
			args:      map[string]any{"criteria": newCriterion},
			wantNamed: `"criteria"`,
		},
		{
			name:      "dod alone",
			args:      map[string]any{"dod": newDoD},
			wantNamed: `"dod"`,
		},
		{
			name:      "both at once names both",
			args:      map[string]any{"criteria": newCriterion, "dod": newDoD},
			wantNamed: `"criteria" and "dod"`,
		},
		{
			// The whole-request rule: a call mixing a frozen field with a
			// mutable one is refused entirely. Half-applying it would leave
			// the caller's own view of the task wrong in a way nothing
			// reports.
			name:      "mixed with a mutable field is refused as a whole",
			args:      map[string]any{"criteria": newCriterion, "result": "partial progress"},
			wantNamed: `"criteria"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each case builds its own store under its own t.TempDir() and
			// shares no state with its siblings, so the subtests are
			// parallel-safe — and a parent that calls t.Parallel() while its
			// subtests do not is what golangci-lint's tparallel flags.
			t.Parallel()

			store := task.New(t.TempDir())
			tk := seedFreezeTask(t, store, task.StatusInProgress)
			tool := NewTaskUpdateTool(store)

			args := map[string]any{"task_id": tk.ID}
			for k, v := range tc.args {
				args[k] = v
			}
			res := tool.Execute(freezeToolCtx(), args)

			require.True(t, res.IsError,
				"a frozen-field edit on a running task must be refused; got success: %s", res.ForLLM)
			assert.Contains(t, res.ForLLM, tc.wantNamed,
				"the refusal must NAME the frozen field(s), not fail generically: %s", res.ForLLM)
			assert.Contains(t, strings.ToLower(res.ForLLM), "while the task is running",
				"the refusal must say the task is running, so the caller can tell a state "+
					"conflict from a malformed argument: %s", res.ForLLM)
			assert.Contains(t, res.ForLLM, "409",
				"both entry points answer with the same 409 Conflict semantics: %s", res.ForLLM)

			// Nothing may have been written — neither the frozen field nor
			// the mutable one that rode along with it.
			got, err := store.Get(tk.ID)
			require.NoError(t, err)
			require.Len(t, got.Criteria, 1, "criteria must be untouched by a refused call")
			assert.Equal(t, "the original criterion", got.Criteria[0].Text,
				"the refused criteria edit must not have landed")
			assert.Empty(t, got.Result,
				"a mutable field riding along with a refused frozen field must NOT be applied "+
					"— the request is refused as a whole, never half-applied")
		})
	}
}

// TestUpdateTaskTool_RunningTask_MutableProgressFieldsStillSucceed is side
// two, and the one that actually distinguishes this fix from a blanket lock:
// the agent doing the work MUST still be able to record its progress while
// the task runs.
func TestUpdateTaskTool_RunningTask_MutableProgressFieldsStillSucceed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedFreezeTask(t, store, task.StatusInProgress)
	tool := NewTaskUpdateTool(store)

	// A progress write mid-run: result + artifacts, no frozen field in sight.
	res := tool.Execute(freezeToolCtx(), map[string]any{
		"task_id":   tk.ID,
		"result":    "two of three checks are green so far",
		"artifacts": []any{"out/report.txt"},
	})
	require.False(t, res.IsError,
		"recording progress on a running task must SUCCEED — the freeze is field-level, "+
			"not a lock on the task: %s", res.ForLLM)

	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "two of three checks are green so far", got.Result,
		"the progress write must have actually persisted, not merely been accepted")
	assert.Equal(t, []string{"out/report.txt"}, got.Artifacts,
		"artifacts written mid-run must persist")

	// A STATUS write mid-run: the working agent sets the status as the work
	// advances and must be able to. `failed` is used because it is a status a
	// running task can legitimately reach without the judge-claim staging path
	// a `done` claim on a criteria-bearing task deliberately takes.
	resStatus := tool.Execute(freezeToolCtx(), map[string]any{
		"task_id": tk.ID,
		"status":  string(task.StatusFailed),
	})
	require.False(t, resStatus.IsError,
		"a status update on a running task must SUCCEED — status is the record of progress, "+
			"never part of the judged contract: %s", resStatus.ForLLM)

	gotAfter, err := store.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusFailed, gotAfter.Status,
		"the status write must have actually persisted")
}

// TestUpdateTaskTool_NotRunning_FrozenFieldsAreEditable proves the gate is
// scoped to the RUNNING state rather than being a permanent ban: the same
// criteria edit the running task refused above is accepted on a task that is
// not running. Without this, a fix that simply rejected every criteria edit
// would look correct from the first test alone.
func TestUpdateTaskTool_NotRunning_FrozenFieldsAreEditable(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedFreezeTask(t, store, task.StatusNext)
	tool := NewTaskUpdateTool(store)

	res := tool.Execute(freezeToolCtx(), map[string]any{
		"task_id":  tk.ID,
		"criteria": []any{map[string]any{"text": "a different criterion entirely"}},
	})
	require.False(t, res.IsError,
		"editing criteria on a task that is NOT running must succeed: %s", res.ForLLM)

	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Len(t, got.Criteria, 1)
	assert.Equal(t, "a different criterion entirely", got.Criteria[0].Text,
		"the criteria edit must have landed on a non-running task")
}

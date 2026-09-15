// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_assignee_readiness_test.go pins the create_task / update_task half of
// the founder decision of 2026-09-15: an agent tool may not assign a task to an
// agent that cannot finish it. The refusal is the checker's own wording, carries
// a structured error naming the agent_id field, and writes nothing.
//
// The checker here is a small stand-in for pkg/agent's real answer that decides
// from its inputs (which agent, and whether the judged set holds a check), so
// each test also proves WHAT the tool asks about: the right agent, and the
// criteria AND the Definition of Done — submitted or stored.
package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const (
	readyAgent   = "agent-ready"
	noClaimAgent = "agent-no-claim"
	noBashAgent  = "agent-no-bash"

	noClaimReason = "Agent No Claim isn't allowed to report tasks as done. " +
		"Allow 'goal_claim' for it in Agents → Tools, or assign another agent."
	noBashReason = "Agent No Bash can't run this task's checks."
)

// fakeReadiness answers like the real check for three agents: noClaimAgent can
// never finish, noBashAgent cannot finish a task that holds a check, and every
// other agent can finish anything.
func fakeReadiness(agentID string, judged []task.AcceptanceCriterion) string {
	switch agentID {
	case noClaimAgent:
		return noClaimReason
	case noBashAgent:
		for _, c := range judged {
			if c.Kind == task.KindCheck {
				return noBashReason
			}
		}
	}
	return ""
}

func checkArg(command string) []any {
	return []any{map[string]any{
		"kind": "check", "text": "the check passes",
		"check": map[string]any{"command": command, "expected_exit_code": float64(0)},
	}}
}

func readinessTools(t *testing.T) goalLifecycleTools {
	t.Helper()
	tl := newGoalLifecycleTools2(t)
	tl.create.SetAssigneeReadinessChecker(fakeReadiness)
	tl.update.SetAssigneeReadinessChecker(fakeReadiness)
	return tl
}

func requireAssigneeRefusal(t *testing.T, res *ToolResult, wantAgent, wantReason string) {
	t.Helper()
	require.True(t, res.IsError, "the write must be refused: %s", res.ForLLM)
	assert.Equal(t, wantReason, res.ForLLM, "the model must read exactly the reason, naming the fix")
	var refusal *AssigneeCannotFinishError
	require.True(t, errors.As(res.Err, &refusal), "the refusal must carry the structured error, got %v", res.Err)
	assert.Equal(t, "agent_id", refusal.Field, "the refusal names the agent field")
	assert.Equal(t, wantAgent, refusal.AgentID)
	assert.Equal(t, wantReason, refusal.Reason)
}

func createArgs(agentID string, criteria, dod []any) map[string]any {
	return map[string]any{
		"title": "readiness", "prompt": "do it", "agent_id": agentID, "criteria": criteria, "dod": dod,
	}
}

// Given an assignee that cannot finish any task
// When create_task assigns it
// Then the create is refused with the reason and nothing is written.
func TestCreateTask_AssigneeCannotFinish_RefusedAndNothingWritten(t *testing.T) {
	t.Parallel()
	tl := readinessTools(t)

	res := tl.create.Execute(goalLifecycleCtx(),
		createArgs(noClaimAgent, proseArg("the report exists"), proseArg("the reviewer signed it off")))

	requireAssigneeRefusal(t, res, noClaimAgent, noClaimReason)
	all, err := tl.taskStore.List(task.Filter{WorkspaceID: "ws-goal-lifecycle"})
	require.NoError(t, err)
	assert.Empty(t, all, "a refused create must not persist a task")
	goals, _, err := tl.goalStore.List()
	require.NoError(t, err)
	assert.Empty(t, goals, "a refused create must not persist a goal record")
}

// Given an assignee that cannot run checks
// When the only check is in the Definition of Done
// Then the create is still refused — the Definition of Done is part of what the
// task is judged against.
func TestCreateTask_CheckInDefinitionOfDone_IsAskedAbout(t *testing.T) {
	t.Parallel()
	tl := readinessTools(t)

	res := tl.create.Execute(goalLifecycleCtx(),
		createArgs(noBashAgent, proseArg("the report exists"), checkArg("test -f report.md")))
	requireAssigneeRefusal(t, res, noBashAgent, noBashReason)

	ok := tl.create.Execute(goalLifecycleCtx(),
		createArgs(noBashAgent, proseArg("the report exists"), proseArg("the reviewer signed it off")))
	require.False(t, ok.IsError, "the same agent with no check to run is not refused: %s", ok.ForLLM)
}

// Given an agent that can finish the task
// When create_task assigns it
// Then the task is created.
func TestCreateTask_ReadyAssignee_Created(t *testing.T) {
	t.Parallel()
	tl := readinessTools(t)

	res := tl.create.Execute(goalLifecycleCtx(),
		createArgs(readyAgent, proseArg("the report exists"), checkArg("test -f report.md")))
	require.False(t, res.IsError, "a ready assignee must be accepted: %s", res.ForLLM)
	all, err := tl.taskStore.List(task.Filter{WorkspaceID: "ws-goal-lifecycle"})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, readyAgent, all[0].AgentID)
}

// Given a task assigned to a ready agent whose stored Definition of Done holds a
// check
// When update_task changes only the title, then reassigns to an agent that cannot
// finish it, then to one that cannot run the stored check
// Then the title edit is not asked about, both reassignments are refused, and the
// task keeps its agent.
func TestUpdateTask_ReassignmentToAnAgentThatCannotFinish_Refused(t *testing.T) {
	t.Parallel()
	tl := readinessTools(t)
	require.False(t, tl.create.Execute(goalLifecycleCtx(),
		createArgs(readyAgent, proseArg("the report exists"), checkArg("test -f report.md"))).IsError)
	all, err := tl.taskStore.List(task.Filter{WorkspaceID: "ws-goal-lifecycle"})
	require.NoError(t, err)
	require.Len(t, all, 1)
	id := all[0].ID

	title := tl.update.Execute(goalLifecycleCtx(), map[string]any{"task_id": id, "title": "renamed"})
	require.False(t, title.IsError, "an edit that changes neither agent nor judged set is not asked about: %s", title.ForLLM)

	res := tl.update.Execute(goalLifecycleCtx(), map[string]any{"task_id": id, "agent_id": noClaimAgent})
	requireAssigneeRefusal(t, res, noClaimAgent, noClaimReason)

	// Only the STORED Definition of Done holds the check here: the refusal
	// proves the update reads it off the paired goal record.
	res = tl.update.Execute(goalLifecycleCtx(), map[string]any{"task_id": id, "agent_id": noBashAgent})
	requireAssigneeRefusal(t, res, noBashAgent, noBashReason)

	stored, err := tl.taskStore.Get(id)
	require.NoError(t, err)
	assert.Equal(t, readyAgent, stored.AgentID, "a refused reassignment writes nothing")
	assert.Equal(t, "renamed", stored.Title)
}

// Given a task assigned to an agent that cannot run checks
// When update_task adds a check to its Definition of Done
// Then the edit is refused and the stored Definition of Done is unchanged.
func TestUpdateTask_AddingACheckTheAgentCannotRun_Refused(t *testing.T) {
	t.Parallel()
	tl := readinessTools(t)
	require.False(t, tl.create.Execute(goalLifecycleCtx(),
		createArgs(noBashAgent, proseArg("the report exists"), proseArg("the reviewer signed it off"))).IsError)
	all, err := tl.taskStore.List(task.Filter{WorkspaceID: "ws-goal-lifecycle"})
	require.NoError(t, err)
	require.Len(t, all, 1)
	id := all[0].ID

	res := tl.update.Execute(goalLifecycleCtx(), map[string]any{"task_id": id, "dod": checkArg("test -f report.md")})
	requireAssigneeRefusal(t, res, noBashAgent, noBashReason)

	g, err := tl.goalStore.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "the reviewer signed it off", g.DoD[0].Text, "a refused edit leaves the Definition of Done in place")
}

// An unwired checker never refuses: the task run's pre-run check is the gate,
// this is only the early feedback.
func TestAssigneeCannotFinishRefusal_UnwiredCheckerDoesNotRefuse(t *testing.T) {
	t.Parallel()
	assert.Nil(t, AssigneeCannotFinishRefusal("create_task", nil, noClaimAgent, nil))
	assert.Nil(t, AssigneeCannotFinishRefusal("create_task", fakeReadiness, "", nil),
		"an unassigned task has no assignee to ask about")
	_ = context.Background()
}

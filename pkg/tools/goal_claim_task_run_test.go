// Omnipus — goal_claim on a task run (founder decision 2026-09-14; UAT B-5).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// claimAccess is a GoalRecordAccess that ALSO implements GoalClaimAccess, the
// shape production wires: ReadGoalState answers the owner-keyed question
// (nothing for a task-owned goal) while ReadClaimableGoal answers by the goal
// bound to the session.
type claimAccess struct {
	*fakeGoalRecordAccess
	bound map[string]string // sessionID -> goalID of the goal bound to it
}

func (c claimAccess) ReadClaimableGoal(sessionID string) (string, string, error) {
	if gid, ok := c.bound[sessionID]; ok {
		return gid, "ship the invoice report", nil
	}
	return "", "", nil
}

// TestGoalClaim_FindsATaskGoalByTheSessionItIsBoundTo is the B-5 defect: a
// task-owned goal is owned by the TASK, so the owner-keyed lookup finds
// nothing from the run's session and every task-run claim was refused with
// "this session has no active goal".
func TestGoalClaim_FindsATaskGoalByTheSessionItIsBoundTo(t *testing.T) {
	const sessionID = "session_task_run_1"
	access := claimAccess{fakeGoalRecordAccess: newFakeGoalRecordAccess(), bound: map[string]string{sessionID: "goal-task-1"}}
	tool := NewGoalClaimTool(func() GoalRecordAccess { return access })

	res := tool.Execute(setGoalCtx(sessionID, "worker"), map[string]any{"status": "met", "evidence": "report written and re-read"})
	if res.IsError {
		t.Fatalf("a task run's claim must find the goal bound to its session: %s", res.ForLLM)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("payload: %v (%s)", err, res.ForLLM)
	}
	if payload["goal_id"] != "goal-task-1" {
		t.Errorf("goal_id = %v, want the bound task goal", payload["goal_id"])
	}

	res = tool.Execute(setGoalCtx("session_with_nothing_bound", "worker"), map[string]any{"status": "blocked"})
	if !res.IsError || !strings.Contains(res.ForLLM, "no active goal") {
		t.Errorf("a session with no bound goal must still be refused: %s", res.ForLLM)
	}
}

// TestGoalClaim_TaskRunAtDelegationDepthCanClaim: every agent-created task
// carries a delegation generation >= 1 on its run's context, so the plain
// depth refusal made those tasks unable to ever claim. A task run's own turn
// (the context names its running task) is exempt; a delegated sub-turn is not.
func TestGoalClaim_TaskRunAtDelegationDepthCanClaim(t *testing.T) {
	const sessionID = "session_task_run_depth"
	access := claimAccess{fakeGoalRecordAccess: newFakeGoalRecordAccess(), bound: map[string]string{sessionID: "goal-task-2"}}
	tool := NewGoalClaimTool(func() GoalRecordAccess { return access })

	taskRun := WithRunningTaskID(WithDelegationDepth(setGoalCtx(sessionID, "worker"), 1), "task-2")
	if res := tool.Execute(taskRun, map[string]any{"status": "met", "evidence": "done and checked"}); res.IsError {
		t.Fatalf("a task run at delegation depth 1 must be able to claim its own goal: %s", res.ForLLM)
	}

	subTurn := WithDelegationDepth(setGoalCtx(sessionID, "worker"), 1)
	res := tool.Execute(subTurn, map[string]any{"status": "met", "evidence": "done and checked"})
	if !res.IsError || !strings.Contains(res.ForLLM, "owner-session-only") {
		t.Errorf("a delegated sub-turn must still be refused: %s", res.ForLLM)
	}
}

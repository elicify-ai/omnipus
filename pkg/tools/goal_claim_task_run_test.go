// Omnipus — goal_claim on a task run (founder decision 2026-09-14; UAT B-5).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// claimAccess is a GoalRecordAccess that ALSO implements GoalClaimAccess, the
// shape production wires: ReadGoalState answers the owner-keyed question
// (nothing for a task-owned goal) while ReadClaimableGoal answers by the goal
// bound to the session. taskSession records the task's own run session,
// independently of the transcript session a caller tries to claim for.
type claimAccess struct {
	*fakeGoalRecordAccess
	bound       map[string]string // sessionID -> goalID of the goal bound to it
	taskSession map[string]string // taskID -> the task's own run sessionID
}

func (c claimAccess) ReadClaimableGoal(sessionID string) (string, string, error) {
	if gid, ok := c.bound[sessionID]; ok {
		return gid, "ship the invoice report", nil
	}
	return "", "", nil
}

// ReadTaskSessionID exposes the task-owned session that goal_claim must check
// before granting the running-task delegation exception (#1026).
func (c claimAccess) ReadTaskSessionID(taskID string) (string, error) {
	return c.taskSession[taskID], nil
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
	const taskID = "task-2"
	access := claimAccess{
		fakeGoalRecordAccess: newFakeGoalRecordAccess(),
		bound:                map[string]string{sessionID: "goal-task-2"},
		taskSession:          map[string]string{taskID: sessionID},
	}
	tool := NewGoalClaimTool(func() GoalRecordAccess { return access })

	taskRun := WithRunningTaskID(WithDelegationDepth(setGoalCtx(sessionID, "worker"), 1), taskID)
	res := tool.Execute(taskRun, map[string]any{"status": "met", "evidence": "done and checked"})
	if res.IsError {
		t.Fatalf("a task run at delegation depth 1 must be able to claim its own goal: %s", res.ForLLM)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("own-task claim payload: %v (%q)", err, res.ForLLM)
	}
	if payload["status"] != "met" || payload["evidence"] != "done and checked" || payload["goal_id"] != "goal-task-2" {
		t.Fatalf("own-task claim = %v, want status met, evidence done and checked, goal_id goal-task-2", payload)
	}

	subTurn := WithDelegationDepth(setGoalCtx(sessionID, "worker"), 1)
	res = tool.Execute(subTurn, map[string]any{"status": "met", "evidence": "done and checked"})
	if !res.IsError || !strings.Contains(res.ForLLM, "owner-session-only") {
		t.Errorf("a delegated sub-turn must still be refused: %s", res.ForLLM)
	}
}

// TestGoalClaim_TaskContextBoundToAnotherSessionRefuses reproduces #1026:
// an inherited running-task ID does not make a delegated sub-turn the OWNER
// of the transcript session whose goal it is trying to claim. The task's
// actual run session must match the claim's transcript session at any depth.
func TestGoalClaim_TaskContextBoundToAnotherSessionRefuses(t *testing.T) {
	const ownerSessionID = "session_goal_owner"
	const taskSessionID = "session_running_task_worker"
	const runningTaskID = "task-running-elsewhere"
	for _, depth := range []int{1, 2} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			access := claimAccess{
				fakeGoalRecordAccess: newFakeGoalRecordAccess(),
				bound:                map[string]string{ownerSessionID: "goal-owner-1026"},
				taskSession:          map[string]string{runningTaskID: taskSessionID},
			}
			tool := NewGoalClaimTool(func() GoalRecordAccess { return access })
			args := map[string]any{"status": "met", "evidence": "x"}
			delegated := WithDelegationDepth(setGoalCtx(ownerSessionID, "worker"), depth)

			// The existing depth-only refusal is the oracle for the requirement
			// that an unrelated task context produce the SAME owner-session error.
			depthOnly := tool.Execute(delegated, args)
			if !depthOnly.IsError || !strings.Contains(depthOnly.ForLLM, "goal_claim is owner-session-only") {
				t.Fatalf("depth-only control must refuse as owner-session-only, got error=%v result=%q", depthOnly.IsError, depthOnly.ForLLM)
			}

			foreignTask := WithRunningTaskID(delegated, runningTaskID)
			res := tool.Execute(foreignTask, args)
			if !res.IsError {
				t.Fatalf("depth %d task %q belongs to %q, not goal owner %q: want owner-session refusal, got success %q",
					depth, runningTaskID, taskSessionID, ownerSessionID, res.ForLLM)
			}
			if res.ForLLM != depthOnly.ForLLM {
				t.Fatalf("depth %d foreign-task refusal = %q, want same owner-session-only message %q", depth, res.ForLLM, depthOnly.ForLLM)
			}
		})
	}
}

// A steered helper launched WITH its own goal runs at delegation depth >= 1 (so
// set_goal stays refused) but owns that goal: goal_claim on its own session is
// allowed. A sub-turn that merely runs under another session's goal is not.
func TestGoalClaim_HelperClaimsItsOwnLaunchGoalButNotAnotherSessions(t *testing.T) {
	const own = "session_helper_own"
	access := claimAccess{fakeGoalRecordAccess: newFakeGoalRecordAccess(), bound: map[string]string{own: "goal-helper-1", "session_parent": "goal-parent"}}
	tool := NewGoalClaimTool(func() GoalRecordAccess { return access })
	args := map[string]any{"status": "met", "evidence": "done and checked"}

	helper := WithOwnGoalSession(WithDelegationDepth(setGoalCtx(own, "ava"), 1), own)
	if res := tool.Execute(helper, args); res.IsError {
		t.Fatalf("a helper must be able to claim its own launch goal: %s", res.ForLLM)
	}

	// The marker names a different session than the one the turn runs in.
	other := WithOwnGoalSession(WithDelegationDepth(setGoalCtx("session_parent", "ava"), 1), own)
	if res := tool.Execute(other, args); !res.IsError || !strings.Contains(res.ForLLM, "owner-session-only") {
		t.Fatalf("a marker for another session must not let a sub-turn claim this session's goal: %s", res.ForLLM)
	}

	// No marker: the plain delegated sub-turn is still refused.
	plain := WithDelegationDepth(setGoalCtx(own, "ava"), 1)
	if res := tool.Execute(plain, args); !res.IsError || !strings.Contains(res.ForLLM, "owner-session-only") {
		t.Fatalf("a delegated sub-turn without the marker must stay refused: %s", res.ForLLM)
	}
}

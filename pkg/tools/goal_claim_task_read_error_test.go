// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A matching value is not authority when its read also failed. This seam
// represents that adversarial access-layer result without changing the real
// goal_claim tool or the healthy task-session mapping used by its control.
type claimTaskReadErrorAccess struct {
	claimAccess
	readErr error
}

func (c *claimTaskReadErrorAccess) ReadTaskSessionID(taskID string) (string, error) {
	return c.taskSession[taskID], c.readErr
}

// Oracle: #1026 requires a failed task-session read to refuse, even if the
// returned value matches the caller. Retain the original cause, not just text.
func TestGoalClaim_TaskSessionReadErrorFailsClosedEvenWithMatchingValue(t *testing.T) {
	const sessionID = "session_task_binding_read_error"
	const taskID = "task-binding-read-error"
	cause := errors.New("task session binding could not be read from disk")
	for _, depth := range []int{1, 2} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			access := &claimTaskReadErrorAccess{claimAccess: claimAccess{
				fakeGoalRecordAccess: newFakeGoalRecordAccess(),
				bound:                map[string]string{sessionID: "goal-read-error-control"},
				taskSession:          map[string]string{taskID: sessionID},
			}}
			tool := NewGoalClaimTool(func() GoalRecordAccess { return access })
			ctx := WithRunningTaskID(WithDelegationDepth(setGoalCtx(sessionID, "worker"), depth), taskID)
			args := map[string]any{"status": "met", "evidence": "report written and checked"}
			control := tool.Execute(ctx, args)
			if control.IsError || control.Err != nil {
				t.Fatalf("healthy matching-session control must claim: error=%v cause=%v result=%q", control.IsError, control.Err, control.ForLLM)
			}

			access.readErr = fmt.Errorf("task binding access: %w", cause)
			returned, readErr := access.ReadTaskSessionID(taskID)
			if returned != sessionID || !errors.Is(readErr, cause) {
				t.Fatalf("fault instrument returned %q / %v, want caller-matching session and the read cause", returned, readErr)
			}
			res := tool.Execute(ctx, args)
			if !res.IsError {
				t.Fatalf("depth %d matching value with a failed read must refuse, got success %q", depth, res.ForLLM)
			}
			if !errors.Is(res.Err, cause) {
				t.Errorf("underlying refusal = %v, want retained read cause %v", res.Err, cause)
			}
			if !strings.Contains(res.ForLLM, access.readErr.Error()) {
				t.Errorf("visible refusal = %q, want full access-layer read error %q", res.ForLLM, access.readErr.Error())
			}
			if access.writes != 0 {
				t.Errorf("goal record writes = %d, want 0 after refusing the claim", access.writes)
			}
		})
	}
}

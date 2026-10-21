package tools

import (
	"context"
	"fmt"
	"strings"
)

// GoalActivationFn connects set_goal to the engine's ordinary goal activation.
// Execute invokes it only after the complete record passed validation.
type GoalActivationFn func(ctx context.Context, sessionID, definition string) error

func (t *SetGoalTool) SetActivationFn(fn GoalActivationFn) { t.activateFn = fn }

func (sg *setGoalToolExecute) activateGoalIfNeeded() (*ToolResult, bool) {
	if !sg.needsActivation {
		return nil, false
	}
	if sg.t.activateFn == nil {
		return ErrorResult("set_goal: goal activation is not wired on this deployment"), true
	}
	if err := sg.t.activateFn(sg.ctx, sg.sessionID, sg.definition); err != nil {
		return ErrorResult(fmt.Sprintf("set_goal: could not start this session's goal: %v", err)).WithError(err), true
	}
	goalID, condition, _, err := sg.access.ReadGoalState(sg.sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("set_goal: could not read the activated goal: %v", err)).WithError(err), true
	}
	if goalID == "" || strings.TrimSpace(condition) == "" {
		return ErrorResult("set_goal: activation did not produce an active session-owned goal"), true
	}
	sg.goalID = goalID
	sg.oldRec.Intent, sg.oldRec.Prompt = condition, condition
	return nil, false
}

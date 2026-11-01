// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_update_retry_limit.go — update_task's max_attempts / clear_max_attempts
// (tool-parity spec r17 §7.2 TK-05, §7.11, dataset D-3).

// definitionCreatorOnlyText is the §7.11 refusal when an assignee that did
// not create the task sends a definition field.
const definitionCreatorOnlyText = "Only the agent that created this task can change what it asks for " +
	"(instruction, criteria, definition of done or retry limit). Ask the creating agent or a person."

// SetPlanningConfigFn installs the live planning-configuration reader used for
// the max_attempts ceiling. Read at every call, never captured.
func (t *TaskUpdateTool) SetPlanningConfigFn(fn PlanningConfigFn) {
	t.planningFn = fn
}

// applyRetryLimit adds max_attempts or clear_max_attempts to the patch. The
// retry limit is in the definition group (creator only), mutable while the
// task runs, at least 1, and at most the live global planning.
// task_max_attempts — the ceiling wins over lowering a higher stored value.
// Clearing to inherit is always accepted from the creator.
func (tu *taskUpdateToolExecute) applyRetryLimit() (*ToolResult, bool) {
	value, present, refusal := wholeNumberArg(tu.args, "max_attempts")
	clearRaw, clearPresent := tu.args["clear_max_attempts"]
	clearOn, _ := clearRaw.(bool)
	if clearPresent && clearRaw != nil {
		if _, isBool := clearRaw.(bool); !isBool {
			return ErrorResult("clear_max_attempts must be true or false"), true
		}
	}
	if !present && !clearOn {
		return nil, false
	}
	// Creator-only authority is checked for the whole definition group in
	// validateAndLoad (checkDefinitionAuthority), before anything is built.
	if present && clearOn {
		return ErrorResult("max_attempts and clear_max_attempts cannot be combined: send one of them"), true
	}
	if clearOn {
		var cleared *int
		tu.patch.MaxAttempts = &cleared
		tu.updatedFields = append(tu.updatedFields, "max_attempts")
		return nil, false
	}
	if refusal != "" {
		return ErrorResult(refusal), true
	}
	if global := livePlanning(tu.t.planningFn).EffectiveTaskMaxAttempts(nil); value > global {
		return ErrorResult(aboveGlobalLimitText("max_attempts", value, global)), true
	}
	v := value
	ptr := &v
	tu.patch.MaxAttempts = &ptr
	tu.updatedFields = append(tu.updatedFields, "max_attempts")
	return nil, false
}

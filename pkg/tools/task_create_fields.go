// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_create_fields.go — create_task's description, tags, run_isolated and
// max_attempts (tool-parity spec r17 §7.2 TK-02, TK-05, TK-08, TK-10: added to
// create as well as update). Bounds are the store's (description ≤2000, tags
// ≤16 each ≤64, normalised; max_attempts ≥1), so REST and the tool refuse the
// same values with the same words; the agent ceiling on max_attempts is the
// live global planning.task_max_attempts, as on update_task.

// SetPlanningConfigFn installs the live planning-configuration reader used for
// the max_attempts ceiling. Read at every call, never captured.
func (t *TaskCreateTool) SetPlanningConfigFn(fn PlanningConfigFn) {
	t.planningFn = fn
}

// stringListArg reads an optional JSON array of strings. present is false when
// the key is absent or null; refusal names the field when the shape is wrong.
func stringListArg(args map[string]any, key string) (list []string, present bool, refusal string) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false, ""
	}
	items, isList := raw.([]any)
	if !isList {
		return nil, true, key + " must be a list of strings"
	}
	list = make([]string, 0, len(items))
	for _, item := range items {
		s, isString := item.(string)
		if !isString {
			return nil, true, key + " must be a list of strings"
		}
		list = append(list, s)
	}
	return list, true, ""
}

// applyCreateFields sets the optional TK-02/05/08/10 fields on the new task.
// The store validates the bounds at Create; a refused value writes nothing.
func (tc *taskCreateToolExecute) applyCreateFields() (*ToolResult, bool) {
	if raw, present := tc.args["description"]; present && raw != nil {
		description, ok := raw.(string)
		if !ok {
			return ErrorResult("description must be a string"), true
		}
		tc.entity.Description = description
	}
	tags, present, refusal := stringListArg(tc.args, "tags")
	if refusal != "" {
		return ErrorResult(refusal), true
	}
	if present {
		tc.entity.Tags = tags
	}
	if raw, sent := tc.args["run_isolated"]; sent && raw != nil {
		isolated, ok := raw.(bool)
		if !ok {
			return ErrorResult("run_isolated must be true or false"), true
		}
		tc.entity.RunIsolated = isolated
	}
	value, present, refusal := wholeNumberArg(tc.args, "max_attempts")
	if refusal != "" {
		return ErrorResult(refusal), true
	}
	if present {
		if global := livePlanning(tc.t.planningFn).EffectiveTaskMaxAttempts(nil); value > global {
			return ErrorResult(aboveGlobalLimitText("max_attempts", value, global)), true
		}
		tc.entity.MaxAttempts = &value
	}
	return nil, false
}

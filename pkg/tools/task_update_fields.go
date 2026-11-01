// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_update_fields.go — update_task's definition authority and organisation
// fields (tool-parity spec r17 §7.2 TK-01, TK-02, TK-03, TK-08, TK-10, TK-13;
// §7.7 authority matrix; §7.11; dataset D-1). Field bounds are the store's
// (title 1–200, description ≤2000, prompt ≤10000, tags ≤16 each ≤64,
// normalised) so REST and the tool refuse the same values with the same words.

import (
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// definitionFieldSent reports whether the call sends any field of the task
// definition group (prompt, criteria, dod, max_attempts, clear_max_attempts:
// true). Presence uses the same type assertions the apply paths use, so the
// authority gate refuses exactly what would otherwise be written.
func definitionFieldSent(args map[string]any) bool {
	if _, ok := args["prompt"].(string); ok {
		return true
	}
	if _, ok := args["criteria"].([]any); ok {
		return true
	}
	if _, ok := args["dod"].([]any); ok {
		return true
	}
	if v, ok := args["max_attempts"]; ok && v != nil {
		return true
	}
	clearOn, _ := args["clear_max_attempts"].(bool)
	return clearOn
}

// checkDefinitionAuthority refuses a definition field from any caller that is
// not the task's creator agent (§7.7): an assignee that did not create the
// task, and the assignee of a person-created task, may not change what it
// asks for.
func (tu *taskUpdateToolExecute) checkDefinitionAuthority() (*ToolResult, bool) {
	if definitionFieldSent(tu.args) && !tu.existing.CreatedByAgent(tu.callerID) {
		return ErrorResult(definitionCreatorOnlyText), true
	}
	return nil, false
}

// applyOrganisationFields adds title, description, prompt, tags,
// run_isolated and due / clear_due to the patch. The store validates every
// bound under its own lock; a refused value writes nothing.
func (tu *taskUpdateToolExecute) applyOrganisationFields() (*ToolResult, bool) {
	if raw, present := tu.args["title"]; present && raw != nil {
		title, ok := raw.(string)
		if !ok || strings.TrimSpace(title) == "" {
			// TK-01: an empty or whitespace-only title is refused, never ignored.
			return ErrorResult("title must not be empty"), true
		}
		tu.patch.Title = &title
		tu.updatedFields = append(tu.updatedFields, "title")
	}
	if description, ok := tu.args["description"].(string); ok {
		tu.patch.Description = &description
		tu.updatedFields = append(tu.updatedFields, "description")
	}
	if prompt, ok := tu.args["prompt"].(string); ok {
		tu.patch.Prompt = &prompt
		tu.updatedFields = append(tu.updatedFields, "prompt")
	}
	if raw, present := tu.args["tags"]; present && raw != nil {
		items, ok := raw.([]any)
		if !ok {
			return ErrorResult("tags must be a list of strings"), true
		}
		tags := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return ErrorResult("tags must be a list of strings"), true
			}
			tags = append(tags, s)
		}
		tu.patch.Tags = &tags
		tu.updatedFields = append(tu.updatedFields, "tags")
	}
	if raw, present := tu.args["run_isolated"]; present && raw != nil {
		isolated, ok := raw.(bool)
		if !ok {
			return ErrorResult("run_isolated must be true or false"), true
		}
		tu.patch.RunIsolated = &isolated
		tu.updatedFields = append(tu.updatedFields, "run_isolated")
	}
	return tu.applyDue()
}

// applyDue sets due (RFC 3339) or clears it with clear_due: true; a due value
// in the same call wins over clear_due (TK-13).
func (tu *taskUpdateToolExecute) applyDue() (*ToolResult, bool) {
	if due, ok := tu.args["due"].(string); ok && due != "" {
		if _, err := time.Parse(time.RFC3339, due); err != nil {
			return ErrorResult(fmt.Sprintf("invalid due date %q (must be RFC 3339): %v", due, err)), true
		}
		tu.patch.Due = &due
		tu.updatedFields = append(tu.updatedFields, "due")
		return nil, false
	}
	if raw, present := tu.args["clear_due"]; present && raw != nil {
		clearOn, ok := raw.(bool)
		if !ok {
			return ErrorResult("clear_due must be true or false"), true
		}
		if clearOn {
			none := ""
			tu.patch.Due = &none
			tu.updatedFields = append(tu.updatedFields, "due")
		}
	}
	return nil, false
}

// SetPlanStore installs the plan store plan_id attach / re-parent is checked
// against (r17 TK-11).
func (t *TaskUpdateTool) SetPlanStore(store *plan.Store) {
	t.planStore = store
}

// applyPlanMembership adds a plan_id change through the shared rule
// (PlanMembershipChange): attach or re-parent only to a draft plan of the
// task's own workspace; detach always, returning the task to inbox unless the
// call sets a status itself.
func (tu *taskUpdateToolExecute) applyPlanMembership() (*ToolResult, bool) {
	raw, present := tu.args["plan_id"]
	if !present || raw == nil {
		return nil, false
	}
	planID, ok := raw.(string)
	if !ok {
		return ErrorResult("plan_id must be a string"), true
	}
	planID = strings.TrimSpace(planID)
	detachToInbox, err := PlanMembershipChange(tu.t.planStore, tu.existing.WorkspaceID, planID, tu.patch.Status != nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("update_task failed: plan_id: %v", err)), true
	}
	if detachToInbox {
		inbox := task.StatusInbox
		tu.patch.Status = &inbox
		tu.detachResets = true
	}
	tu.patch.PlanID = &planID
	tu.updatedFields = append(tu.updatedFields, "plan_id")
	return nil, false
}

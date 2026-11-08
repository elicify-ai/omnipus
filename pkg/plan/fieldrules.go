// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// fieldrules.go — Table T-PLAN, the shared field rule table for plans
// (tool-parity spec r17 §7.1, §7.3, §7.7). Read by the REST create and PUT
// handlers, create_plan, update_plan and the parity guard, so the draft-only
// decision lives in one place. Paths are canonical (r17 §7.1 rule 1).

// Operation names which write a row applies to (r17 §7.1 rule 4).
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpBoth   = "both"
)

// FieldRule is one row of Table T-PLAN.
type FieldRule struct {
	Row          string
	Property     string
	Person       string
	Tool         string
	DraftOnly    bool
	AgentCeiling string
	Reason       string
}

// FieldMapping pairs a REST request leaf with a tool parameter leaf.
type FieldMapping struct {
	RESTPath string
	Tool     string
	ToolPath string
	Ops      string
}

// FieldExclusion is a one-sided leaf with its reason.
type FieldExclusion struct {
	Side   string
	Path   string
	Ops    string
	Reason string
}

var planFieldRules = []FieldRule{
	{Row: "PL-01", Property: "workspace_id", Person: "create", Tool: "create_plan"},
	{Row: "PL-02", Property: "title", Person: "create, update", Tool: "create_plan, update_plan (any state)"},
	{Row: "PL-03", Property: "objective", Person: "create, update", Tool: "create_plan, update_plan (any state)"},
	{Row: "PL-04", Property: "description", Person: "create, update", Tool: "create_plan, update_plan"},
	{Row: "PL-06", Property: "dod", Person: "create, update; frozen after draft", Tool: "create_plan, update_plan (draft only)", DraftOnly: true},
	{Row: "PL-05", Property: "owner_agent_id", Person: "create, update; frozen after draft", Tool: "create_plan, update_plan (draft only)", DraftOnly: true},
	{Row: "PL-07", Property: "rationale", Person: "create", Tool: "create_plan"},
	{Row: "PL-08", Property: "bounds", Person: "create, update (merged; not frozen)", Tool: "create_plan, update_plan",
		AgentCeiling: "each bound at most its effective global planning value"},
	{Row: "PL-09", Property: "state", Person: "dedicated endpoints", Reason: "state changes through the dedicated execute and stop tools, never a state parameter"},
}

// FieldRules returns Table T-PLAN in row order (a copy).
func FieldRules() []FieldRule {
	return append([]FieldRule(nil), planFieldRules...)
}

// DraftOnlyProperties lists the properties that may change only while the
// plan is in draft (PL-05, PL-06), in table order: REST PUT and update_plan
// refuse the first one sent with LeftDraftRefusal.
func DraftOnlyProperties() []string {
	var out []string
	for _, rule := range planFieldRules {
		if rule.DraftOnly {
			out = append(out, rule.Property)
		}
	}
	return out
}

// LeftDraftRefusal is the shared refusal for a draft-only property sent to a
// plan that has left draft.
func LeftDraftRefusal(property string) string {
	return property + " cannot be changed once the plan has left draft state"
}

// FirstFrozenAfterDraft returns the first draft-only property sent to a plan
// in state, or "" when none is refused.
func FirstFrozenAfterDraft(state State, sent func(property string) bool) string {
	if state == StateDraft {
		return ""
	}
	for _, property := range DraftOnlyProperties() {
		if sent(property) {
			return property
		}
	}
	return ""
}

var planCriterionLeaves = []string{
	"behavior.max_count", "behavior.min_count", "behavior.scope", "behavior.tool",
	"check.command", "check.expected_exit_code", "kind", "text",
}

var planCriterionRecordLeaves = []string{
	"author.id", "author.kind", "clause_count", "id", "judgment", "provenance", "status",
}

// MappingRows returns the REST↔tool leaf mappings for plans.
func MappingRows() []FieldMapping {
	shared := make([]string, 0, 8+len(planCriterionLeaves))
	shared = append(shared, []string{
		"bounds.idle_expiry_days", "bounds.plan_judge_max_rounds", "bounds.supervision_max_attempts",
		"bounds.supervision_turn_timeout_seconds", "description", "objective", "owner_agent_id", "title",
	}...)
	for _, leaf := range planCriterionLeaves {
		shared = append(shared, "dod[]."+leaf)
	}
	rows := make([]FieldMapping, 0, 2*len(shared)+2)
	for _, leaf := range shared {
		rows = append(rows,
			FieldMapping{RESTPath: leaf, Tool: "create_plan", ToolPath: leaf, Ops: OpCreate},
			FieldMapping{RESTPath: leaf, Tool: "update_plan", ToolPath: leaf, Ops: OpUpdate})
	}
	for _, leaf := range []string{"rationale", "workspace_id"} {
		rows = append(rows, FieldMapping{RESTPath: leaf, Tool: "create_plan", ToolPath: leaf, Ops: OpCreate})
	}
	return rows
}

// ExcludedRows returns the plan leaves that deliberately have no counterpart.
func ExcludedRows() []FieldExclusion {
	rows := make([]FieldExclusion, 0, 2+len(planCriterionRecordLeaves))
	rows = append(rows, []FieldExclusion{
		{Side: "REST", Path: "state", Ops: OpUpdate, Reason: "PL-09: state changes through the dedicated execute and stop tools"},
		{Side: "tool", Path: "plan_id", Ops: OpUpdate, Reason: "addresses the plan; REST carries it in the path /plans/{id}"},
	}...)
	for _, leaf := range planCriterionRecordLeaves {
		rows = append(rows, FieldExclusion{Side: "REST", Path: "dod[]." + leaf, Ops: OpBoth,
			Reason: "criterion record metadata: the tool records the calling agent as author and leaves id, status, " +
				"provenance, judgment and clause count to the store and the Judge"})
	}
	return rows
}

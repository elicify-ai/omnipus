// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// fieldrules.go — Table T-TASK, the shared field rule table for tasks
// (tool-parity spec r17 §7.1, §7.2, §7.7). Read by the REST create and patch
// handlers, create_task, update_task and the parity guard, so the creator-only
// and running-freeze decisions live in one place.
//
// Paths are canonical (r17 §7.1 rule 1): dotted JSON names, "[]" for an array
// element, "{}" for a map value. A rule row names a top-level property; a
// mapping or excluded row names a leaf.

// FieldGroup is the §7.7 authority group a task property belongs to.
type FieldGroup string

const (
	// GroupDefinition: creator agent (and people) only (§7.7).
	GroupDefinition FieldGroup = "definition"
	// GroupOrganisation: creator or assignee agent (and people).
	GroupOrganisation FieldGroup = "organisation"
	// GroupProgress: status, result, artifacts (store transitions apply).
	GroupProgress FieldGroup = "progress"
	// GroupExcluded: not on the agent tool surface; the row's Reason says why.
	GroupExcluded FieldGroup = "excluded"
)

// Operation names which write a row applies to (r17 §7.1 rule 4).
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpBoth   = "both"
)

// FieldRule is one row of Table T-TASK: the property, who may set it and
// when, its tool exposure, the agent ceiling, and a reason for every excluded
// or one-sided property.
type FieldRule struct {
	Row                string
	Property           string
	Person             string
	Tool               string
	Group              FieldGroup
	FrozenWhileRunning bool
	AgentCeiling       string
	Reason             string
}

// FieldMapping pairs a REST request leaf with a tool parameter leaf for one
// operation (r17 §7.1 rule 4). A flattened tool parameter maps explicitly.
type FieldMapping struct {
	RESTPath string
	Tool     string
	ToolPath string
	Ops      string
}

// FieldExclusion is a leaf on one side ("REST" or "tool") that deliberately
// has no counterpart for the operation, with its reason.
type FieldExclusion struct {
	Side   string
	Path   string
	Ops    string
	Reason string
}

var taskFieldRules = []FieldRule{
	{Row: "TK-01", Property: "title", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-02", Property: "description", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-03", Property: "prompt", Person: "create, update; frozen while running", Tool: "create_task, update_task", Group: GroupDefinition, FrozenWhileRunning: true},
	{Row: "TK-04", Property: "criteria", Person: "create, update; frozen while running", Tool: "create_task, update_task", Group: GroupDefinition, FrozenWhileRunning: true},
	{Row: "TK-04", Property: "dod", Person: "create, update; frozen while running", Tool: "create_task, update_task", Group: GroupDefinition, FrozenWhileRunning: true},
	{Row: "TK-05", Property: "max_attempts", Person: "create, update; mutable while running", Tool: "create_task, update_task (clear_max_attempts clears)", Group: GroupDefinition, AgentCeiling: "planning.task_max_attempts"},
	{Row: "TK-06", Property: "action", Person: "create (llm only)", Group: GroupExcluded, Reason: "single value: every task is an llm task"},
	{Row: "TK-07", Property: "agent_id", Person: "create, update", Tool: "create_task, update_task (delegation gate)", Group: GroupOrganisation},
	{Row: "TK-08", Property: "run_isolated", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-09", Property: "priority", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-09", Property: "blocked_by", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-10", Property: "tags", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-11", Property: "plan_id", Person: "create, update", Tool: "create_task, update_task (shared membership helper)", Group: GroupOrganisation},
	{Row: "TK-12", Property: "write_set", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-12", Property: "stream", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-12", Property: "is_join", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
	{Row: "TK-13", Property: "due", Person: "create, update (clear_due clears)", Tool: "create_task, update_task (clear_due clears)", Group: GroupOrganisation},
	{Row: "TK-14", Property: "status", Person: "update", Tool: "update_task (inbox, next, done, failed)", Group: GroupProgress},
	{Row: "TK-15", Property: "result", Person: "update", Tool: "update_task", Group: GroupProgress},
	{Row: "TK-15", Property: "artifacts", Person: "update", Tool: "update_task", Group: GroupProgress},
	{Row: "TK-16", Property: "todos", Person: "create, update", Group: GroupExcluded, Reason: "agents write todos through set_todos (#1269)"},
	{Row: "TK-17", Property: "surface", Person: "create, update", Group: GroupExcluded, Reason: "heartbeats are a workspace feature"},
	{Row: "TK-18", Property: "source_channel", Person: "create", Group: GroupExcluded, Reason: "provenance: the tool records the calling turn's channel"},
	{Row: "TK-18", Property: "source_chat_id", Person: "create", Group: GroupExcluded, Reason: "provenance: the tool records the calling turn's chat"},
	{Row: "TK-19", Property: "started_at", Person: "update", Group: GroupExcluded, Reason: "engine record"},
	{Row: "TK-19", Property: "completed_at", Person: "update", Group: GroupExcluded, Reason: "engine record"},
	{Row: "TK-20", Property: "parent_task_id", Person: "create", Tool: "create_task", Group: GroupOrganisation},
	{Row: "TK-21", Property: "workspace_id", Person: "create; immutable", Tool: "create_task; assertion only on update_task", Group: GroupOrganisation},
	{Row: "TK-30", Property: "trigger", Person: "create, update", Tool: "create_task, update_task", Group: GroupOrganisation},
}

// FieldRules returns Table T-TASK in row order (a copy).
func FieldRules() []FieldRule {
	return append([]FieldRule(nil), taskFieldRules...)
}

func propertiesWhere(keep func(FieldRule) bool) []string {
	var out []string
	for _, rule := range taskFieldRules {
		if keep(rule) {
			out = append(out, rule.Property)
		}
	}
	return out
}

// CreatorOnlyProperties lists the definition group (§7.7): only the task's
// creator agent, or a person, may change these.
func CreatorOnlyProperties() []string {
	return propertiesWhere(func(r FieldRule) bool { return r.Group == GroupDefinition })
}

// FrozenWhileRunningProperties lists the properties no caller may change while
// the task is running (TK-03, TK-04), in table order: REST patch and
// update_task refuse exactly this set.
func FrozenWhileRunningProperties() []string {
	return propertiesWhere(func(r FieldRule) bool { return r.FrozenWhileRunning })
}

// SentProperties returns the members of properties for which sent reports
// true, in the given order.
func SentProperties(properties []string, sent func(property string) bool) []string {
	var out []string
	for _, property := range properties {
		if sent(property) {
			out = append(out, property)
		}
	}
	return out
}

// criterionLeaves are the agent-authored leaves of one criteria or dod entry.
var criterionLeaves = []string{
	"behavior.max_count", "behavior.min_count", "behavior.scope", "behavior.tool",
	"check.command", "check.expected_exit_code", "kind", "text",
}

// criterionRecordLeaves are criterion record fields the REST body carries and
// the tool does not take.
var criterionRecordLeaves = []string{
	"author.id", "author.kind", "clause_count", "id", "judgment", "provenance", "status",
}

const criterionRecordReason = "criterion record metadata: the tool records the calling agent as author and " +
	"leaves id, status, provenance, judgment and clause count to the store and the Judge"

// sharedTaskLeaves are the leaves both create surfaces and both update
// surfaces name identically.
func sharedTaskLeaves() []string {
	leaves := make([]string, 0, 19+2*len(criterionLeaves))
	leaves = append(leaves, []string{
		"agent_id", "blocked_by[]", "description", "due", "is_join", "max_attempts", "plan_id", "priority",
		"prompt", "run_isolated", "stream", "tags[]", "title", "write_set[]",
		"trigger.type", "trigger.config.at_ms", "trigger.config.dtstart_ms", "trigger.config.rrule", "trigger.config.tz",
	}...)
	for _, list := range []string{"criteria", "dod"} {
		for _, leaf := range criterionLeaves {
			leaves = append(leaves, list+"[]."+leaf)
		}
	}
	return leaves
}

// MappingRows returns the REST↔tool leaf mappings for tasks.
func MappingRows() []FieldMapping {
	shared := sharedTaskLeaves()
	rows := make([]FieldMapping, 0, 2*len(shared)+7)
	for _, leaf := range shared {
		rows = append(rows,
			FieldMapping{RESTPath: leaf, Tool: "create_task", ToolPath: leaf, Ops: OpCreate},
			FieldMapping{RESTPath: leaf, Tool: "update_task", ToolPath: leaf, Ops: OpUpdate})
	}
	for _, leaf := range []string{"parent_task_id", "workspace_id"} {
		rows = append(rows, FieldMapping{RESTPath: leaf, Tool: "create_task", ToolPath: leaf, Ops: OpCreate})
	}
	for _, leaf := range []string{"artifacts[]", "clear_due", "result", "status"} {
		rows = append(rows, FieldMapping{RESTPath: leaf, Tool: "update_task", ToolPath: leaf, Ops: OpUpdate})
	}
	// TK-05: REST clears the retry limit with max_attempts: null; the tool
	// has an explicit flag for it.
	rows = append(rows, FieldMapping{RESTPath: "max_attempts", Tool: "update_task", ToolPath: "clear_max_attempts", Ops: OpUpdate})
	return rows
}

// ExcludedRows returns the task leaves that deliberately have no counterpart.
func ExcludedRows() []FieldExclusion {
	rows := make([]FieldExclusion, 0, 11+2*len(criterionRecordLeaves))
	rows = append(rows, []FieldExclusion{
		{Side: "REST", Path: "action", Ops: OpCreate, Reason: "TK-06: single value, every task is an llm task"},
		{Side: "REST", Path: "surface", Ops: OpBoth, Reason: "TK-17: heartbeats are a workspace feature"},
		{Side: "REST", Path: "source_channel", Ops: OpCreate, Reason: "TK-18: provenance, the tool records the calling turn's channel"},
		{Side: "REST", Path: "source_chat_id", Ops: OpCreate, Reason: "TK-18: provenance, the tool records the calling turn's chat"},
		{Side: "REST", Path: "started_at", Ops: OpUpdate, Reason: "TK-19: engine record"},
		{Side: "REST", Path: "completed_at", Ops: OpUpdate, Reason: "TK-19: engine record"},
		{Side: "tool", Path: "task_id", Ops: OpUpdate, Reason: "addresses the task; REST carries it in the path /tasks/{id}"},
		{Side: "tool", Path: "workspace_id", Ops: OpUpdate, Reason: "TK-21: assertion only, the workspace is immutable after create"},
	}...)
	for _, leaf := range []string{"todos[].active_form", "todos[].status", "todos[].text"} {
		rows = append(rows, FieldExclusion{Side: "REST", Path: leaf, Ops: OpBoth, Reason: "TK-16: agents write todos through set_todos (#1269)"})
	}
	for _, list := range []string{"criteria", "dod"} {
		for _, leaf := range criterionRecordLeaves {
			rows = append(rows, FieldExclusion{Side: "REST", Path: list + "[]." + leaf, Ops: OpBoth, Reason: criterionRecordReason})
		}
	}
	return rows
}

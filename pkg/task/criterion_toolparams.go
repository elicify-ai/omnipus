// Omnipus — behavior-criterion tool-parameter helpers
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// This file is the single shared home for the two behavior-criterion pieces
// every criteria-authoring TOOL needs (ADR-052 FR-034 / ADR-074 D3a): the
// JSON-schema fragment its parameter schema advertises, and the decode of
// the raw `behavior` payload map an LLM tool call delivers. Both pkg/tools
// (create_task, create_plan, plan_correct) and pkg/sysagent/tools
// (create_task_in_workspace) previously carried byte-identical private
// copies of each — extracted here so the payload shape, its schema, and its
// pointer semantics evolve in exactly one place. Both packages already
// import pkg/task, and pkg/task imports neither, so there is no cycle.

// BehaviorCriterionParamSchema returns the JSON-schema fragment for a
// criterion's `behavior` payload, as advertised by the criteria-authoring
// tool schemas. The schema mirrors CriterionBehavior field-for-field; the
// min_count/max_count descriptions document the pointer semantics that
// DecodeBehaviorPayload implements.
func BehaviorCriterionParamSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool": map[string]any{
				"type":        "string",
				"description": "Name of the tool whose successful-call count is checked",
			},
			"min_count": map[string]any{
				"type":    "integer",
				"minimum": 0,
				"description": "Minimum successful calls of tool required within scope. Omitted defaults " +
					"to 1; an EXPLICIT 0 is distinct (0 with max_count 0 = \"never call this tool\")",
			},
			"max_count": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Maximum successful calls allowed within scope. Omitted = unbounded; must be >= min_count when present",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"attempt", "task_session"},
				"description": "Window the count is evaluated over; defaults to task_session",
			},
		},
		"required":    []string{"tool"},
		"description": "Required when kind is \"behavior\"; must be omitted for other kinds",
	}
}

// DecodeBehaviorPayload converts a raw `behavior` payload map (the
// map[string]any shape LLM tool-call arguments always decode into) into a
// CriterionBehavior, honoring the pointer semantics this package's
// CriterionBehavior documents: an ABSENT min_count/max_count stays nil
// (validateCriterionBehavior later defaults min_count to 1), while an
// EXPLICIT 0 decodes to a pointer at 0 — the distinction that makes
// {min_count: 0, max_count: 0} ("never call this tool") expressible.
// Numeric values arrive as float64 (JSON decode); non-numeric or missing
// values are treated as absent. Strict shape/range validation is left to
// validateCriterionBehavior, invoked from the store's normalizeCriteria.
func DecodeBehaviorPayload(m map[string]any) *CriterionBehavior {
	b := &CriterionBehavior{}
	b.Tool, _ = m["tool"].(string)
	if v, ok := m["min_count"].(float64); ok {
		n := int(v)
		b.MinCount = &n
	}
	if v, ok := m["max_count"].(float64); ok {
		n := int(v)
		b.MaxCount = &n
	}
	if s, ok := m["scope"].(string); ok && s != "" {
		b.Scope = BehaviorScope(s)
	}
	return b
}

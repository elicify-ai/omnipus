// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// red_scaffolding_fieldselect.go — RED SCAFFOLDING (qa-lead, RED phase, T8).
//
// The symbols below are the §7.13 field-selection helper's exact shapes,
// declared so the RED tests compile. They have NO behaviour: every function
// returns zero values, so the tests fail on their spec-derived expectations
// (never on a stubbed success). The implementing lead deletes this file and
// writes the real helper (spec §7.13: one shared helper, pkg/tools/fieldselect.go)
// in GREEN. Nothing here may grow behaviour during RED.

import (
	"context"
)

// FieldSpec describes one read tool's selectable object (spec §7.13).
type FieldSpec struct {
	// Object is the name used in error texts: "agent", "agent preview",
	// "task", "plan", "goal".
	Object string
	// Identity lists the always-returned, never-selectable keys
	// (agent: "id", "revision"; task, plan: "id"; goal: "goal_id";
	// preview: "preview_of").
	Identity []string
	// Valid lists the selectable names, in schema order
	// (schema properties − Identity − Metadata).
	Valid []string
}

// Metadata keys a result may carry; never selectable, never in Valid (r5, P-2).
var Metadata = []string{"absent_fields", "truncated_fields"}

// FieldSelecting is implemented by every read tool (r5, R2-MIN-001). The
// registry calls ValidateFieldsArg with FieldSpecFor(args) before generic
// validation, so the shared error is the one the agent sees.
type FieldSelecting interface {
	FieldSpecFor(args map[string]any) FieldSpec
}

// ValidateFieldsArg checks args["fields"] and returns the selected names
// (all Valid names for ["all"]) or the shared error result. RED scaffolding:
// returns nil, nil — the real implementation lives in GREEN.
func ValidateFieldsArg(args map[string]any, spec FieldSpec) ([]string, *ToolResult) {
	return nil, nil
}

// Project marshals the full generated object once, keeps Identity plus the
// selected keys the full object carries, records the others in absent_fields,
// and applies the size bound. RED scaffolding: returns nil, nil.
func Project(full any, spec FieldSpec, selected []string) (map[string]any, *ToolResult) {
	return nil, nil
}

// ResultBoundChars is the shared result size bound (§7.13): at most 48,000
// characters, JSON-encoded.
const ResultBoundChars = 48000

// CutChars is the per-string cut threshold (r6 value rule): only a selected,
// non-identity, top-level string VALUE longer than 8,000 characters is a cut
// candidate, and only while the result is over the bound.
const CutChars = 8000

// fieldSelectingProbe exists only so the scaffolding file cannot be deleted
// while a test still needs the interface in scope; the registry boundary
// (§7.13 "Validation boundary") is implemented in GREEN.
var _ = context.Background

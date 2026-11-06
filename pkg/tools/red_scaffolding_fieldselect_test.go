package tools

import "testing"

// The §7.13 test adapters, bound to the production helper (FieldSpec,
// Metadata, FieldSelecting, ResultBoundChars, CutChars, ValidateFieldsArg and
// Project live in fieldselect.go). This file must never grow
// projection/validation logic.

func t8ValidateFieldsArg(t *testing.T, args map[string]any, spec FieldSpec) ([]string, *ToolResult) {
	t.Helper()
	return ValidateFieldsArg(args, spec)
}
func t8Project(t *testing.T, full any, spec FieldSpec, selected []string) (map[string]any, *ToolResult) {
	t.Helper()
	return Project(full, spec, selected)
}

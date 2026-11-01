package tools

import "testing"

// Test-only declaration of the §7.13 contract while the production helper is
// missing. Nothing here is linked into the product. On GREEN reconciliation,
// the single test author removes these declarations and binds the two adapters
// to the real helper. This file must never grow projection/validation logic.
// In particular a failure here is BLOCKED, not behavioral RED evidence.
type FieldSpec struct {
	Object   string
	Identity []string
	Valid    []string
}

var Metadata = []string{"absent_fields", "truncated_fields"}

type FieldSelecting interface {
	FieldSpecFor(args map[string]any) FieldSpec
}

const ResultBoundChars = 48000
const CutChars = 8000

func t8ValidateFieldsArg(t *testing.T, args map[string]any, spec FieldSpec) ([]string, *ToolResult) {
	t.Helper()
	t.Fatal("BLOCKED: production ValidateFieldsArg not implemented — required by §7.13 / scenarios 20.1–20.6, 20.10; test-only scaffolding is not a validation implementation")
	return nil, nil
}
func t8Project(t *testing.T, full any, spec FieldSpec, selected []string) (map[string]any, *ToolResult) {
	t.Helper()
	t.Fatal("BLOCKED: production Project not implemented — required by §7.13 / scenarios 20.6–20.8b, 20.11; test-only scaffolding is not a projection implementation")
	return nil, nil
}

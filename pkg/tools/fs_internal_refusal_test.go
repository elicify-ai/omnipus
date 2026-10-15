package tools

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// WU-23 marker ruling (HANDOFFS 2026-10-14T00:15:15Z): exact structural
// fields, not a substring of prose, identify a protected internal-file refusal.
func requireInternalFileRefusal(t *testing.T, result *ToolResult, tool string) {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("internal file must be refused: %+v", result)
	}
	var refusal generated.PermissionDenied
	if err := json.Unmarshal([]byte(result.ForLLM), &refusal); err != nil {
		t.Fatalf("internal-file refusal is not a PermissionDenied payload: %v", err)
	}
	if refusal.Error != "permission_denied" || refusal.Code == nil || *refusal.Code != "protected_internal_file" ||
		!refusal.Permanent || refusal.Tool != tool {
		t.Fatalf("internal-file refusal fields: %+v", refusal)
	}
	if result.ExitCode != nil || len(result.InspectionImages) != 0 || len(result.Media) != 0 {
		t.Fatalf("refusal must not execute or disclose a file: %+v", result)
	}
}

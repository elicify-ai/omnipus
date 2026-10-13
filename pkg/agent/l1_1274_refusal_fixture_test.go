package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// The real registry's schema validator uses plain ForLLM text; create_agent
// field validation uses its INVALID_INPUT envelope. Both must expose the same
// path-specific reason. Never treat a different-field refusal as a path pass.
func l11274ModelRefusalReason(t *testing.T, result *tools.ToolResult) string {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("invalid CLI path must be refused, got %+v", result)
	}
	if !strings.HasPrefix(strings.TrimSpace(result.ForLLM), "{") {
		if !strings.HasPrefix(result.ForLLM, "invalid arguments for tool") {
			t.Fatalf("path refusal is neither a registry argument error nor INVALID_INPUT: %q", result.ForLLM)
		}
		return result.ForLLM
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &body); err != nil {
		t.Fatalf("malformed model-facing error envelope: %v; result=%q", err, result.ForLLM)
	}
	return l11274FailureDetails(t, result, "INVALID_INPUT")
}

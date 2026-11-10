package bedrock

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBedrockBody_Unchanged is TDD row 21. This feature adds no cache
// checkpoint and does not require Bedrock cache fields. The body is the
// pre-feature Converse shape: no cache_control, no prompt_cache_key, no
// cachePoint.
func TestBedrockBody_Unchanged(t *testing.T) {
	req := buildConverseRequest(
		[]Message{
			{Role: "system", Content: "instructions"},
			{Role: "user", Content: "hello"},
		},
		[]ToolDefinition{{
			Type:     "function",
			Function: ToolFunctionDefinition{Name: "read", Parameters: map[string]any{"type": "object"}},
		}},
		map[string]any{"max_tokens": 16},
	)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)
	for _, banned := range []string{"cache_control", "prompt_cache_key", "cachePoint", "cache_point"} {
		if strings.Contains(text, banned) {
			t.Errorf("Bedrock body contains %q; this feature must not add it:\n%s", banned, text)
		}
	}
	if !strings.Contains(text, "hello") || !strings.Contains(text, "instructions") {
		t.Fatalf("Bedrock body dropped the pre-feature messages:\n%s", text)
	}
}

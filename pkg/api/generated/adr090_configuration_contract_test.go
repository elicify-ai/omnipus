//go:build !windows

package generated

import (
	"strings"
	"testing"
)

// These cases derive from ADR-090 FR-002/003/007, independently of handlers.
func TestADR090AgentUpdateContract(t *testing.T) {
	revision := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name  string
		body  map[string]any
		valid bool
	}{
		{"omit capabilities", map[string]any{"revision": revision, "model": "test-model"}, true},
		{"clear skills", map[string]any{"revision": revision, "skills": []any{}}, true},
		{"null skills", map[string]any{"revision": revision, "skills": nil}, false},
		{"clear connectors", map[string]any{"revision": revision, "mcp_servers": []any{}}, true},
		{"null connectors", map[string]any{"revision": revision, "mcp_servers": nil}, false},
		{"assigned all tools", map[string]any{"revision": revision, "mcp_servers": []any{map[string]any{"id": "local"}}}, true},
		{"assigned no tools", map[string]any{"revision": revision, "mcp_servers": []any{map[string]any{"id": "local", "tools": []any{}}}}, true},
		{"null binding tools", map[string]any{"revision": revision, "mcp_servers": []any{map[string]any{"id": "local", "tools": nil}}}, false},
		{"remove override", map[string]any{"revision": revision, "tool_policy_changes": map[string]any{"remove": []any{"bash"}}}, true},
		{"null removal", map[string]any{"revision": revision, "tool_policy_changes": map[string]any{"remove": nil}}, false},
		{"missing revision", map[string]any{"model": "test-model", "skills": []any{}}, false},
		{"short revision", map[string]any{"revision": strings.Repeat("a", 63), "model": "test-model"}, false},
		{"long revision", map[string]any{"revision": strings.Repeat("a", 65), "model": "test-model"}, false},
		{"invalid revision", map[string]any{"revision": strings.Repeat("z", 64), "model": "test-model"}, false},
		{"no actual fields", map[string]any{"revision": revision}, false},
		{"obsolete concurrency field", map[string]any{"revision": revision, "updated_at": "2026-09-17T00:00:00Z", "model": "test-model"}, false},
		{"unsupported timeout", map[string]any{"revision": revision, "timeout_seconds": 10}, false},
		{"workspace heartbeat", map[string]any{"revision": revision, "heartbeat_enabled": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAgainstComponentSchema(t, "AgentUpdateRequest", tc.body)
			if (err == nil) != tc.valid {
				t.Fatalf("schema validity = %v, want %v; error: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestADR090ToolsUpdateContract(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		includeRevision, includeOverrides, valid bool
	}{
		{"explicit inheritance", true, true, true},
		{"missing revision", false, true, false},
		{"missing override intent", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"builtin": map[string]any{"policies": map[string]any{"bash": "deny"}}}
			if tc.includeRevision {
				body["revision"] = strings.Repeat("b", 64)
			}
			if tc.includeOverrides {
				body["override_names"] = []any{}
			}
			err := validateAgainstComponentSchema(t, "AgentToolsUpdateRequest", body)
			if (err == nil) != tc.valid {
				t.Fatalf("schema validity = %v, want %v; error: %v", err == nil, tc.valid, err)
			}
		})
	}
}

// ADR-090 FR-002/003/007 configuration-contract tests. The
// validateAgainstComponentSchema harness lives in schema_harness_test.go and
// is portable across Linux/macOS/Windows (TEST-008) — no build constraint.

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

func TestADR090WorkspaceMutationContract(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		body         map[string]any
		valid        bool
	}{
		{"metadata revision required", "WorkspaceUpdateRequest", map[string]any{"description": "changed", "pinned": false}, false},
		{"metadata with revision", "WorkspaceUpdateRequest", map[string]any{"revision": strings.Repeat("a", 64), "description": "changed"}, true},
		{"clear team and graph", "WorkspaceUpdateRequest", map[string]any{"revision": strings.Repeat("a", 64), "core_team": []any{}, "delegation": []any{}}, true},
		{"null graph refused", "WorkspaceUpdateRequest", map[string]any{"revision": strings.Repeat("a", 64), "delegation": nil}, false},
		{"graph revision required", "WorkspaceDelegationUpdateRequest", map[string]any{"edges": []any{}}, false},
		{"graph explicit clear", "WorkspaceDelegationUpdateRequest", map[string]any{"revision": strings.Repeat("a", 64), "edges": []any{}}, true},
		{"graph unknown field", "WorkspaceUpdateRequest", map[string]any{"revision": strings.Repeat("a", 64), "made_up": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAgainstComponentSchema(t, tc.schema, tc.body)
			if (err == nil) != tc.valid {
				t.Fatalf("schema validity = %v, want %v; error: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestADR090ToolsUpdateRejectsReadOnlyEchoes(t *testing.T) {
	for _, field := range []string{"agent_type", "tools"} {
		t.Run(field, func(t *testing.T) {
			body := map[string]any{"revision": strings.Repeat("a", 64), "override_names": []any{}, "builtin": map[string]any{"policies": map[string]any{"bash": "deny"}}}
			if err := validateAgainstComponentSchema(t, "AgentToolsUpdateRequest", body); err != nil {
				t.Fatalf("valid control rejected: %v", err)
			}
			if field == "agent_type" {
				body[field] = "core"
			} else {
				body[field] = []any{}
			}
			if err := validateAgainstComponentSchema(t, "AgentToolsUpdateRequest", body); err == nil {
				t.Fatalf("read-only echo %s was accepted", field)
			}
		})
	}
}

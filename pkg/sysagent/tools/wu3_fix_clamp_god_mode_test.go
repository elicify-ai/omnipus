package systools_test

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
)

// type-design T6 / architect M02: "clamped" reports what will run. With God
// Mode active the global layer is floored at allow, so a global ask no longer
// clamps an agent's allow; an agent's own deny/ask still applies. With God
// Mode off the same stored values are clamped (control).
func TestWU3ClampReportFollowsActiveGodMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		godMode bool
		want    []any
	}{
		{"god mode active", true, []any{}},
		{"god mode off", false, []any{map[string]any{"tool": "read_file", "requested": "allow", "effective": "ask"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, cfg := wu3Deps(t)
			d.GodModeActive = func() bool { return tc.godMode }
			id := wu3CreateAgent(t, d, cfg, "Policy subject")
			cfg.Sandbox.ToolPolicies["read_file"] = "ask"
			cfg.Sandbox.ToolPolicies["write_file"] = "ask"
			before := wu3AgentRead(t, d, id)
			body := wu3Success(t, systools.NewAgentPermissionsTool(d).Execute(context.Background(), map[string]any{
				"id": id, "revision": before["revision"],
				"tool_policy_changes": map[string]any{"set": map[string]any{"read_file": "allow", "write_file": "deny"}},
			}))
			read := wu3Success(t, systools.NewAgentGetToolsTool(d).Execute(context.Background(), map[string]any{"id": id}))
			assert.Equal(t, tc.want, body["clamped"], "mutation response")
			assert.Equal(t, tc.want, read["clamped"], "read-back")
		})
	}
}

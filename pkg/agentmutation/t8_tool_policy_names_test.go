package agentmutation

// Oracle: tool-parity r17 D-10 and §7.5 AG-07. Storage/HTTP mapping is
// separately blocked in the scenario pack; this tests the real shared name rule.
import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolPolicyNames_McpAndStale(t *testing.T) {
	ask, deny := config.ToolPolicyAsk, config.ToolPolicyDeny
	for _, tc := range []struct {
		name          string
		patch         ToolPolicyChanges
		want          map[string]config.ToolPolicy
		field, reason string
	}{
		{"D-10.1 builtin", ToolPolicyChanges{Set: map[string]config.ToolPolicy{"bash": ask}}, map[string]config.ToolPolicy{"bash": ask, "legacy_retired_tool": deny, "mcp_old_tool": deny}, "", ""},
		{"D-10.2 MCP", ToolPolicyChanges{Set: map[string]config.ToolPolicy{"mcp_composio_personal_googlecalendar_create_event": ask}}, map[string]config.ToolPolicy{"bash": deny, "legacy_retired_tool": deny, "mcp_old_tool": deny, "mcp_composio_personal_googlecalendar_create_event": ask}, "", ""},
		{"D-10.3 wildcard", ToolPolicyChanges{Set: map[string]config.ToolPolicy{"mcp_context7_*": deny}}, map[string]config.ToolPolicy{"bash": deny, "legacy_retired_tool": deny, "mcp_old_tool": deny, "mcp_context7_*": deny}, "", ""},
		{"D-10.4 unknown set", ToolPolicyChanges{Set: map[string]config.ToolPolicy{"no_such_tool": ask}}, nil, "tool_policy_changes.set", "unknown tool: no_such_tool"},
		{"D-10.5 invalid policy", ToolPolicyChanges{Set: map[string]config.ToolPolicy{"bash": "sometimes"}}, nil, "tool_policy_changes.set", `invalid policy "sometimes" for bash`},
		{"D-10.6 remove stored MCP", ToolPolicyChanges{Remove: []string{"mcp_old_tool"}}, map[string]config.ToolPolicy{"bash": deny, "legacy_retired_tool": deny}, "", ""},
		{"D-10.7 remove stored retired", ToolPolicyChanges{Remove: []string{"legacy_retired_tool"}}, map[string]config.ToolPolicy{"bash": deny, "mcp_old_tool": deny}, "", ""},
		{"D-10.8 unknown remove", ToolPolicyChanges{Remove: []string{"no_such_tool"}}, nil, "tool_policy_changes.remove", "unknown tool: no_such_tool"},
		{"D-10.9 overlap", ToolPolicyChanges{Set: map[string]config.ToolPolicy{"bash": ask}, Remove: []string{"bash"}}, nil, "tool_policy_changes", "tool appears in both set and remove: bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := map[string]config.ToolPolicy{"bash": deny, "legacy_retired_tool": deny, "mcp_old_tool": deny}
			original := map[string]config.ToolPolicy{"bash": deny, "legacy_retired_tool": deny, "mcp_old_tool": deny}
			got, err := ApplyToolPolicyChanges(current, tc.patch, map[string]struct{}{"bash": {}})
			if tc.field == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			} else {
				var fieldErr *FieldError
				require.ErrorAs(t, err, &fieldErr)
				assert.Equal(t, []string{tc.field}, fieldErr.Fields)
				assert.Equal(t, tc.reason, fieldErr.Reason)
				assert.Nil(t, got)
			}
			assert.Equal(t, original, current, "shared helper may not mutate its input, including on refusal")
		})
	}
}

package systools_test

// FR-037 / T-10 R15/R16: the existing permission tool, entity store and
// revision guard remain real. Expected Q7 refusal text comes from the spec.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestQ7T35_PermissionToolRejectsAllowBeforePersistence(t *testing.T) {
	for _, id := range []string{"subject", "omnipus"} {
		t.Run(id, func(t *testing.T) {
			_, store, before, permission := l1WU2PermissionFixture(t, id)
			result := permission.Execute(context.Background(), map[string]any{
				"id": id, "revision": before.Revision,
				"tool_policy_changes": map[string]any{"set": map[string]any{"set_channel_outbound": "allow"}},
			})
			require.True(t, result.IsError, "FR-037: Allow is refused, not clamped and stored: %s", result.ForLLM)
			var body map[string]any
			require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
			require.Equal(t, false, body["success"])
			failure, ok := body["error"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "set_channel_outbound always asks for approval; it can be set to ask or deny, not allow", failure["message"], "T-10 R15/R16: exact named refusal, not an unknown tool or missing revision error")
			after, err := store.ReadState(id)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestQ7T35_PermissionToolStoresApplicableAskDenyAndClearsToInheritance(t *testing.T) {
	for _, tc := range []struct {
		id, policy string
		refuse     bool
	}{
		{"subject", "ask", false}, {"subject", "deny", false},
		{"omnipus", "deny", false}, {"omnipus", "ask", true},
	} {
		t.Run(tc.id+"_"+tc.policy, func(t *testing.T) {
			_, store, before, permission := l1WU2PermissionFixture(t, tc.id)
			result := permission.Execute(context.Background(), map[string]any{
				"id": tc.id, "revision": before.Revision,
				"tool_policy_changes": map[string]any{"set": map[string]any{"set_channel_outbound": tc.policy}},
			})
			after, err := store.ReadState(tc.id)
			require.NoError(t, err)
			if tc.refuse {
				require.True(t, result.IsError)
				require.Contains(t, result.ForLLM, "OMNIPUS_PER_AGENT_ASK", "T-10 R16: preserve existing Omnipus Ask refusal")
				require.Equal(t, before, after)
				return
			}
			require.False(t, result.IsError, "FR-037: applicable Ask/Deny is saved: %s", result.ForLLM)
			require.Equal(t, config.ToolPolicy(tc.policy), after.Agent.Tools.Builtin.Policies["set_channel_outbound"])
			cleared := permission.Execute(context.Background(), map[string]any{
				"id": tc.id, "revision": after.Revision,
				"tool_policy_changes": map[string]any{"remove": []any{"set_channel_outbound"}},
			})
			require.False(t, cleared.IsError, "FR-037: removing the override is the supported way to inherit: %s", cleared.ForLLM)
			state, err := store.ReadState(tc.id)
			require.NoError(t, err)
			_, present := state.Agent.Tools.Builtin.Policies["set_channel_outbound"]
			require.False(t, present, "a clear never writes Ask in its place")
		})
	}
}

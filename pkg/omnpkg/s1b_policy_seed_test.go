package omnpkg_test

// Architect ruling /workspace/logs/1094-s1b/architect-ruling.md section 4B:
// external package policies are not inputs to create; the target worker seed
// is supplied by the normal create path. Native policy mapping is unchanged.

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestS1bExternalImportMapping_UsesTargetWorkerPolicySeed(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "native control still maps and warns"
		if external {
			name = "external package policies are removed without policy warnings"
		}
		t.Run(name, func(t *testing.T) {
			target := newFakeTarget()
			target.knownTools["bash"], target.knownTools["search_web"] = true, true
			target.alwaysAsk["bash"] = true
			target.providers["openrouter"] = true
			target.seeds["custom"] = map[string]string{"bash": "ask", "search_web": "allow"}
			target.seeds["worker"] = map[string]string{"bash": "ask", "search_web": "allow"}
			rec := s1bExternalRecord(nil)
			if !external {
				rec["type"] = "custom"
				delete(rec, "subagents")
			}
			rec["tools"] = map[string]any{"builtin": map[string]any{"policies": map[string]any{
				"bash": "allow", "search_web": "deny", "retired_s1b_tool": "allow",
			}}}
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			before := append([]byte(nil), raw...)
			plan, err := omnpkg.MapAgentForImport(raw, testSourceID, target)
			require.NoError(t, err)
			var mapped map[string]any
			require.NoError(t, json.Unmarshal(plan.Record, &mapped))
			tools, _ := mapped["tools"].(map[string]any)
			builtin, _ := tools["builtin"].(map[string]any)
			if external {
				_, policiesPresent := builtin["policies"]
				assert.False(t, policiesPresent, "ruling 4B: remove the source policy input before mapTools; create owns the target seed")
				assert.Empty(t, warningsOf(plan, "tool_policy_dropped"), "external seed selection is not a policy drop warning")
				assert.Empty(t, warningsOf(plan, "tool_policy_tightened"), "external seed selection is not policy tightening")
				assert.Len(t, warningsOf(plan, "runtime_settings_removed"), 1, "keep the unrelated external-runtime guidance")
			} else {
				assert.Equal(t, map[string]any{"bash": "ask", "search_web": "deny"}, builtin["policies"], "native package changes still apply over the target seed")
				require.Len(t, warningsOf(plan, "tool_policy_dropped"), 1)
				assert.Equal(t, []string{"retired_s1b_tool"}, warningsOf(plan, "tool_policy_dropped")[0].Items)
				require.Len(t, warningsOf(plan, "tool_policy_tightened"), 1)
				assert.Equal(t, []string{"bash"}, warningsOf(plan, "tool_policy_tightened")[0].Items)
			}
			assert.Equal(t, before, raw, "mapping must not mutate the uploaded bytes")
		})
	}
}

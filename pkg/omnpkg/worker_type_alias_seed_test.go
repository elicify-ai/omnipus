package omnpkg_test

// #1953 / CHECK P3-2: direct mapper callers must select the decoded native
// worker's seed, not the Main/custom seed, for every accepted type spelling.
// The fake target supplies distinct seed maps at the mapper's lookup edge;
// literal issue expectations, never mapper output, form the oracle.

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapAgentForImport_WorkerTypeAliasesSelectWorkerSeed(t *testing.T) {
	for _, tc := range []struct{ name, members, wantType, wantCreateTask string }{
		{"lowercase_worker_control", `"type":"worker"`, "worker", "deny"},
		{"capitalized_Type", `"Type":"worker"`, "worker", "deny"},
		{"uppercase_TYPE", `"TYPE":"worker"`, "worker", "deny"},
		{"type_custom_then_Type_worker", `"type":"custom","Type":"worker"`, "worker", "deny"},
		{"Type_custom_then_type_worker", `"Type":"custom","type":"worker"`, "worker", "deny"},
		{"capitalized_custom_control", `"Type":"custom"`, "custom", "allow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := newFakeTarget()
			target.knownTools["search_web"], target.knownTools["create_task"] = true, true
			target.seeds["worker"] = map[string]string{"create_task": "deny", "search_web": "ask"}
			target.seeds["custom"] = map[string]string{"create_task": "allow", "search_web": "ask"}
			target.seeds[""] = map[string]string{"create_task": "allow", "search_web": "ask"}
			raw := []byte(fmt.Sprintf(`{"id":%q,"name":"Coder","description":"researches",%s,"subagents":{"executor":{"kind":"native"}},"tools":{"builtin":{"policies":{"search_web":"allow"}}}}`, testSourceID, tc.members))
			before := append([]byte(nil), raw...)
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.Equal(t, config.AgentType(tc.wantType), input.Type, "fixture's literal decoded type")
			require.False(t, input.IsExternalCLIWorker(), "this test covers native policy mapping, not external policy stripping")
			require.NotNil(t, input.Tools)
			require.Equal(t, map[string]config.ToolPolicy{"search_web": config.ToolPolicyAllow}, input.Tools.Builtin.Policies)
			require.NotContains(t, input.Tools.Builtin.Policies, "create_task", "the omitted permission must actually come from type seed selection")
			plan, err := omnpkg.MapAgentForImport(raw, testSourceID, target)
			require.NoError(t, err, "accepted native type aliases must map")
			require.NotNil(t, plan)
			var mapped config.AgentConfig
			require.NoError(t, json.Unmarshal(plan.Record, &mapped))
			assert.Equal(t, config.AgentType(tc.wantType), mapped.Type)
			require.NotNil(t, mapped.Tools)
			assert.Equal(t, map[string]config.ToolPolicy{
				"create_task": config.ToolPolicy(tc.wantCreateTask), "search_web": config.ToolPolicyAllow,
			}, mapped.Tools.Builtin.Policies, "#1953: exact selected type seed plus explicit input policy; never Main allow on a worker")
			assert.Empty(t, plan.Warnings, "no installed-reference drop or policy tightening is needed")
			assert.Equal(t, before, raw, "the mapper must not mutate incoming raw bytes")
		})
	}
}

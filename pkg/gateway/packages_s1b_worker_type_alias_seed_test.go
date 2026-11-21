package gateway

// Issue #1953 / second-pass security report R2: a native worker's omitted
// builtin permissions use the worker seed, regardless of type-key casing.
// Normal REST creation is the independent parity oracle; the ticket also
// fixes create_task to deny, so matching two incorrect maps cannot pass.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_NativeWorkerTypeAliasesUseWorkerPolicySeed(t *testing.T) {
	oracle := packageAPI(t)
	created := postRESTCreate(t, oracle, `{"name":"Target Worker Seed","type":"Subagent","description":"researches","soul":"Be brief."}`)
	require.Equal(t, http.StatusCreated, created.Code, "ordinary native-worker REST create; body=%s", created.Body)
	seedID := decodeAgentResp(t, created.Body).Id
	require.NotEmpty(t, seedID)
	seed, err := agentstore.New(oracle.homePath).ReadState(seedID)
	require.NoError(t, err)
	require.Equal(t, config.AgentTypeWorker, seed.Agent.Type)
	require.False(t, seed.Agent.IsExternalCLIWorker())
	require.NotNil(t, seed.Agent.Tools)
	want := seed.Agent.Tools.Builtin.Policies
	require.Equal(t, config.ToolPolicyDeny, want["create_task"], "#1953 fixes the independent expectation: worker defaults deny create_task")
	require.Equal(t, config.ToolPolicyAllow, want["search_web"], "the sparse packaged policy must agree with the worker seed")

	for _, tc := range []struct{ name, typeKey string }{
		{"lowercase_type_control", "type"},
		{"capitalized_Type", "Type"},
		{"uppercase_TYPE", "TYPE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			record := map[string]any{
				"id": specSourceID, "name": "Coder", tc.typeKey: "worker", "description": "researches",
				"subagents": map[string]any{"executor": map[string]any{"kind": "native"}},
				"tools":     map[string]any{"builtin": map[string]any{"policies": map[string]string{"search_web": "allow"}}},
			}
			pkg := buildAgentPackage(t, specSourceID, "Coder", record, []byte("Be brief."), nil, 0)
			_, files := decodePackageBytes(t, pkg)
			raw := files[agentRecordPath(specSourceID)]
			var members map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &members))
			require.JSONEq(t, `"worker"`, string(members[tc.typeKey]), "fixture must contain the requested type-key spelling")
			if tc.typeKey != "type" {
				require.NotContains(t, members, "type", "the capitalized fixture must not quietly carry a lowercase seed selector")
			}
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.Equal(t, config.AgentTypeWorker, input.Type, "each accepted spelling denotes a worker, never Main")
			require.False(t, input.IsExternalCLIWorker())
			require.NotNil(t, input.Tools)
			require.Equal(t, map[string]config.ToolPolicy{"search_web": config.ToolPolicyAllow}, input.Tools.Builtin.Policies, "fixture contains exactly one explicit policy, not a prefilled seed")
			require.NotContains(t, input.Tools.Builtin.Policies, "create_task", "#1953 exercises an omitted permission, not a packaged override")
			before := snapshotHome(t, api.homePath)
			preview := stageUpload(t, api, pkg)
			require.Len(t, preview.WillAdd, 1)
			require.Empty(t, agentEntityNames(t, api.homePath), "preview must not persist the worker")
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			got := callPackageApply(t, api, preview.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "native worker import; body=%s", got.Body)
			result := decodeResult(t, got.Body)
			require.Len(t, result.Created, 1)
			stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
			require.NoError(t, err)
			require.Equal(t, config.AgentTypeWorker, stored.Agent.Type, "changing key casing must not turn a worker into Main")
			require.False(t, stored.Agent.IsExternalCLIWorker())
			require.NotNil(t, stored.Agent.Subagents)
			require.NotNil(t, stored.Agent.Subagents.Executor)
			assert.Equal(t, config.ExecutorKindNative, stored.Agent.Subagents.Executor.Kind)
			require.NotNil(t, stored.Agent.Tools)
			assert.Equal(t, config.ToolPolicyDeny, stored.Agent.Tools.Builtin.Policies["create_task"], "#1953: omitted create_task must use worker deny, not Main allow, for every type-key spelling")
			assert.Equal(t, want, stored.Agent.Tools.Builtin.Policies, "#1953: the complete stored map must equal the independent worker REST seed, as in the lowercase control")
			assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId, "successful import must publish the native worker")
		})
	}
}

package gateway

// #1952 / CHECK P3-2: valid native policy/model alias conflicts must import
// with a literal incoming-order oracle, not pass by taking a refusal branch.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_ConflictingPolicyAliasesPreserveIncomingMeaning(t *testing.T) {
	// Field mapping: native policy changes override the ordinary type seed.
	// Fix the seed independently through normal REST create, never import.
	oracle := packageAPI(t)
	created := postRESTCreate(t, oracle, `{"name":"Native Seed","type":"Subagent","description":"researches","soul":"Be brief."}`)
	require.Equal(t, http.StatusCreated, created.Code, "native seed control; body=%s", created.Body)
	seed, err := agentstore.New(oracle.homePath).ReadState(decodeAgentResp(t, created.Body).Id)
	require.NoError(t, err)
	require.NotNil(t, seed.Agent.Tools)
	require.Equal(t, config.ToolPolicyDeny, seed.Agent.Tools.Builtin.Policies["create_task"], "#1953: baseline worker seed is deny")
	const first = `{"search_web":"deny","bash":"deny"}`
	const second = `{"search_web":"ask","create_task":"ask"}`
	for _, tc := range []struct {
		name, members string
		wantSearch    config.ToolPolicy
	}{
		{"canonical_merged_control", `"tools":{"builtin":{"policies":{"search_web":"ask","bash":"deny","create_task":"ask"}}}`, config.ToolPolicyAsk},
		{"tools_then_Tools", `"tools":{"builtin":{"policies":` + first + `}},"Tools":{"builtin":{"policies":` + second + `}}`, config.ToolPolicyAsk},
		{"Tools_then_tools", `"Tools":{"builtin":{"policies":` + second + `}},"tools":{"builtin":{"policies":` + first + `}}`, config.ToolPolicyDeny},
		{"builtin_then_Builtin", `"tools":{"builtin":{"policies":` + first + `},"Builtin":{"policies":` + second + `}}`, config.ToolPolicyAsk},
		{"Builtin_then_builtin", `"tools":{"Builtin":{"policies":` + second + `},"builtin":{"policies":` + first + `}}`, config.ToolPolicyDeny},
		{"policies_then_Policies", `"tools":{"builtin":{"policies":` + first + `,"Policies":` + second + `}}`, config.ToolPolicyAsk},
		{"Policies_then_policies", `"tools":{"builtin":{"Policies":` + second + `,"policies":` + first + `}}`, config.ToolPolicyDeny},
		{"duplicate_policies_second_last", `"tools":{"builtin":{"policies":` + first + `,"policies":` + second + `}}`, config.ToolPolicyAsk},
		{"duplicate_policies_first_last", `"tools":{"builtin":{"policies":` + second + `,"policies":` + first + `}}`, config.ToolPolicyDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			runtime := s1bAliasRuntime{"native_worker", config.AgentTypeWorker, config.ExecutorKindNative}
			raw := s1bAliasRuntimeRecord(t, runtime, tc.members)
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.NotNil(t, input.Tools)
			wantChanges := map[string]config.ToolPolicy{"search_web": tc.wantSearch, "bash": config.ToolPolicyDeny, "create_task": config.ToolPolicyAsk}
			require.Equal(t, wantChanges, input.Tools.Builtin.Policies, "fixture must exercise last scalar wins AND merging of distinct policy entries")
			before := snapshotHome(t, api.homePath)
			prev := stageUpload(t, api, s1bAliasPackage(t, raw, true))
			require.Len(t, prev.WillAdd, 1)
			require.Empty(t, agentEntityNames(t, api.homePath), "preview must not store the agent")
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "native policy aliases must import, not be refused; body=%s", got.Body)
			result := decodeResult(t, got.Body)
			require.Len(t, result.Created, 1)
			stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
			require.NoError(t, err)
			require.Equal(t, config.AgentTypeWorker, stored.Agent.Type)
			require.False(t, stored.Agent.IsExternalCLIWorker())
			require.NotNil(t, stored.Agent.Tools)
			want := make(map[string]config.ToolPolicy, len(seed.Agent.Tools.Builtin.Policies))
			for tool, policy := range seed.Agent.Tools.Builtin.Policies {
				want[tool] = policy
			}
			for tool, policy := range wantChanges {
				want[tool] = policy
			}
			assert.Equal(t, want, stored.Agent.Tools.Builtin.Policies, "#1952: preserve the complete seed plus incoming merged overrides, including order-dependent search_web")
			assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId)
		})
	}
}

func TestPackagesS1b_ConflictingModelAliasesPreserveIncomingMeaning(t *testing.T) {
	const first = `{"primary":"first-model","provider":"first-provider"}`
	const second = `{"primary":"second-model","provider":"second-provider"}`
	for _, tc := range []struct{ name, members, wantModel, wantProvider string }{
		{"canonical_control", `"model":` + second, "second-model", "second-provider"},
		{"model_then_Model", `"model":` + first + `,"Model":` + second, "second-model", "second-provider"},
		{"Model_then_model", `"Model":` + second + `,"model":` + first, "first-model", "first-provider"},
		{"duplicate_model_second_last", `"model":` + first + `,"model":` + second, "second-model", "second-provider"},
		{"duplicate_model_first_last", `"model":` + second + `,"model":` + first, "first-model", "first-provider"},
		{"primary_provider_then_capitals", `"model":{"primary":"first-model","provider":"first-provider","Primary":"second-model","Provider":"second-provider"}`, "second-model", "second-provider"},
		{"capitals_then_primary_provider", `"model":{"Primary":"second-model","Provider":"second-provider","primary":"first-model","provider":"first-provider"}`, "first-model", "first-provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			record := s1bWorkerRecord(false)
			record["subagents"] = map[string]any{"executor": map[string]any{"kind": "native"}}
			delete(record, "model")
			raw := s1bFix3RecordWithMembers(t, record, tc.members)
			want := &config.AgentModelConfig{Primary: tc.wantModel, Provider: tc.wantProvider}
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.Equal(t, want, input.Model, "fixture's literal incoming model/provider interpretation")
			before := snapshotHome(t, api.homePath)
			prev := stageUpload(t, api, s1bAliasPackage(t, raw, true))
			require.Len(t, prev.WillAdd, 1)
			require.Empty(t, agentEntityNames(t, api.homePath))
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "valid model aliases must import, even with a provider not configured here; body=%s", got.Body)
			result := decodeResult(t, got.Body)
			require.Len(t, result.Created, 1)
			stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
			require.NoError(t, err)
			assert.Equal(t, want, stored.Agent.Model, "#1952: mapping must preserve the incoming primary/provider pair")
			assert.Equal(t, config.AgentTypeWorker, stored.Agent.Type)
			assert.False(t, stored.Agent.IsExternalCLIWorker())
			assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId)
		})
	}
}

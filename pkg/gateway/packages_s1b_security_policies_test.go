package gateway

// Security report F2 / architect ruling 4B: JSON case aliases and conflicting
// duplicate members cannot replace an external worker's target policy seed.
// The full map is needed to pass the submitted-map completeness validator.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_ExternalPolicyCaseAliasesUseTargetSeed(t *testing.T) {
	// Independent parity oracle: normal REST create on a fresh target-version
	// home, not MapAgentForImport or a previous package import's output.
	oracle := packageAPI(t)
	targetID := s1bCreateExternalByREST(t, oracle, "Target Seed")
	target, err := agentstore.New(oracle.homePath).ReadState(targetID)
	require.NoError(t, err)
	require.NotNil(t, target.Agent.Tools)
	want := target.Agent.Tools.Builtin.Policies
	require.NotEmpty(t, want)
	require.Equal(t, config.ToolPolicyDeny, want["create_task"], "F2's independent fixed expectation: standard workers deny create_task")
	malicious := make(map[string]config.ToolPolicy, len(want))
	for name, policy := range want {
		malicious[name] = policy
	}
	malicious["create_task"] = config.ToolPolicyAllow
	goodJSON, err := json.Marshal(want)
	require.NoError(t, err)
	badJSON, err := json.Marshal(malicious)
	require.NoError(t, err)
	good, bad := string(goodJSON), string(badJSON)

	for _, tc := range []struct{ name, members string }{
		{"canonical_lowercase_control", `"tools":{"builtin":{"policies":` + bad + `}}`},
		{"Policies_alias", `"tools":{"builtin":{"Policies":` + bad + `}}`},
		{"Builtin_alias", `"tools":{"Builtin":{"policies":` + bad + `}}`},
		{"Tools_alias", `"Tools":{"builtin":{"policies":` + bad + `}}`},
		{"all_three_aliases", `"Tools":{"Builtin":{"Policies":` + bad + `}}`},
		{"policies_then_Policies_conflict", `"tools":{"builtin":{"policies":` + good + `,"Policies":` + bad + `}}`},
		{"Policies_then_policies_conflict", `"tools":{"builtin":{"Policies":` + bad + `,"policies":` + good + `}}`},
		{"builtin_then_Builtin_conflict", `"tools":{"builtin":{"policies":` + good + `},"Builtin":{"Policies":` + bad + `}}`},
		{"Builtin_then_builtin_conflict", `"tools":{"Builtin":{"Policies":` + bad + `},"builtin":{"policies":` + good + `}}`},
		{"tools_then_Tools_conflict", `"tools":{"builtin":{"policies":` + good + `}},"Tools":{"Builtin":{"Policies":` + bad + `}}`},
		{"Tools_then_tools_conflict", `"Tools":{"Builtin":{"Policies":` + bad + `}},"tools":{"builtin":{"policies":` + good + `}}`},
		{"duplicate_Policies_bad_last", `"tools":{"builtin":{"Policies":` + good + `,"Policies":` + bad + `}}`},
		{"duplicate_Policies_bad_first", `"tools":{"builtin":{"Policies":` + bad + `,"Policies":` + good + `}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			raw := s1bFix3RecordWithMembers(t, s1bWorkerRecord(false), tc.members)
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.True(t, input.IsExternalCLIWorker(), "aliases affect policy keys, not this fixture's runtime")
			require.NotNil(t, input.Tools)
			require.Len(t, input.Tools.Builtin.Policies, len(want), "use a complete map; a completeness refusal would not exercise F2")
			require.Contains(t, string(raw), `"create_task":"allow"`, "the submitted package must really include the conflicting permission")
			manifest, parts := s1bFix3RecordParts(raw)
			pkg := s1bArchive(t, manifest, parts)
			_, inputFiles := decodePackageBytes(t, pkg)
			require.Equal(t, raw, inputFiles[agentRecordPath(specSourceID)], "archive construction must preserve member spelling and duplicate order")
			prev := stageUpload(t, api, pkg)
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "complete external policy maps select the target seed; body=%s", got.Body)
			result := decodeResult(t, got.Body)
			require.Len(t, result.Created, 1)
			stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
			require.NoError(t, err)
			require.True(t, stored.Agent.IsExternalCLIWorker())
			require.NotNil(t, stored.Agent.Tools)
			assert.Equal(t, config.ToolPolicyDeny, stored.Agent.Tools.Builtin.Policies["create_task"], "F2: an alias/duplicate cannot raise create_task above the target's worker seed")
			assert.Equal(t, want, stored.Agent.Tools.Builtin.Policies, "F2: the complete target worker map, never any packaged policy spelling, must win")
			assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId)
		})
	}
}

package gateway

// Issue #1952 / CHECK P3-1: re-encoding an agent record must not change the
// meaning of conflicting case aliases. Valid effective inputs must import;
// a blanket refusal is not a successful implementation of the typed-decoding
// fix. Effective external workers with nonempty effort must be refused.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_ConflictingAgentAliasesCannotChangeMeaning(t *testing.T) {
	const external = `"type":"worker","subagents":{"executor":{"kind":"external-cli","cli":"codex"}}`
	const externalExecutor = `{"kind":"external-cli","cli":"codex"}`
	const nativeExecutor = `{"kind":"native","cli":"codex"}`
	for _, tc := range []struct {
		name, members, wantEffort string
		wantType                  config.AgentType
		wantKind                  config.ExecutorKind
		mustImport, mustRefuse    bool
	}{
		{"canonical_external_control", external, "", config.AgentTypeWorker, config.ExecutorKindExternalCLI, true, false},
		{"canonical_effort_refusal_control", external + `,"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindExternalCLI, false, true},
		{"effort_then_Effort", external + `,"reasoning_effort":"high","Reasoning_Effort":""`, "", config.AgentTypeWorker, config.ExecutorKindExternalCLI, true, false},
		{"Effort_then_effort", external + `,"Reasoning_Effort":"","reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindExternalCLI, false, true},
		{"type_then_Type", `"type":"worker","Type":"custom","subagents":{"executor":` + externalExecutor + `},"reasoning_effort":"high"`, "high", config.AgentTypeCustom, config.ExecutorKindExternalCLI, true, false},
		{"Type_then_type", `"Type":"custom","type":"worker","subagents":{"executor":` + externalExecutor + `},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindExternalCLI, false, true},
		{"subagents_then_Subagents", `"type":"worker","subagents":{"executor":` + externalExecutor + `},"Subagents":{"executor":` + nativeExecutor + `},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindNative, true, false},
		{"Subagents_then_subagents", `"type":"worker","Subagents":{"executor":` + nativeExecutor + `},"subagents":{"executor":` + externalExecutor + `},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindExternalCLI, false, true},
		{"executor_then_Executor", `"type":"worker","subagents":{"executor":` + externalExecutor + `,"Executor":` + nativeExecutor + `},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindNative, true, false},
		{"Executor_then_executor", `"type":"worker","subagents":{"Executor":` + nativeExecutor + `,"executor":` + externalExecutor + `},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindExternalCLI, false, true},
		{"kind_then_Kind", `"type":"worker","subagents":{"executor":{"kind":"external-cli","Kind":"native","cli":"codex"}},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindNative, true, false},
		{"Kind_then_kind", `"type":"worker","subagents":{"executor":{"Kind":"native","kind":"external-cli","cli":"codex"}},"reasoning_effort":"high"`, "high", config.AgentTypeWorker, config.ExecutorKindExternalCLI, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			record := s1bWorkerRecord(false)
			delete(record, "type")
			delete(record, "subagents")
			// These members must remain raw, in the stated order: a fixture
			// assembled as a Go map would sort away the issue's input ordering.
			raw := s1bFix3RecordWithMembers(t, record, tc.members)
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.Equal(t, tc.wantType, input.Type, "fixture's specified incoming type")
			require.NotNil(t, input.Subagents)
			require.NotNil(t, input.Subagents.Executor)
			require.Equal(t, tc.wantKind, input.Subagents.Executor.Kind, "fixture's specified incoming executor kind")
			require.Equal(t, tc.wantEffort, input.ReasoningEffort, "fixture's specified incoming effort")
			wantExternal := tc.wantType == config.AgentTypeWorker && tc.wantKind == config.ExecutorKindExternalCLI
			// Field mapping SOUL.md row: valid native inputs need an owned,
			// nonblank prompt; otherwise a soul refusal could hide alias bugs.
			pkg := s1bAliasPackage(t, raw, !wantExternal)
			_, files := decodePackageBytes(t, pkg)
			require.Equal(t, raw, files[agentRecordPath(specSourceID)], "archive must preserve alias spelling and incoming order byte for byte")
			before := snapshotHome(t, api.homePath)
			noAgentStored := func() {
				t.Helper()
				require.Empty(t, agentEntityNames(t, api.homePath), "#1952: refusal/preview must not write any agent, including pending state")
				require.Empty(t, listedAgentIDs(t, api), "#1952: refusal/preview must not publish an agent")
				requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			}
			got := callPackageImport(t, api, pkg)
			require.NotEqual(t, tc.mustImport, tc.mustRefuse, "each case must have exactly one specified outcome")
			if tc.mustRefuse {
				er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, "element_not_creatable")
				assert.Equal(t, "reasoning_effort", packageErrField(er))
				assert.Contains(t, er.Error, agentmutation.ExternalCLIEffortRefusal)
				require.Empty(t, packageStagingIDs(t, api.homePath), "a refused upload must not retain a staging slot")
				noAgentStored()
				return
			}
			require.Equal(t, http.StatusCreated, got.Code, "CHECK P3-1: valid aliases must stage, not be refused; body=%s", got.Body)
			preview := decodePreview(t, got.Body)
			require.Len(t, preview.WillAdd, 1)
			noAgentStored()
			applied := callPackageApply(t, api, preview.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, applied.Code, "CHECK P3-1: valid aliases must import, not be refused; body=%s", applied.Body)
			result := decodeResult(t, applied.Body)
			require.Len(t, result.Created, 1)
			stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
			require.NoError(t, err)
			assert.Equal(t, tc.wantType, stored.Agent.Type, "#1952: mapping must not change the incoming type interpretation")
			assert.Equal(t, wantExternal, stored.Agent.IsExternalCLIWorker(), "#1952: mapping must not switch the runtime after validation")
			if tc.wantType == config.AgentTypeWorker {
				require.NotNil(t, stored.Agent.Subagents)
				require.NotNil(t, stored.Agent.Subagents.Executor)
				assert.Equal(t, tc.wantKind, stored.Agent.Subagents.Executor.Kind, "#1952: nested runtime aliases must keep their incoming interpretation")
			}
			assert.Equal(t, tc.wantEffort, stored.Agent.ReasoningEffort, "#1952: mapping must not replace the effort that validation interpreted")
			if stored.Agent.IsExternalCLIWorker() {
				assert.Equal(t, "", stored.Agent.ReasoningEffort, "#1952: unsupported reasoning_effort must never be stored on an external worker")
				assert.Equal(t, "", stored.Soul, "the canonical external control remains promptless")
			}
			assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId, "the accepted path must reach the reloaded roster")
		})
	}
}

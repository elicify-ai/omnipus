package gateway

// Security report F4 / FR-047: runtime discrimination must agree with the
// record that is eventually created, including Go's accepted case aliases.
// The brief permits element_not_creatable or a genuinely external result with
// the unsupported field absent, never storing the submitted effort.

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

func TestPackagesS1b_RuntimeCaseAliasesCannotStoreUnsupportedFields(t *testing.T) {
	for _, tc := range []struct {
		name, typeKey, subKey, executorKey, kindKey, effort string
		mustRefuse                                          bool
	}{
		{"canonical_external_control", "type", "subagents", "executor", "kind", "", false},
		{"canonical_effort_refusal_control", "type", "subagents", "executor", "kind", "high", true},
		{"Type_alias", "Type", "subagents", "executor", "kind", "high", false},
		{"Subagents_alias", "type", "Subagents", "executor", "kind", "high", false},
		{"Executor_alias", "type", "subagents", "Executor", "kind", "high", false},
		{"Kind_alias", "type", "subagents", "executor", "Kind", "high", false},
		{"all_runtime_aliases", "Type", "Subagents", "Executor", "Kind", "high", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			record := s1bWorkerRecord(false)
			delete(record, "type")
			delete(record, "subagents")
			record[tc.typeKey] = "worker"
			record[tc.subKey] = map[string]any{tc.executorKey: map[string]any{tc.kindKey: "external-cli", "cli": "codex"}}
			if tc.effort != "" {
				record["reasoning_effort"] = tc.effort
			}
			raw, err := json.Marshal(record)
			require.NoError(t, err)
			var input config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &input))
			require.True(t, input.IsExternalCLIWorker(), "positive control: these spellings denote an external record to the real typed decoder")
			require.Equal(t, tc.effort, input.ReasoningEffort, "the unsupported input must actually reach the raw record")
			manifest, parts := s1bFix3RecordParts(raw)
			before := snapshotHome(t, api.homePath)
			got := callPackageImport(t, api, s1bArchive(t, manifest, parts))
			if tc.mustRefuse || (tc.effort != "" && got.Code == http.StatusUnprocessableEntity) {
				er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, "element_not_creatable")
				if tc.mustRefuse {
					assert.Equal(t, "reasoning_effort", packageErrField(er))
					assert.Contains(t, er.Error, agentmutation.ExternalCLIEffortRefusal, "canonical control keeps the exact external-runtime refusal")
				}
				// Alias rejection may name the noncanonical discriminator instead;
				// the brief fixes its code, not an effort-specific error message.
				require.Empty(t, packageStagingIDs(t, api.homePath))
				require.Empty(t, agentEntityNames(t, api.homePath))
				require.Empty(t, listedAgentIDs(t, api))
				requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
				return
			}
			require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
			preview := decodePreview(t, got.Body)
			require.Len(t, preview.WillAdd, 1)
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			applied := callPackageApply(t, api, preview.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, applied.Code, "body=%s", applied.Body)
			result := decodeResult(t, applied.Body)
			require.Len(t, result.Created, 1)
			stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
			require.NoError(t, err)
			require.True(t, stored.Agent.IsExternalCLIWorker(), "F4: an accepted alias record must still be external, never a native worker")
			assert.Equal(t, "", stored.Agent.ReasoningEffort, "F4: a mixed-case runtime discriminator must not persist unsupported reasoning_effort")
			assert.Equal(t, "", stored.Soul, "promptless external identity survives the accepted path")
			assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId)
		})
	}
}

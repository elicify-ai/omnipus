// packages_b1_iterations_red_test.go — CHECK F05.
//
// Field mapping: max_tool_iterations is kept only inside 1–1000 and at or
// below the target's global limit. Anything else is 422 element_not_creatable,
// field max_tool_iterations, at upload and at apply. An omitted field is not
// an own limit. Explicit -1 is a present value outside 1–1000; the mapper
// currently treats only "above the global limit" as a refusal and then drops
// non-positive values before create validation.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// iterationsBoundSentence is the create rule's own text
// (config.MaxToolIterationsBoundMessage). Literal, so the oracle is not the
// production constant.
const iterationsBoundSentence = "max_tool_iterations must be between 1 and 1000"

const iterationsField = "max_tool_iterations"

func iterationsRecord(present bool, v any) map[string]any {
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	if present {
		rec["max_tool_iterations"] = v
	}
	return rec
}

func iterationsPackage(t *testing.T, present bool, v any) []byte {
	t.Helper()
	return buildAgentPackage(t, specSourceID, "Writer", iterationsRecord(present, v), []byte("Answer in rhyme.\n"), nil, 0)
}

func setGlobalMaxToolIterations(t *testing.T, api *restAPI, n int) {
	t.Helper()
	api.agentLoop.GetConfig().Agents.Defaults.MaxToolIterations = n
	got := api.agentLoop.GetConfig().Agents.Defaults.EffectiveGlobalMaxToolIterations().Value
	require.Equal(t, n, got, "the apply/upload check must see the lowered or raised global limit")
}

func uploadIterationsRefusal(t *testing.T, present bool, v any, reason string, global int) {
	t.Helper()
	api := packageAPI(t)
	if global > 0 {
		setGlobalMaxToolIterations(t, api, global)
	} else {
		got := api.agentLoop.GetConfig().Agents.Defaults.EffectiveGlobalMaxToolIterations().Value
		require.Equal(t, 200, got, "Dataset D5 row 11: a fresh install's global limit is 200")
	}
	before := snapshotHome(t, api.homePath)
	cfgBefore := append([]byte(nil), configBytes(t, api.homePath)...)
	slotsBefore := packageStagingIDs(t, api.homePath)
	agentsBefore := agentEntityNames(t, api.homePath)
	auditBefore := len(auditJSONLLines(t, api.homePath))
	got := callPackageImport(t, api, iterationsPackage(t, present, v))
	er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
	require.Equal(t, iterationsField, packageErrField(er), "body=%s", got.Body)
	require.Contains(t, er.Error, reason, "body=%s", got.Body)
	require.Equal(t, slotsBefore, packageStagingIDs(t, api.homePath), "FR-020: refused upload leaves no staging slot")
	require.Equal(t, agentsBefore, agentEntityNames(t, api.homePath), "no partial agent")
	require.Equal(t, cfgBefore, configBytes(t, api.homePath))
	requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
	requireUploadRefusalAudit(t, api.homePath, auditBefore)
}

func uploadIterationsAccepted(t *testing.T, present bool, v any) {
	t.Helper()
	api := packageAPI(t)
	got := callPackageImport(t, api, iterationsPackage(t, present, v))
	require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
}

func applyIterationsRefusal(t *testing.T, present bool, v any, reason string, global int) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, validMainPackage(t))
	if global > 0 {
		setGlobalMaxToolIterations(t, api, global)
	}
	replaceStagedPackage(t, api, prev.StagingId, iterationsPackage(t, present, v))
	agentsBefore := agentEntityNames(t, api.homePath)
	auditBefore := len(auditJSONLLines(t, api.homePath))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
	require.Equal(t, iterationsField, packageErrField(er), "body=%s", got.Body)
	require.Contains(t, er.Error, reason, "body=%s", got.Body)
	require.Equal(t, agentsBefore, agentEntityNames(t, api.homePath), "no partial agent")
	require.Equal(t, auditBefore, len(auditJSONLLines(t, api.homePath)), "a pre-commit refusal writes no audit entry")
	state, err := api.packageRegistry().State(prev.StagingId)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReady, state)
}

func applyIterationsAccepted(t *testing.T, present bool, v any, want any) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, iterationsPackage(t, present, v))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	raw, err := os.ReadFile(filepath.Join(api.homePath, "entities", "agents", res.Created[0].NewId+".json"))
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored), "record=%s", raw)
	require.Equal(t, want, stored["max_tool_iterations"])
}

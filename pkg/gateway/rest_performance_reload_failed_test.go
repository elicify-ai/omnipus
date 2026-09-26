// rest_performance_reload_failed_test.go — #904 gate round 2, item 3: the
// typed performance_reload_failed body (contracts/components/schemas/
// PerformanceReloadFailedError.yaml + PerformanceReloadFailedDetails.yaml).
//
// Oracles come from the contract text: stage "refresh" = config.json written
// but the in-memory config NOT swapped (GET still shows the OLD values);
// stage "reload" = in-memory config updated, registry reload failed (GET
// shows the NEW values); changed_fields = the Performance fields present in
// the PUT body; lowered_agents = the agents this request lowered (always
// present, empty when none). The message names only the changed settings.

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// decodeReloadFailed decodes the 500 body strictly into the generated type:
// an unknown field or a missing details object fails the test.
func decodeReloadFailed(t *testing.T, body []byte) gen.PerformanceReloadFailedError {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var out gen.PerformanceReloadFailedError
	require.NoError(t, dec.Decode(&out), "body must match PerformanceReloadFailedError: %s", body)
	var raw struct {
		Details map[string]json.RawMessage `json:"details"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))
	for _, k := range []string{"stage", "changed_fields", "lowered_agents"} {
		_, ok := raw.Details[k]
		require.True(t, ok, "details.%s is required by the contract; body: %s", k, body)
	}
	return out
}

// breakInMemoryRefresh makes the next refreshConfigAndRewireServices fail
// AFTER config.json is written: the strict roster load refuses an entity
// store whose only record is unparseable (gateway_boot_roster.go).
func breakInMemoryRefresh(t *testing.T, api *restAPI) {
	t.Helper()
	dir := filepath.Join(api.homePath, "entities", "agents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600))
}

func TestPerformancePut_ReloadFailed_RefreshStage_OnlyChangedFieldNamed(t *testing.T) {
	api := newMTIAPI(t, "200")
	before := mtiGetPerf(t, api)["max_parallel_agents"]
	breakInMemoryRefresh(t, api)

	w := mtiPutPerf(t, api, `{"max_parallel_agents":3}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	got := decodeReloadFailed(t, w.Body.Bytes())

	assert.Equal(t, "performance_reload_failed", got.Code)
	assert.Equal(t, gen.PerformanceReloadFailedDetailsStageRefresh, got.Details.Stage)
	assert.Equal(t, []gen.PerformanceReloadFailedDetailsChangedFields{
		gen.PerformanceReloadFailedDetailsChangedFieldsMaxParallelAgents,
	}, got.Details.ChangedFields)
	assert.Empty(t, got.Details.LoweredAgents)
	msg := strings.ToLower(got.Error)
	assert.NotContains(t, msg, "tool-iteration", "only max_parallel_agents changed; message: %s", got.Error)
	assert.Contains(t, msg, "parallel", "message must name the changed setting: %s", got.Error)

	// Stage refresh: the running config was NOT swapped, GET shows the old value.
	assert.Equal(t, before, mtiGetPerf(t, api)["max_parallel_agents"])
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"max_parallel_agents": 3`, "the value IS saved on disk")
}

func TestPerformancePut_ReloadFailed_ReloadStage_ToolIterationLimit(t *testing.T) {
	api := newMTIAPI(t, "200")
	api.agentLoop.SetReloadFunc(func() error { return fmt.Errorf("injected reload failure") })

	w := mtiPutPerf(t, api, `{"max_tool_iterations":300,"goal_max_rounds":9}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	got := decodeReloadFailed(t, w.Body.Bytes())

	assert.Equal(t, "performance_reload_failed", got.Code)
	assert.Equal(t, gen.PerformanceReloadFailedDetailsStageReload, got.Details.Stage)
	assert.Equal(t, []gen.PerformanceReloadFailedDetailsChangedFields{
		gen.PerformanceReloadFailedDetailsChangedFieldsGoalMaxRounds,
		gen.PerformanceReloadFailedDetailsChangedFieldsMaxToolIterations,
	}, got.Details.ChangedFields)
	assert.Empty(t, got.Details.LoweredAgents)
	assert.Contains(t, strings.ToLower(got.Error), "tool-iteration")

	// Stage reload: the in-memory config WAS updated, GET shows the new value.
	assert.EqualValues(t, 300, mtiGetPerf(t, api)["max_tool_iterations"])
}

func TestPerformancePut_ReloadFailed_ReloadStage_CarriesLoweredAgents(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "a", own: 250})
	api.agentLoop.SetReloadFunc(func() error { return fmt.Errorf("injected reload failure") })

	w := mtiPutPerf(t, api, `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"a","old_value":250}]}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	got := decodeReloadFailed(t, w.Body.Bytes())

	assert.Equal(t, gen.PerformanceReloadFailedDetailsStageReload, got.Details.Stage)
	require.Len(t, got.Details.LoweredAgents, 1)
	assert.Equal(t, "a", got.Details.LoweredAgents[0].AgentId)
	assert.Equal(t, 250, got.Details.LoweredAgents[0].OldValue)
	assert.Equal(t, 200, got.Details.LoweredAgents[0].NewValue)
}

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

	"github.com/elicify-ai/omnipus/pkg/agentstore"
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

// CodeQL clear-text logging (PR #932): a refresh failure's error text can
// carry a credential reference name, so PUT /performance's own log lines and
// its response carry only the fixed stage, never the cause text. The roster
// failure injected here has a recognisable cause ("unparseable") standing in
// for any such text.
func TestPerformancePut_ReloadFailed_CauseTextNotLoggedOrReturned(t *testing.T) {
	api := newMTIAPI(t, "200")
	logs := captureMTILogs(t)
	breakInMemoryRefresh(t, api)

	w := mtiPutPerf(t, api, `{"max_parallel_agents":3}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "unparseable", "the response must not echo the cause text")

	logs.mu.Lock()
	all := logs.buf.String()
	logs.mu.Unlock()
	require.Contains(t, all, "unparseable",
		"instrument: the cause text must be visible somewhere in the capture (refreshConfigAndRewireServices logs it)")
	var perfLines []string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "rest: PUT /performance") {
			perfLines = append(perfLines, l)
		}
	}
	require.NotEmpty(t, perfLines, "instrument: PUT /performance must log the saved-but-not-applied outcome")
	for _, l := range perfLines {
		assert.NotContains(t, l, "unparseable", "PUT /performance log line carries the cause text: %s", l)
		// Formatting-agnostic: logsafe.go's helpers currently hand slog one
		// slice argument (rendered !BADKEY=[stage refresh …]), reported
		// separately; only the presence of the stage value matters here.
		assert.Contains(t, l, "refresh", "the log line names the stage: %s", l)
	}
}

// After a refresh-stage failure config.json holds the new global while the
// in-memory config still holds the old one. The next preview / PUT must
// decide raise-vs-lower and the affected set from the SAVED global (the
// value the write builds on), not the stale in-memory copy: here the saved
// global is 500 (in memory still 300), so 400 is a LOWERING that must list
// agent a (own 450) and require consent — the stale copy would call it a
// raise and leave a at 450 without asking (D11).
func TestPerformancePut_AfterRefreshFailure_DecidesFromSavedGlobal(t *testing.T) {
	api := newMTIAPI(t, "300", mtiAgent{id: "a", own: 450})
	// The lowering path reads agents through limitAgentStore; move the
	// record there so the entity store the refresh reads holds only an
	// unparseable record (breakInMemoryRefresh) and every refresh fails.
	limitHome := t.TempDir()
	rec, err := agentstore.New(api.homePath).Get("a")
	require.NoError(t, err)
	require.NoError(t, agentstore.New(limitHome).Create("a", rec))
	api.limitAgentStore = agentstore.New(limitHome)
	require.NoError(t, os.Remove(filepath.Join(api.homePath, "entities", "agents", "a.json")))
	breakInMemoryRefresh(t, api)

	w := mtiPutPerf(t, api, `{"max_tool_iterations":500}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.Equal(t, gen.PerformanceReloadFailedDetailsStageRefresh, decodeReloadFailed(t, w.Body.Bytes()).Details.Stage)
	require.EqualValues(t, 500, mtiDiskGlobal(t, api))
	require.EqualValues(t, 300, mtiGetPerf(t, api)["max_tool_iterations"], "instrument: memory is stale")

	p := mtiPreviewViaMux(t, api, "?value=400", true)
	require.Equal(t, http.StatusOK, p.Code, "body: %s", p.Body.String())
	mtiAssertAgentChanges(t, mtiDecode(t, p.Body.Bytes())["agents"], []mtiLowering{{id: "a", name: "Agent a", oldV: 450, newV: 400}})

	w = mtiPutPerf(t, api, `{"max_tool_iterations":400}`)
	require.Equal(t, http.StatusConflict, w.Code, "an unconfirmed lowering is drift; body: %s", w.Body.String())
	assert.EqualValues(t, 500, mtiDiskGlobal(t, api), "nothing written on drift")

	w = mtiPutPerf(t, api, `{"max_tool_iterations":400,"confirmed_lowering":[{"agent_id":"a","old_value":450}]}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	got := decodeReloadFailed(t, w.Body.Bytes())
	require.Len(t, got.Details.LoweredAgents, 1)
	assert.Equal(t, 450, got.Details.LoweredAgents[0].OldValue)
	assert.Equal(t, 400, got.Details.LoweredAgents[0].NewValue)
	lowered, err := agentstore.New(limitHome).Get("a")
	require.NoError(t, err)
	assert.Equal(t, 400, lowered.MaxToolIterations)
}

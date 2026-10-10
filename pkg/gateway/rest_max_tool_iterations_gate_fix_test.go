// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Backend-lead's own tests for the #904 review-gate fixes
// (docs/internal/specs/tool-iteration-limit-spec.md; founder decisions D20,
// D21 in tool-iteration-limit-interview.md). Expected values come from those
// decisions and the error codes named in contracts/openapi.yaml, not from the
// implementation.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func gateFixDiskDefaults(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return gateFixDefaultsOf(t, m)
}

// gateFixDefaultsOf returns m["agents"]["defaults"], failing the test when
// either level is missing or not a JSON object.
func gateFixDefaultsOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	agents, ok := m["agents"].(map[string]any)
	require.True(t, ok, "config.json: agents is not an object: %#v", m["agents"])
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok, "config.json: agents.defaults is not an object: %#v", agents["defaults"])
	return defaults
}

// gateFixSetDiskMarker writes the env-import marker into config.json.
func gateFixSetDiskMarker(t *testing.T, api *restAPI) {
	t.Helper()
	p := filepath.Join(api.homePath, "config.json")
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gateFixDefaultsOf(t, m)["max_tool_iterations_env_imported"] = true
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, out, 0o600))
}

func gateFixPutConfig(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)
	return w
}

func gateFixErr(t *testing.T, w *httptest.ResponseRecorder) gen.ErrorResponse {
	t.Helper()
	var e gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), "body=%s", w.Body.String())
	return e
}

// D8/D15/D21: PUT /api/v1/config refuses the global and its marker in every
// body shape that encoding/json would bind to the field.
func TestGateFix_ConfigPUT_RefusesGlobalAndMarker(t *testing.T) {
	for name, body := range map[string]string{
		"nested global":        `{"agents":{"defaults":{"max_tool_iterations":900}}}`,
		"dotted global":        `{"agents.defaults.max_tool_iterations":900}`,
		"half-dotted global":   `{"agents.defaults":{"max_tool_iterations":900}}`,
		"upper-case global":    `{"agents":{"defaults":{"MAX_TOOL_ITERATIONS":900}}}`,
		"mixed-case ancestors": `{"Agents":{"Defaults":{"Max_Tool_Iterations":900}}}`,
		"nested marker":        `{"agents":{"defaults":{"max_tool_iterations_env_imported":false}}}`,
		"dotted marker":        `{"agents.defaults.max_tool_iterations_env_imported":false}`,
		"benign sibling too":   `{"agents":{"defaults":{"steering_mode":"all","max_tool_iterations":900}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			api := newLimitAPI(t, 300)
			w := gateFixPutConfig(t, api, body)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.Contains(t, gateFixErr(t, w).Error, "agents.defaults.max_tool_iterations")
			assert.EqualValues(t, 300, savedGlobal(t, api), "a refused PUT writes nothing")
			assert.Equal(t, 300, api.agentLoop.GetConfig().Agents.Defaults.MaxToolIterations)
		})
	}
}

// The one-level merge must not drop the global or the marker when a body
// replaces agents.defaults (the SPA's own Settings save shape).
func TestGateFix_ConfigPUT_DefaultsWritePreservesGlobalAndMarker(t *testing.T) {
	api := newLimitAPI(t, 300)
	gateFixSetDiskMarker(t, api)

	w := gateFixPutConfig(t, api, `{"agents":{"defaults":{"steering_mode":"all"}}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	d := gateFixDiskDefaults(t, api)
	assert.EqualValues(t, 300, d["max_tool_iterations"], "global survives the agents.defaults replace")
	assert.Equal(t, true, d["max_tool_iterations_env_imported"], "marker survives the agents.defaults replace")
	assert.Equal(t, "all", d["steering_mode"], "the requested field is written")
	g := api.agentLoop.GetConfig().Agents.Defaults.EffectiveGlobalMaxToolIterations()
	assert.Equal(t, 300, g.Value)
	assert.Equal(t, config.MaxToolIterationsSavedOK, g.SavedState, "not reported missing after the save")
}

// A body that would turn agents.defaults into a non-object is refused (400)
// and writes nothing.
func TestGateFix_ConfigPUT_NullDefaultsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"null defaults": `{"agents":{"defaults":null}}`,
		"null agents":   `{"agents":null}`,
		"scalar agents": `{"agents":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			api := newLimitAPI(t, 300)
			w := gateFixPutConfig(t, api, body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.EqualValues(t, 300, savedGlobal(t, api))
		})
	}
}

// D20: a raise never rewrites — no confirmation needed, the capped agent
// keeps its stored value; the preview of a raise is empty.
func TestGateFix_D20_RaiseNeverRewrites(t *testing.T) {
	api := newLimitAPI(t, 200, config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 500})

	w := httptest.NewRecorder()
	api.HandleMaxToolIterationsPreview(w,
		httptest.NewRequest(http.MethodGet, "/api/v1/performance/max-tool-iterations/preview?value=300", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var p gen.MaxToolIterationsLoweringPreview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Empty(t, p.Agents, "a raise (300 > 200 in force) previews no agent")
	require.NotNil(t, p.Agents, "agents is a required array, empty not null")

	// 300 is below A's stored 500 but ABOVE the global in force (200).
	w = putPerformanceJSON(t, api, `{"max_tool_iterations":300}`, true)
	require.Equal(t, http.StatusOK, w.Code, "a raise needs no confirmation (D20): %s", w.Body.String())
	assert.Equal(t, 500, storedOwnLimit(t, api, "agent-a"), "a raise never rewrites the stored value")
	assert.EqualValues(t, 300, savedGlobal(t, api))
	var ps gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ps))
	assert.Nil(t, ps.MaxToolIterationsLoweredAgents, "nothing lowered")

	// Unchanged value: also no rewrite, no confirmation.
	w = putPerformanceJSON(t, api, `{"max_tool_iterations":300}`, true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 500, storedOwnLimit(t, api, "agent-a"))

	// A lowering still asks (control: the rule did not become "never").
	w = putPerformanceJSON(t, api, `{"max_tool_iterations":250}`, true)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}

// PUT /performance writes the D6 marker with the global, so the retired env
// var can never overwrite an admin-set value on a later boot.
func TestGateFix_PerformancePUT_WritesImportMarker(t *testing.T) {
	api := newLimitAPI(t, 300)
	w := putPerformanceJSON(t, api, `{"max_tool_iterations":400}`, true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, true, gateFixDiskDefaults(t, api)["max_tool_iterations_env_imported"])
}

// readFaultStore fails ReadState, or the n-th List call, on demand.
type readFaultStore struct {
	*agentstore.Store
	mu         sync.Mutex
	failRead   bool
	failListAt int
	lists      int
}

var errInjectedRead = errors.New("injected read failure")

func (f *readFaultStore) List() ([]config.AgentConfig, []string, error) {
	f.mu.Lock()
	f.lists++
	n := f.lists
	f.mu.Unlock()
	if n == f.failListAt {
		return nil, nil, errInjectedRead
	}
	return f.Store.List()
}

func (f *readFaultStore) ReadState(id string) (*agentstore.State, error) {
	if f.failRead {
		return nil, errInjectedRead
	}
	return f.Store.ReadState(id)
}

// Item 4: a failed agent-store read on the lowering path is a 500 with its
// own code, never a drift 409, and writes nothing.
func TestGateFix_LoweringReadFailures_Are500(t *testing.T) {
	body := `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`
	cases := map[string]*readFaultStore{
		"ReadState fails in the deciding check": {failRead: true},
		"List fails in the pre-check":           {failListAt: 1},
		"List fails in the deciding check":      {failListAt: 2},
	}
	for name, fs := range cases {
		t.Run(name, func(t *testing.T) {
			api := newLimitAPI(t, 300, config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250})
			fs.Store = agentstore.New(api.homePath)
			api.limitAgentStore = fs
			w := putPerformanceJSON(t, api, body, true)
			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			e := gateFixErr(t, w)
			require.NotNil(t, e.Code)
			assert.Equal(t, "max_tool_iterations_agents_read_failed", *e.Code)
			assert.Equal(t, 250, storedOwnLimit(t, api, "agent-a"))
			assert.EqualValues(t, 300, savedGlobal(t, api))
		})
	}
}

// Item 4: rollback_incomplete carries a FIXED cause class in details.cause,
// never the raw storage error text.
//
// Leak round 6 (developer 2, work/session-core-mainfix-20261010): the raw I/O
// error used to be echoed in details.cause. The product now answers with fixed
// text and keeps the cause in the server log. This oracle therefore asserts
// the fixed text, the rollback-failure signal, and the ABSENCE of the injected
// cause text from the whole response.
//
// EXPECTED RED until developer 2's product fix merges: this branch runs against
// the pre-fix product, so the absence assertion fails by design.
func TestGateFix_RollbackIncomplete_CarriesCause(t *testing.T) {
	api := newLimitAPI(t, 300,
		config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250},
		config.AgentConfig{ID: "agent-b", Name: "B", MaxToolIterations: 280})
	api.limitAgentStore = &faultLimitStore{Store: agentstore.New(api.homePath),
		failOn: map[string][]bool{"agent-b": {true}, "agent-a": {false, true}}, calls: map[string]int{}}
	w := putPerformanceJSON(t, api, `{"max_tool_iterations":200,"confirmed_lowering":`+
		`[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]}`, true)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	e := gateFixErr(t, w)
	require.NotNil(t, e.Code)
	assert.Equal(t, "max_tool_iterations_rollback_incomplete", *e.Code)
	require.NotNil(t, e.Details)

	// The rollback-failure signal: details.cause still names the cause class.
	cause, ok := (*e.Details)["cause"]
	require.True(t, ok, "rollback_incomplete must still name its cause class in details.cause")
	fixed, _ := cause.(string)
	require.NotEmpty(t, fixed, "details.cause must be a fixed, non-empty cause class")

	// The fixed text: the product's own fixed write-failure class, not the raw
	// error. (If developer 2 pins a different literal, this one line changes.)
	assert.Equal(t, configWriteFailure(errInjectedIO).Error(), fixed,
		"details.cause must be the fixed cause class, not the raw error text")

	// The leak: the injected storage error text must reach the client NOWHERE.
	assert.NotContains(t, fixed, errInjectedIO.Error(),
		"details.cause leaked the raw storage error text")
	assert.NotContains(t, w.Body.String(), errInjectedIO.Error(),
		"the response body leaked the raw storage error text")
}

// Item 5: a committed save whose reload fails is 500
// performance_reload_failed; the writes stand, GET shows the saved global,
// and the lowered agents travel in details.lowered_agents.
func TestGateFix_ReloadFailure_CodeAndLoweredAgents(t *testing.T) {
	api := newLimitAPI(t, 300, config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250})
	api.agentLoop.SetReloadFunc(func() error { return errors.New("injected reload failure") })
	w := putPerformanceJSON(t, api,
		`{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`, true)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	e := gateFixErr(t, w)
	require.NotNil(t, e.Code)
	assert.Equal(t, "performance_reload_failed", *e.Code)
	require.NotNil(t, e.Details)
	lowered, _ := (*e.Details)["lowered_agents"].([]any)
	require.Len(t, lowered, 1, "details.lowered_agents: %v", *e.Details)
	row, _ := lowered[0].(map[string]any)
	assert.Equal(t, "agent-a", row["agent_id"])
	assert.EqualValues(t, 250, row["old_value"])
	assert.EqualValues(t, 200, row["new_value"])

	assert.EqualValues(t, 200, savedGlobal(t, api), "the save is committed")
	assert.Equal(t, 200, storedOwnLimit(t, api, "agent-a"), "the lowering is committed")
	g := httptest.NewRecorder()
	api.HandlePerformance(g, httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil))
	require.Equal(t, http.StatusOK, g.Code)
	var ps gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &ps))
	require.NotNil(t, ps.MaxToolIterations)
	assert.Equal(t, 200, *ps.MaxToolIterations, "GET after the failed reload shows the saved global")
}

// corruptAfterLowerStore lowers through the real store, then makes every
// agent record unparseable, so updateConfigJSONLocked's in-memory refresh
// (populateAgentsListFromEntityStoreStrict) fails AFTER config.json is
// written — the real refresh-failure path, no test-only hook.
type corruptAfterLowerStore struct {
	*agentstore.Store
	home string
}

func (c *corruptAfterLowerStore) MutateState(id, rev string, mutate func(*config.AgentConfig) error, soul *string) (agentstore.MutationResult, error) {
	res, err := c.Store.MutateState(id, rev, mutate, soul)
	if err == nil {
		p := filepath.Join(c.home, "entities", "agents", id+".json")
		if werr := os.WriteFile(p, []byte("{not json"), 0o600); werr != nil {
			return res, werr
		}
	}
	return res, err
}

// Coordinator ruling on item 4/5: when config.json is already written and the
// refresh step fails, the save is committed — 500 performance_reload_failed
// with details.lowered_agents, no rollback, never "nothing was changed".
func TestGateFix_RefreshFailureAfterWrite_IsReloadFailedNotRollback(t *testing.T) {
	api := newLimitAPI(t, 300, config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250})
	api.limitAgentStore = &corruptAfterLowerStore{Store: agentstore.New(api.homePath), home: api.homePath}
	w := putPerformanceJSON(t, api,
		`{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`, true)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	e := gateFixErr(t, w)
	require.NotNil(t, e.Code, w.Body.String())
	assert.Equal(t, "performance_reload_failed", *e.Code)
	assert.NotContains(t, e.Error, "nothing was changed")
	require.NotNil(t, e.Details)
	lowered, _ := (*e.Details)["lowered_agents"].([]any)
	require.Len(t, lowered, 1, "details.lowered_agents: %v", *e.Details)
	assert.EqualValues(t, 200, savedGlobal(t, api), "the global write stands")
}

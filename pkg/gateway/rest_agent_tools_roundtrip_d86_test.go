// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for UAT 2026-09-13 D-86: GET /api/v1/agents/{id}/tools
// returned {"config":{"builtin":{"policies":…}},"tools":[…],"agent_type":…}
// while PUT required {"builtin":{"policies":…}}, so echoing the GET body back
// was rejected ("builtin.policies is required …"). The PUT now accepts the GET
// shape as-is (config.builtin / config.mcp lifted; tools and agent_type
// ignored) and still rejects a body carrying neither shape.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestUpdateAgentTools_D86_GetBodyRoundTripsThroughPut(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)
	// The GET reads the loop config's agent list; mirror the stored map there
	// so the GET reports the agent's real explicit entries (bash: deny).
	stored, err := store.Get(agentID)
	require.NoError(t, err)
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].Tools = stored.Tools
		}
	}

	// GET the agent's tools exactly as the SPA or an operator's curl would.
	get := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(get, httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil), agentID)
	require.Equal(t, http.StatusOK, get.Code, "body=%s", get.Body.String())
	getBody := get.Body.String()

	// The response must carry the three top-level members D-86 saw, so the
	// round-trip below exercises the real shape and not a trimmed one.
	var echo map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(getBody), &echo))
	for _, k := range []string{"config", "tools", "agent_type"} {
		require.Contains(t, echo, k, "GET response must carry %q for this test to mean anything", k)
	}
	require.NotContains(t, echo, "builtin", "the GET shape has no top-level builtin — that is the whole defect")

	// Flip one entry inside config.builtin.policies and PUT the body back
	// otherwise unchanged (tools[] and agent_type still present).
	var cfgPart struct {
		Builtin struct {
			Policies map[string]string `json:"policies"`
		} `json:"builtin"`
	}
	require.NoError(t, json.Unmarshal(echo["config"], &cfgPart))
	require.Equal(t, "deny", cfgPart.Builtin.Policies["bash"], "precondition: bash is denied before the write")
	cfgPart.Builtin.Policies["bash"] = "ask"
	cfgRaw, err := json.Marshal(cfgPart)
	require.NoError(t, err)
	echo["config"] = cfgRaw
	putBody, err := json.Marshal(echo)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(string(putBody)))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "the GET body must be an acceptable PUT body; got body=%s", w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicy("ask"), got.Tools.Builtin.Policies["bash"],
		"the value edited inside config.builtin.policies must be the one persisted")
	assert.Len(t, got.Tools.Builtin.Policies, len(cfgPart.Builtin.Policies),
		"the whole map from config.builtin must be persisted, not an empty one")
}

func TestUpdateAgentTools_D86_NeitherShapeStillRejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	for name, body := range map[string]string{
		"config without builtin": `{"config":{"mcp":{"servers":[]}},"tools":[],"agent_type":"Main"}`,
		"tools and type only":    `{"tools":[],"agent_type":"Main"}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r = withReAuthAdmin(t, api, r)
			w := httptest.NewRecorder()
			api.updateAgentTools(w, r, agentID)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), "builtin.policies is required")

			got, err := store.Get(agentID)
			require.NoError(t, err)
			assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"], "nothing may be persisted")
		})
	}
}

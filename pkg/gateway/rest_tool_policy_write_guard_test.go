// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RELEASE BLOCKER regression tests for CLAUDE.md hard constraint 6 on the REST
// write surface. A live UAT round (2026-09-02, batches 2/3/4, real gateway, real
// REST calls) proved the documented guarantee — "every agent create/update/
// tools-write is rejected with 400 on a gap … never a silent runtime default" —
// did not hold on ANY of the three agent-policy write paths:
//
//	POST /api/v1/agents             a body omitting `bash` returned 201; a body
//	                                carrying a literal "*" key returned 201.
//	PUT  /api/v1/agents/{id}        a tools_cfg omitting `stop_plan` returned 200.
//	PUT  /api/v1/agents/{id}/tools  a body missing the required `builtin` wrapper
//	                                returned 200 and persisted
//	                                `"tools":{"builtin":{},"mcp":{}}` — after
//	                                which the agent, explicitly policied
//	                                `bash: deny`, executed bash successfully.
//
// One shared root cause, in two shapes. createAgent merged the caller's map ON
// TOP of a fully-enumerated deny-seeded baseline BEFORE validating, so a
// caller-side gap was always backfilled before the check ran. And on every path,
// the check itself (config.ValidateToolPolicyCoverage) counts a tool as covered
// when EITHER the global ceiling or the agent has an entry — and
// pkg/config/defaults.go seeds the ceiling with an explicit entry for the entire
// static catalog, so it reports zero gaps for any agent-side map, empty
// included. Runtime resolution is "one side is enough"
// (pkg/tools/compositor.go's resolveEffectivePolicyWith), so an agent-side hole
// does not fail closed: it inherits the permissive ceiling.
//
// Each test below FAILS on the pre-fix handlers (201/200) and passes after.
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// fullBuiltinPolicyMap returns an explicit, literal, wildcard-free entry for
// EVERY static builtin tool — the only shape hard constraint 6 accepts. Derived
// from the live catalog rather than a hand-listed set so the fixture cannot
// drift out of sync with the tool registry.
func fullBuiltinPolicyMap(policy string) map[string]string {
	known := buildKnownBuiltinToolNames()
	out := make(map[string]string, len(known))
	for name := range known {
		out[name] = policy
	}
	return out
}

func mustPolicyJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// seedGlobalCeiling gives the harness config a complete global
// sandbox.tool_policies map, which is what a real install always has
// (pkg/config/defaults.go seeds an explicit entry for the entire static
// catalog).
//
// This is required to make these tests MEAN anything. Without it the bare
// fixture agent already has 88 roster-wide coverage gaps, so the PRE-EXISTING
// config.ValidateToolPolicyCoverage guard 400s every create for reasons that
// have nothing to do with the caller's submitted map — and a test asserting
// only "400" would pass against the unfixed handler. Seeding the ceiling
// satisfies that older guard exactly as production does, leaving the new
// caller-side check as the only thing that can reject the request. Each
// rejection assertion below additionally pins the caller-side error text, so a
// 400 from the wrong guard fails.
func seedGlobalCeiling(t *testing.T, api *restAPI) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Sandbox.ToolPolicies = fullBuiltinPolicyMap("allow")
}

// seedAgentWithFullPolicy creates a real entity record whose builtin policy map
// is complete and denies `bash` — the "explicitly tightened agent" whose
// tightening the UAT watched disappear.
func seedAgentWithFullPolicy(t *testing.T, api *restAPI, agentID string) *agentstore.Store {
	t.Helper()
	policies := make(map[string]config.ToolPolicy)
	for name := range buildKnownBuiltinToolNames() {
		policies[name] = config.ToolPolicyAllow
	}
	policies["bash"] = config.ToolPolicyDeny
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:    agentID,
		Name:  "Test Agent",
		Type:  config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}},
	}))
	return store
}

// --- PUT /api/v1/agents/{id}/tools ---

// TestUpdateAgentTools_BodyMissingBuiltinWrapper_Rejected400 is the batch-2
// CRITICAL reproduction, and the most severe of the three because it failed in
// the ALLOW direction.
//
// BDD: Given an agent with a complete, explicit policy map that DENIES `bash`,
//
//	When PUT /api/v1/agents/{id}/tools is sent {"policies":{...}} — the
//	malformed shape a client assuming a PATCH-style partial update would send,
//	missing the required `builtin` wrapper,
//	Then the request is rejected 400, and the agent's persisted policy map is
//	byte-for-byte unchanged — `bash` is still explicitly denied.
//
// Pre-fix this returned 200 and wrote an empty builtin map, after which bash
// resolved "allow" from the global ceiling. Asserting the persisted STATE (not
// just the status code) is deliberate: a handler that 400s but has already
// written would pass a status-only assertion.
func TestUpdateAgentTools_BodyMissingBuiltinWrapper_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	body := `{"policies":` + mustPolicyJSON(t, map[string]string{"list_skills": "allow"}) + `}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a body with no \"builtin\" object must be rejected, never persisted as an empty policy; body=%s",
		w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, got.Tools)
	require.NotEmpty(t, got.Tools.Builtin.Policies,
		"the rejected write must leave the agent's policy map intact, not emptied")
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"],
		"THE BUG: the agent's explicit bash deny must survive a rejected malformed write")
}

// TestUpdateAgentTools_IncompletePolicyMap_Rejected400: the same endpoint with
// the correct wrapper but one tool omitted. The coverage guard could never see
// this (the seeded global ceiling covers `bash`), yet omitting it drops this
// agent's deny and hands the decision to the permissive ceiling.
func TestUpdateAgentTools_IncompletePolicyMap_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	delete(policies, "bash")
	body := `{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "builtin.policies",
		"the rejection must come from the caller-side submitted-map check")
	assert.Contains(t, w.Body.String(), "bash",
		"the 400 must name the omitted tool so the caller can fix the request")

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"],
		"a rejected write must not have loosened the agent's bash policy")
}

// TestUpdateAgentTools_WildcardKey_Rejected400: a literal "*" is not a policy
// entry for the static builtin catalog under hard constraint 6 — the MCP
// per-server wildcard is the only sanctioned wildcard, and it lives under the
// mcp_ namespace.
func TestUpdateAgentTools_WildcardKey_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	policies["*"] = "allow"
	body := `{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a wildcard key must be rejected outright, not stored inertly; body=%s", w.Body.String())
}

// TestUpdateAgentTools_CompleteMapStillAccepted is the other half of the
// contract: the guard must not turn a legitimate, complete write into a 400.
// Without this, "reject everything" would pass every test above.
func TestUpdateAgentTools_CompleteMapStillAccepted(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	policies["bash"] = "deny"
	policies["mcp_context7_*"] = "ask" // the documented MCP carve-out must pass
	body := `{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"])
	assert.Equal(t, config.ToolPolicy("ask"), got.Tools.Builtin.Policies["mcp_context7_*"],
		"MCP per-server bulk keys must still round-trip")
}

// --- POST /api/v1/agents ---

func postAgent(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.createAgent(w, r)
	return w
}

// TestCreateAgent_IncompletePolicyMap_Rejected400 is UAT batch 3 S48 / batch 4
// S83. Pre-fix this returned 201: createAgent copied the caller's entries on top
// of coreagent.NewCustomAgentToolsCfg()'s fully-enumerated deny seed BEFORE
// config.ValidateToolPolicyCoverage ran, so the omitted key was silently
// backfilled to "deny" — a value nobody sent — and the coverage check was dead
// code on this path.
func TestCreateAgent_IncompletePolicyMap_Rejected400(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	policies := fullBuiltinPolicyMap("allow")
	delete(policies, "bash")
	body := fmt.Sprintf(`{"type":"Main","name":"GapAgent","soul":"s","tools_cfg":{"builtin":{"policies":%s}}}`,
		mustPolicyJSON(t, policies))

	w := postAgent(t, api, body)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a create whose policy map omits a static builtin tool must be rejected, not silently seeded; body=%s",
		w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg.builtin.policies",
		"the rejection must come from the CALLER-side submitted-map check, not the older "+
			"roster-wide coverage guard (which the seeded ceiling satisfies)")
	assert.Contains(t, w.Body.String(), "bash", "and it must name the omitted tool")
}

// TestCreateAgent_WildcardKey_Rejected400: the merge loop copied every caller
// key verbatim with no check that it named a real tool, so a literal "*" was
// stored inertly alongside the real entries and the create returned 201.
func TestCreateAgent_WildcardKey_Rejected400(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	policies := fullBuiltinPolicyMap("allow")
	policies["*"] = "allow"
	body := fmt.Sprintf(`{"type":"Main","name":"WildcardAgent","soul":"s","tools_cfg":{"builtin":{"policies":%s}}}`,
		mustPolicyJSON(t, policies))

	w := postAgent(t, api, body)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a wildcard key in a create body must be rejected; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg.builtin.policies",
		"the rejection must come from the caller-side submitted-map check")
	assert.Contains(t, w.Body.String(), "wildcards are not valid",
		"and it must explain that a wildcard key is not a policy entry for the static catalog")
}

// TestCreateAgent_CompletePolicyMap_StillAccepted / and a create with NO
// tools_cfg at all: the guard must fire only on a caller-supplied map. A request
// that omits tools_cfg legitimately falls through to the server-generated,
// complete, deny-seeded baseline — that is not a caller gap, and rejecting it
// would break every create the SPA makes for an inheriting subagent.
func TestCreateAgent_CompleteOrAbsentPolicyMap_StillAccepted(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	t.Run("complete map accepted", func(t *testing.T) {
		policies := fullBuiltinPolicyMap("allow")
		policies["bash"] = "deny"
		body := fmt.Sprintf(`{"type":"Main","name":"CompleteAgent","soul":"s","tools_cfg":{"builtin":{"policies":%s}}}`,
			mustPolicyJSON(t, policies))
		w := postAgent(t, api, body)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	})

	t.Run("absent tools_cfg accepted", func(t *testing.T) {
		w := postAgent(t, api, `{"type":"Main","name":"SeededAgent","soul":"s"}`)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	})
}

// --- PUT /api/v1/agents/{id} (tools_cfg) ---

// TestUpdateAgent_ToolsCfgIncomplete_Rejected400 is UAT batch 4 S83 leg 2: a
// tools_cfg sent to the general agent-update endpoint REPLACES the agent's
// builtin map wholesale, so an omitted key silently drops that agent's own
// posture for the omitted tool.
func TestUpdateAgent_ToolsCfgIncomplete_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	delete(policies, "stop_plan")
	body := fmt.Sprintf(`{"tools_cfg":{"builtin":{"policies":%s}}}`, mustPolicyJSON(t, policies))

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a tools_cfg omitting a static builtin tool must be rejected; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg.builtin.policies",
		"the rejection must come from the caller-side submitted-map check")
	assert.Contains(t, w.Body.String(), "stop_plan", "and it must name the omitted tool")

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"],
		"the rejected write must have persisted nothing")
}

// --- GET /api/v1/agents/{id}/tools round-trip ---

// TestGetAgentTools_ConfigPoliciesCoverFullCatalog closes the loop the write-side
// guards open. The SPA builds its PUT body by spreading the map this GET returns
// and overwriting one key (ToolsAndPermissions.tsx's cfgToValue → valueToCfg).
// If this response reported only the agent's own, possibly-sparse stored map,
// then any agent whose stored map predates a catalog addition — or was emptied
// by the malformed-body defect above — would round-trip an incomplete map and be
// rejected by the very guards added here. Read must therefore return a body that
// is a VALID write.
func TestGetAgentTools_ConfigPoliciesCoverFullCatalog(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	// A deliberately sparse stored map — one entry only.
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Sparse Agent",
		Type: config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
			Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny},
		}},
	}))
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].Tools = &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny},
			}}
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil)
	w := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Config struct {
			Builtin struct {
				Policies map[string]string `json:"policies"`
			} `json:"builtin"`
		} `json:"config"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	defects := config.ValidateSubmittedToolPolicyMap(
		resp.Config.Builtin.Policies, buildKnownBuiltinToolNames())
	assert.True(t, defects.Empty(),
		"the GET response's config.builtin.policies must itself be an acceptable PUT body, "+
			"or the SPA's read/modify/write cycle breaks: %s", defects.String())
	assert.Equal(t, "deny", resp.Config.Builtin.Policies["bash"],
		"the agent's own explicit entry must be reported as-is, not overwritten by the ceiling")
}

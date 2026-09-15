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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
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

// --- POST /api/v1/agents ---

func postAgent(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.createAgent(w, r)
	return w
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestUpdateAgentTools_PolicyOnlyUpdatePreservesMCPBindings pins the fix for a
// live, unrecoverable data-loss bug: a builtin tool-policy update through the
// Agents UI silently wiped the agent's MCP server bindings.
//
// The full chain, all four links confirmed:
//  1. No gateway read path populates tools_cfg — grepping `ToolsCfg:` across
//     the non-test pkg/gateway sources returns nothing.
//  2. The SPA's ToolsAndPermissions editor builds its payload with
//     `valueToCfg`, which spreads the agent's *existing* cfg and overwrites
//     `builtin.policies`. Because of (1) that existing cfg never carries
//     `mcp`, so the request body never carries it either.
//  3. updateAgentTools built a fresh AgentToolsCfg and assigned it wholesale,
//     populating MCP only when the request supplied servers.
//  4. The write is triggered by useAutoSave, so a single allow/ask/deny toggle
//     destroyed the bindings — no Save click, no confirmation, and no way to
//     restore them from the UI.
//
// The sibling handler updateAgent already had exactly this preservation
// branch, with a comment naming the same hazard. One handler learned the
// lesson and the other did not, which is the whole bug.
//
// Asserts the OUTCOME (the bindings are still on the record afterwards)
// rather than the mechanism, because a test that merely checks "the else
// branch was taken" would pass just as happily against a branch that
// preserved the wrong thing.
func TestUpdateAgentTools_PolicyOnlyUpdatePreservesMCPBindings(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)

	// Seed a REAL entity record carrying MCP bindings — the persist step does
	// a read-modify-write against the agent store, so an in-memory-only agent
	// would not exercise the preservation branch at all.
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Test Agent",
		Type: config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{
			MCP: config.AgentMCPToolsCfg{
				Servers: []config.AgentMCPServerBinding{
					{ID: "context7", Tools: []string{"query-docs"}},
					{ID: "tavily", Tools: []string{"search"}},
				},
			},
		},
	}))

	// A policy-only body, exactly as the SPA sends it: no "mcp" key at all.
	// The coverage guard (CLAUDE.md constraint 6) requires an explicit,
	// wildcard-free entry for every builtin, so enumerate the real catalog
	// rather than hand-listing a few names that would drift.
	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "allow"
	}
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"builtin":{"policies":` + string(policiesJSON) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// The property: the bindings are still there, intact, with their per-server
	// tool allow-lists. A 200 proves nothing on its own — the wiping version of
	// this handler also returned 200.
	got, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, got.Tools, "tools cfg must exist after the update")
	require.Len(t, got.Tools.MCP.Servers, 2,
		"a builtin-policy-only update must not drop the agent's MCP server bindings")

	byID := make(map[string][]string, 2)
	for _, s := range got.Tools.MCP.Servers {
		byID[s.ID] = s.Tools
	}
	require.Equal(t, []string{"query-docs"}, byID["context7"])
	require.Equal(t, []string{"search"}, byID["tavily"])

	// And the update itself still landed — preservation must not come at the
	// cost of the write the caller actually asked for.
	require.NotEmpty(t, got.Tools.Builtin.Policies, "the policy update must still persist")
}

// TestUpdateAgentTools_ExplicitMCPServersStillReplace is the other half of the
// contract: preservation applies only when the request OMITS mcp. A caller
// that genuinely sends servers must still be able to change them — otherwise
// the fix above would turn the field into write-once.
func TestUpdateAgentTools_ExplicitMCPServersStillReplace(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)

	// The handler 422s on a binding to an MCP server that is not configured
	// globally, so register one for the replacement to target.
	cfg := api.agentLoop.GetConfig()
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"playwright": {},
	}

	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Test Agent",
		Type: config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{
			MCP: config.AgentMCPToolsCfg{
				Servers: []config.AgentMCPServerBinding{
					{ID: "context7", Tools: []string{"query-docs"}},
				},
			},
		},
	}))

	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "allow"
	}
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"builtin":{"policies":` + string(policiesJSON) +
		`},"mcp":{"servers":[{"id":"playwright","tools":["navigate"]}]}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, got.Tools)
	require.Len(t, got.Tools.MCP.Servers, 1)
	require.Equal(t, "playwright", got.Tools.MCP.Servers[0].ID,
		"an explicit mcp payload must replace the bindings, not merge into them")
}

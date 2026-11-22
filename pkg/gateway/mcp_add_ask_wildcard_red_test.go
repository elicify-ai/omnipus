// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 19.
// TestMcpAdd_WritesAskWildcard: adding a service writes the global Ask key
// for all its tools, present and future (C-7, R30b), unless a broader global
// key already covers the service (C-18's "covered" meaning; Dataset D8 rows
// 10-11).
//
// Oracles: skills-tools-redesign-spec.md C-7 ("adding a service writes the
// global key `mcp_<server>_*` = `ask` when no global key already covers the
// service, which covers every tool the service has now and adds later") and
// BDD S-59 (notion added with 4 tools → all rows Ask; a later 5th tool also
// Ask — carried by the wildcard form itself), S-04 (no tool row is ever
// Unset — the ask key is what removes Unset). The add_mcp_server agent-tool
// surface of FR-045 is order 19's second file
// (pkg/sysagent/tools/mcp_add_ask_wildcard_red_test.go).
//
// pkg/gateway/rest_mcp.go::addMCPServer persists the server but writes no
// such key at this commit; per the qa-lead RED rule the test fails LOUD for
// that missing behaviour.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"

	"github.com/stretchr/testify/require"
)

// postMcpServer POSTs a minimal stdio service through the REST handler.
func postMcpServer(t *testing.T, api *restAPI, name string) *httptest.ResponseRecorder {
	t.Helper()
	cmd := "npx"
	body, err := json.Marshal(gen.McpServerCreate{
		Name:      name,
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
	})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleMCPServers(w, r)
	return w
}

// TestMcpAdd_WritesAskWildcard (order 19).
// Traces to: BDD S-59, S-04; spec C-7, C-18 (covered meaning); Dataset D8 rows 10-11.
func TestMcpAdd_WritesAskWildcard(t *testing.T) {
	t.Run("S-59 a new service writes mcp_<server>_* = ask", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		w := postMcpServer(t, api, "notion")
		require.Equal(t, http.StatusCreated, w.Code, "setup: %s", w.Body.String())
		got := api.agentLoop.GetConfig().Sandbox.ToolPolicies["mcp_notion_*"]
		require.Equal(t, "ask", got,
			"C-7: POST /mcp-servers must write the global key mcp_notion_* = ask; got %q (ceiling: %v)",
			got, api.agentLoop.GetConfig().Sandbox.ToolPolicies)
	})

	t.Run("S-59 D8#11 a broader global key covering the service writes nothing", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		// C-18: `mcp_*: deny` covers every service, so adding notion writes no
		// server key — the rows show deny through the broader key. Setup seeds
		// the ceiling directly; a fresh harness config has not reconciled its
		// ceiling yet, so create the map if the boot reconcile has not.
		ceiling := api.agentLoop.GetConfig().Sandbox.ToolPolicies
		if ceiling == nil {
			ceiling = map[string]string{}
			api.agentLoop.GetConfig().Sandbox.ToolPolicies = ceiling
		}
		ceiling["mcp_*"] = "deny"
		w := postMcpServer(t, api, "notion")
		require.Equal(t, http.StatusCreated, w.Code, "setup: %s", w.Body.String())
		got, written := api.agentLoop.GetConfig().Sandbox.ToolPolicies["mcp_notion_*"]
		require.False(t, written,
			"C-18/D8#11: a covered service must not gain its own key; got %q", got)
	})
}

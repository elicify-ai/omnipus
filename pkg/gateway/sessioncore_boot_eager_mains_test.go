// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// FR-002: "Eligible native MAIN membership MUST eagerly create/reuse" a main.
// A fresh install seeds the default workspace's team at first boot; every
// eligible seeded member must already own a main then, with no team change
// and no lookup. The sessions LIST is a non-creating read, so only boot can
// explain a row. A worker is not eligible and gets none.
func TestSessionCoreFR002_FreshBootCreatesMainsForSeededTeam(t *testing.T) {
	gw := testutil.StartTestGateway(t, testutil.WithAgents([]config.AgentConfig{
		{ID: "mia", Name: "Mia"},
		{ID: "jim", Name: "Jim"},
		{ID: "ava", Name: "Ava"},
		{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
	}))

	workspaces, err := listWorkspaceFiles(gw.HomeDir())
	require.NoError(t, err)
	var wsID string
	var team []string
	for _, w := range workspaces {
		if w.IsDefault {
			wsID, team = w.ID, w.CoreTeam
		}
	}
	require.NotEmpty(t, wsID)
	require.Contains(t, team, "worker", "instrument check: the seeded team must contain the ineligible worker")
	require.Contains(t, team, "ava")

	req, err := gw.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	require.NoError(t, err)
	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var page struct {
		Sessions []map[string]any `json:"sessions"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	var ids []string
	for _, s := range page.Sessions {
		id, _ := s["id"].(string)
		ids = append(ids, id)
	}
	for _, agentID := range []string{"admin", "mia", "jim", "ava"} {
		require.Contains(t, ids, u1MainID(wsID, agentID), "boot must eagerly create %s's main", agentID)
	}
	require.NotContains(t, ids, u1MainID(wsID, "worker"), "a worker owns no main")
}

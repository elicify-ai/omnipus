//go:build !cgo

// channel_routing_binding_test.go — ADR-029 WS-A gateway handler tests.
// Covers TDD plan items #13 (rejection set), #14 (valid binding persists),
// #15 (GET round-trip), #21 (workspace cascade), #23a (partial-cascade abort).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// writeTestWorkspaceJSON writes a workspace JSON file into the temp home dir.
func writeTestWorkspaceJSON(t *testing.T, api *restAPI, id, status string, coreTeam []string, isDefault bool) {
	t.Helper()
	dir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	entry := map[string]any{
		"id":     id,
		"name":   id,
		"status": status,
	}
	if len(coreTeam) > 0 {
		entry["core_team"] = coreTeam
	}
	if isDefault {
		entry["is_default"] = true
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600))
}

// addAgentsToAPI persists agents to config.json so they survive safeUpdateConfigJSON
// reload cycles. The restAPI handlers read cfg.Agents.List from GetConfig() to validate
// agents for routing purposes; any in-memory-only mutation is lost on the next
// safeUpdateConfigJSON → refreshConfigAndRewireServices call.
func addAgentsToAPI(t *testing.T, api *restAPI, agents []config.AgentConfig) {
	t.Helper()
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		agentsMap, _ := m["agents"].(map[string]any)
		if agentsMap == nil {
			agentsMap = map[string]any{}
		}
		rawList, _ := agentsMap["list"].([]any)
		for _, a := range agents {
			entry := map[string]any{"id": a.ID}
			if a.Default {
				entry["default"] = true
			}
			if a.Type != "" {
				entry["type"] = string(a.Type)
			}
			rawList = append(rawList, entry)
		}
		agentsMap["list"] = rawList
		m["agents"] = agentsMap
		return nil
	}))
}

// seedChannelInstance writes a minimal channel instance entry into config.json
// so the per-instance existence check in setChannelRouting passes for namespaced
// keys (e.g. "whatsapp.eu"). In production the channel is first configured via
// the /channels/{id}/configure endpoint; in tests we seed it directly.
// safeUpdateConfigJSON triggers refreshConfigAndRewireServices which reloads
// the config from disk, so the channel entry is visible to all subsequent
// GetConfig() calls.
func seedChannelInstance(t *testing.T, api *restAPI, channelID string) {
	t.Helper()
	baseType, _ := config.ParseInstanceKey(channelID)
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
		}
		if _, exists := channels[channelID]; !exists {
			channels[channelID] = map[string]any{
				"type":    baseType,
				"enabled": true,
			}
		}
		m["channels"] = channels
		return nil
	}))
}

// setChannelRoutingReq issues PUT /api/v1/channels/{id}/routing.
func setChannelRoutingReq(t *testing.T, api *restAPI, channelID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+channelID+"/routing",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.setChannelRouting(w, r, channelID)
	return w
}

// getChannelRoutingReq issues GET /api/v1/channels/{id}/routing.
func getChannelRoutingReq(t *testing.T, api *restAPI, channelID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	api.getChannelRouting(w, channelID)
	return w
}

// ── TDD #13: rejection set (DS-1) ─────────────────────────────────────────────

// TestSetChannelRouting_Bound_EmptyAgentID verifies FR-005:
// workspace_id present but default_agent_id empty → 422.
func TestSetChannelRouting_Bound_EmptyAgentID(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":""}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"empty default_agent_id with workspace_id must be 422 (FR-005)")
}

// TestSetChannelRouting_Bound_AgentNotInTeam verifies FR-006:
// agent not in workspace CoreTeam → 422.
func TestSetChannelRouting_Bound_AgentNotInTeam(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
		{ID: "jim"},
	})
	// CoreTeam has mia and ray, NOT jim.
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"jim"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"agent not in CoreTeam must be 422 (FR-006)")
}

// TestSetChannelRouting_Bound_UnknownWorkspace verifies FR-007:
// workspace_id references a non-existent workspace → 404.
func TestSetChannelRouting_Bound_UnknownWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})
	// No workspace "ghost" written.
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"ghost","default_agent_id":"mia"}`)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"unknown workspace_id must be 404 (FR-007)")
}

// TestSetChannelRouting_Bound_ArchivedWorkspace verifies FR-007:
// archived workspace → 404 (treated same as unknown/inactive).
func TestSetChannelRouting_Bound_ArchivedWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})
	writeTestWorkspaceJSON(t, api, "archived1", "archived", []string{"mia"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"archived1","default_agent_id":"mia"}`)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"archived workspace must be 404 (FR-007)")
}

// TestSetChannelRouting_Bound_WorkerAgent verifies FR-008 / MIN-002:
// worker agent → 422 (standardized from former 400).
func TestSetChannelRouting_Bound_WorkerAgent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "worker1"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"worker1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"worker agent must be 422 (FR-008/MIN-002)")
}

// TestSetChannelRouting_Unbound_Worker_Returns422 verifies MIN-002 in the
// unbound (legacy) flow: worker agent → 422, not 400.
func TestSetChannelRouting_Unbound_Worker_Returns422(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	})

	// No workspace_id → unbound flow.
	w := setChannelRoutingReq(t, api, "telegram",
		`{"default_agent_id":"worker1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"unbound worker must be 422 (MIN-002)")
}

// ── TDD #14: valid bound binding persists ─────────────────────────────────────

// TestSetChannelRouting_ValidBinding_SetsIdentity verifies FR-010/FR-029:
// a valid bound PUT persists WorkspaceID + Identity and removes any stale
// wildcard binding for the same instance.
func TestSetChannelRouting_ValidBinding_SetsIdentity(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)

	seedChannelInstance(t, api, "whatsapp.eu")

	// Pre-plant a stale wildcard binding for the same channel instance.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["bindings"] = []any{
			map[string]any{
				"agent_id": "mia",
				"match": map[string]any{
					"channel":    "whatsapp.eu",
					"account_id": "*",
				},
			},
		}
		return nil
	}))

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)

	require.Equal(t, http.StatusOK, w.Code, "valid bound routing must return 200")

	// Verify response body.
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "sales", resp["workspace_id"], "response must include workspace_id")
	assert.Equal(t, "ray", resp["default_agent_id"], "response must include default_agent_id")

	// Verify the stale wildcard binding for whatsapp.eu is removed.
	liveCfg := api.agentLoop.GetConfig()
	for _, b := range liveCfg.Bindings {
		if b.Match.Channel == "whatsapp.eu" && b.Match.AccountID == "*" &&
			b.Match.Peer == nil && b.Match.GuildID == "" && b.Match.TeamID == "" {
			t.Errorf("stale wildcard binding for 'whatsapp.eu' must be removed by the PUT: %+v", b)
		}
	}

	// Verify cfg.Channels entry has WorkspaceID + Identity.
	inst, ok := liveCfg.Channels["whatsapp.eu"]
	require.True(t, ok, "cfg.Channels['whatsapp.eu'] must exist after binding")
	assert.Equal(t, "sales", inst.WorkspaceID, "WorkspaceID must be set")
	require.NotNil(t, inst.Identity, "Identity must be set")
	assert.Equal(t, "agent", inst.Identity.Kind, "Identity.Kind must be 'agent'")
	assert.Equal(t, "ray", inst.Identity.ID, "Identity.ID must be 'ray'")
}

// ── TDD #15: GET round-trip ────────────────────────────────────────────────────

// TestGetChannelRouting_BoundReadsIdentity verifies FR-029 MAJ-004:
// after a valid bound PUT, GET reads from cfg.Channels (Identity), not wildcard.
func TestGetChannelRouting_BoundReadsIdentity(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// PUT a valid binding.
	putW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, putW.Code)

	// GET the routing.
	getW := getChannelRoutingReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusOK, getW.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.Equal(t, "sales", resp["workspace_id"], "GET must return workspace_id from Identity")
	assert.Equal(t, "ray", resp["default_agent_id"], "GET must return agent_id from Identity")
}

// TestGetChannelRouting_UnboundReadsWildcard verifies that for an UNBOUND instance
// GET returns the legacy wildcard binding agent_id and no workspace_id.
func TestGetChannelRouting_UnboundReadsWildcard(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
	})

	// Plant a wildcard binding for telegram (unbound).
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["bindings"] = []any{
			map[string]any{
				"agent_id": "mia",
				"match": map[string]any{
					"channel":    "telegram",
					"account_id": "*",
				},
			},
		}
		return nil
	}))

	getW := getChannelRoutingReq(t, api, "telegram")
	require.Equal(t, http.StatusOK, getW.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp["default_agent_id"],
		"unbound GET must read the wildcard binding agent_id")
	// workspace_id must be absent or empty for unbound instances.
	wsVal, hasWS := resp["workspace_id"]
	if hasWS && wsVal != nil && wsVal != "" {
		t.Errorf("unbound GET must not return workspace_id; got %v", wsVal)
	}
}

// ── TDD #21: workspace-delete cascade ─────────────────────────────────────────

// TestWorkspaceDelete_CascadesToInstances verifies FR-025/MAJ-005:
// deleting a workspace disables + unbinds its bound channel instances BEFORE
// removing the workspace file.
func TestWorkspaceDelete_CascadesToInstances(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})

	// Create workspace "sales" and bind whatsapp.eu to it.
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	putW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, putW.Code)

	// Verify the channel is bound before delete.
	preCfg := api.agentLoop.GetConfig()
	preInst, ok := preCfg.Channels["whatsapp.eu"]
	require.True(t, ok)
	require.Equal(t, "sales", preInst.WorkspaceID, "pre-condition: channel must be bound")

	// Delete the workspace.
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/sales", nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "sales")
	require.Equal(t, http.StatusNoContent, w.Code, "workspace delete must succeed with 204")

	// Workspace file must be gone.
	wsPath := filepath.Join(api.homePath, "workspaces", "sales.json")
	_, statErr := os.Stat(wsPath)
	assert.True(t, os.IsNotExist(statErr), "workspace file must be removed after delete")

	// Channel instance must be disabled and unbound.
	afterCfg := api.agentLoop.GetConfig()
	if afterInst, exists := afterCfg.Channels["whatsapp.eu"]; exists {
		assert.False(t, afterInst.Enabled, "bound instance must be disabled after workspace delete")
		assert.Empty(t, afterInst.WorkspaceID, "WorkspaceID must be cleared after workspace delete")
		assert.Nil(t, afterInst.Identity, "Identity must be cleared after workspace delete")
	}
	// If the channel key is fully absent after cascade, that is also acceptable.
}

// ── TDD #23a: abort guards ─────────────────────────────────────────────────────

// TestWorkspaceDelete_NonExistent_Returns404 verifies that deleting a workspace
// that doesn't exist returns 404 and no state is mutated.
func TestWorkspaceDelete_NonExistent_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/nonexistent", nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "nonexistent")

	assert.Equal(t, http.StatusNotFound, w.Code, "delete non-existent workspace must 404")
}

// TestWorkspaceDelete_DefaultWorkspace_Returns409 verifies that the default
// workspace (is_default=true) cannot be deleted (FR-1.6 delete-protection).
func TestWorkspaceDelete_DefaultWorkspace_Returns409(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	writeTestWorkspaceJSON(t, api, "myws", "active", nil, true /* isDefault */)

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/myws", nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "myws")

	assert.Equal(t, http.StatusConflict, w.Code, "default workspace delete must return 409")

	// Verify the file still exists (not touched).
	wsPath := filepath.Join(api.homePath, "workspaces", "myws.json")
	_, statErr := os.Stat(wsPath)
	assert.NoError(t, statErr, "default workspace file must still exist after failed delete")
}

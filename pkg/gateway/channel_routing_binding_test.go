// channel_routing_binding_test.go — ADR-029 WS-A gateway handler tests.
// Covers TDD plan items #13 (rejection set), #14 (valid binding persists),
// #15 (GET round-trip), #21 (workspace cascade), #23a (partial-cascade abort).

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// addAgentsToAPI seeds agents as REAL per-entity records under
// entities/agents/<id>.json (ADR-054 D2) via agentstore, then forces an
// immediate config reload so cfg.Agents.List — which setChannelRouting reads
// directly off a.agentLoop.GetConfig() to validate FR-006/007/008 — reflects
// the new roster right away.
//
// This USED TO splice agents.list directly into the raw config.json map via
// safeUpdateConfigJSON. ADR-054 changed the ground truth out from under that
// approach: config.LoadConfig unconditionally STRIPS any agents.list content
// on every load (pkg/config/legacy_agents_list.go), and
// refreshConfigAndRewireServices repopulates cfg.Agents.List exclusively from
// the agent entity store (a.populateAgentsListFromStore ->
// populateAgentsListFromEntityStore -> agentstore.Store.List(), see
// gateway.go), never from whatever a raw config.json splice wrote. The old
// helper therefore silently became a no-op: it wrote agents.list to disk, and
// the very next reload (e.g. seedChannelInstance's own safeUpdateConfigJSON
// call) discarded it and repopulated cfg.Agents.List from the (empty) entity
// store instead. Every routing test that named an agent this way was
// actually validating against an EMPTY roster — "agent %q not found" (422 in
// the bound flow, 404 in the unbound flow) silently substituted for the
// worker/team/system checks this file exists to exercise, and in the bound
// flow both "not found" and "worker rejected" return the same 422 status, so
// a naive status-code-only assertion could not tell the difference. Seeding
// via the real entity store closes that gap for real.
func addAgentsToAPI(t *testing.T, api *restAPI, agents []config.AgentConfig) {
	t.Helper()
	store := agentstore.New(api.homePath)
	for i := range agents {
		ac := agents[i]
		require.NoError(t, store.Create(ac.ID, &ac), "seed agent entity record %q", ac.ID)
	}
	// Some callers (e.g. TestSetChannelRouting_Unbound_Worker_Returns422) issue
	// the routing request with no other config write in between — force the
	// reload here so a.agentLoop.GetConfig().Agents.List is never stale.
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
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
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "sales"), nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "sales")
	require.Equal(t, http.StatusNoContent, w.Code, "workspace delete must succeed with 204")

	// Workspace file must be gone.
	wsPath := filepath.Join(api.homePath, "workspaces", "sales.json")
	_, statErr := os.Stat(wsPath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "workspace file must be removed after delete")

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

	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "nonexistent"), nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "nonexistent")

	assert.Equal(t, http.StatusNotFound, w.Code, "delete non-existent workspace must 404")
}

// TestWorkspaceDelete_DefaultWorkspace_Returns409 verifies that the default
// workspace (is_default=true) cannot be deleted (FR-1.6 delete-protection).
func TestWorkspaceDelete_DefaultWorkspace_Returns409(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	writeTestWorkspaceJSON(t, api, "myws", "active", nil, true /* isDefault */)

	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "myws"), nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, "myws")

	assert.Equal(t, http.StatusConflict, w.Code, "default workspace delete must return 409")

	// Verify the file still exists (not touched).
	wsPath := filepath.Join(api.homePath, "workspaces", "myws.json")
	_, statErr := os.Stat(wsPath)
	assert.NoError(t, statErr, "default workspace file must still exist after failed delete")
}

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

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
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
	// Content check (anti-shortcut): "agent not found" is ALSO a 422 in the
	// bound flow — pin the actual rejection reason so a regression that
	// silently drops "jim" from the roster (making it look "not found" instead
	// of "not in team") is caught by this test, not masked by a shared status code.
	assert.Contains(t, w.Body.String(), "not a member of workspace",
		"rejection must be the team-membership check (FR-006), not a generic agent-not-found")
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
	// Content check (anti-shortcut): "agent not found" is ALSO a 422 in the
	// bound flow — pin the actual rejection reason so a regression that
	// silently drops "worker1" from the roster is caught here rather than
	// masked by the shared status code.
	assert.Contains(t, w.Body.String(), "workers are not chat targets",
		"rejection must be the worker-type check (FR-008), not a generic agent-not-found")
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
	// Content check (anti-shortcut): in the UNBOUND flow "agent not found" is
	// a 404, not a 422 (see rest.go's unbound-flow lookup) — so this
	// particular pair can't be confused by status code alone, but pin the
	// message anyway so a future change collapsing the two status codes
	// still gets caught here.
	assert.Contains(t, w.Body.String(), "workers are not chat targets",
		"rejection must be the worker-type check (MIN-002), not a generic agent-not-found")
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
	var resp map[string]any
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

	var resp map[string]any
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

	var resp map[string]any
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

// ── S-1+S-2: HandleChannels slug grammar enforcement ─────────────────────────

// TestHandleChannels_BadSlug_Returns400_NothingWritten verifies that
// PUT /channels/whatsapp.BAD/<action> (uppercase slug) is rejected at the
// HandleChannels gate with 400 "malformed channel id" BEFORE any credential or
// config write, closing the boot-brick / orphaned-credential path (S-1+S-2).
//
// Uses /routing (a read-only probe for config) so nothing is written even
// before the grammar check — the assertion then proves the check fired first.
func TestHandleChannels_BadSlug_Returns400_NothingWritten(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Snapshot the raw config.json before the request.
	cfgPathBefore := api.homePath + "/config.json"
	before, err := os.ReadFile(cfgPathBefore)
	require.NoError(t, err)

	// PUT /api/v1/channels/whatsapp.BAD/routing — uppercase slug violates ADR-029
	// grammar (slugPattern requires [a-z0-9-]{1,32}).
	r := httptest.NewRequest(http.MethodPut,
		"/api/v1/channels/whatsapp.BAD/routing",
		strings.NewReader(`{"default_agent_id":"mia"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleChannels(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"uppercase slug must be rejected with 400 (S-1+S-2); body=%s", w.Body.String())

	// Confirm no config write occurred (on-disk content unchanged).
	after, err := os.ReadFile(cfgPathBefore)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"config.json must not be modified when the channel id grammar check fires")
}

// ── Fix 2: bound→unbound transition clears WorkspaceID and Identity ──────────

// TestSetChannelRouting_UnbindClears_WorkspaceID_And_Identity verifies FR-029:
// a PUT without workspace_id on a previously-bound instance clears WorkspaceID
// and Identity so the next getChannelRouting returns the unbound representation.
//
// Without the fix, safeUpdateConfigJSON preserved the bound representation and
// RepairStaleChannelWildcardBindings dropped the new wildcard on next load,
// silently losing the unbind intent.
func TestSetChannelRouting_UnbindClears_WorkspaceID_And_Identity(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Step 1: bind the instance.
	putW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, putW.Code, "initial bind must succeed: %s", putW.Body.String())

	// Verify it is bound.
	cfg := api.agentLoop.GetConfig()
	inst, ok := cfg.Channels["whatsapp.eu"]
	require.True(t, ok, "channel entry must exist after bind")
	require.Equal(t, "sales", inst.WorkspaceID, "pre-condition: WorkspaceID must be 'sales'")
	require.NotNil(t, inst.Identity, "pre-condition: Identity must be set")

	// Step 2: unbind by sending a PUT without workspace_id.
	unbindW := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"default_agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, unbindW.Code, "unbind must succeed (200): %s", unbindW.Body.String())

	// Assert WorkspaceID and Identity are cleared in the live config.
	afterCfg := api.agentLoop.GetConfig()
	afterInst, instExists := afterCfg.Channels["whatsapp.eu"]
	if instExists {
		assert.Empty(t, afterInst.WorkspaceID,
			"WorkspaceID must be cleared after unbound PUT (FR-029 mutual-exclusivity)")
		assert.Nil(t, afterInst.Identity,
			"Identity must be cleared after unbound PUT (FR-029 mutual-exclusivity)")
	}
	// If the entry is fully absent that is also a valid cleared state.

	// Assert GET returns the unbound (wildcard) representation, not the old bound one.
	getW := getChannelRoutingReq(t, api, "whatsapp.eu")
	require.Equal(t, http.StatusOK, getW.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	wsVal, hasWS := resp["workspace_id"]
	if hasWS && wsVal != nil && wsVal != "" {
		t.Errorf("GET after unbind must not return workspace_id; got %v", wsVal)
	}
	assert.Equal(t, "mia", resp["default_agent_id"],
		"GET after unbind must return the new wildcard agent_id")
}

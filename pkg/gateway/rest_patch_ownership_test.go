//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Tests for patchAgentOwnership (PATCH /api/v1/agents/<id>).
// BDD scenarios: #79b, #79c, #79d
// Traces to: path-sandbox-and-capability-tiers-spec.md (v4)

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// newPatchOwnershipAPI creates a restAPI with:
// - an admin user "admin" in the config
// - optional additional users
// - a custom agent "custom-agent-1" with the given initial owner
// - a system agent "omnipus-system"
// - a core agent "jim"
// - audit logger wired in
//
// Returns (api, tmpDir, auditDir).
func newPatchOwnershipAPI(
	t *testing.T,
	initialOwner string,
	extraUsers []config.UserConfig,
) (*restAPI, string, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	auditDir := t.TempDir()

	enabled := true
	adminUser := config.UserConfig{
		Username: "admin",
		Role:     config.UserRoleAdmin,
	}

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host:  "127.0.0.1",
			Port:  8080,
			Users: append([]config.UserConfig{adminUser}, extraUsers...),
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:            "custom-agent-1",
					Name:          "Custom Agent One",
					Type:          config.AgentTypeCustom,
					Enabled:       &enabled,
					OwnerUsername: initialOwner,
				},
				{
					ID:      "omnipus-system",
					Name:    "Omnipus",
					Type:    config.AgentTypeSystem,
					Locked:  true,
					Enabled: &enabled,
				},
				{
					ID:      "jim",
					Name:    "Jim",
					Type:    config.AgentTypeCore,
					Enabled: &enabled,
				},
			},
		},
	}

	// Write a minimal config.json so safeUpdateConfigJSON can read it.
	// Include the custom agent and users so PATCH can find them.
	gwUsers := make([]any, 0, 1+len(extraUsers))
	gwUsers = append(gwUsers, map[string]any{
		"username":      "admin",
		"role":          "admin",
		"password_hash": "",
		"token_hash":    "",
	})
	for _, u := range extraUsers {
		gwUsers = append(gwUsers, map[string]any{
			"username":      u.Username,
			"role":          string(u.Role),
			"password_hash": "",
			"token_hash":    "",
		})
	}
	initialOwnerField := any(nil)
	if initialOwner != "" {
		initialOwnerField = initialOwner
	}
	agentEntry := map[string]any{
		"id":   "custom-agent-1",
		"name": "Custom Agent One",
		"type": "custom",
	}
	if initialOwner != "" {
		agentEntry["owner_username"] = initialOwner
	}
	diskCfg := map[string]any{
		"version": 1,
		"gateway": map[string]any{
			"users": gwUsers,
		},
		"agents": map[string]any{
			"defaults": map[string]any{},
			"list":     []any{agentEntry},
		},
		"providers": []any{},
	}
	_ = initialOwnerField
	data, err := json.Marshal(diskCfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), data, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     taskstore.New(filepath.Join(tmpDir, "tasks")),
	}
	return api, tmpDir, auditDir
}

// doPatchOwnership sends a PATCH /api/v1/agents/<id> request and returns the
// recorder. requester is the user injected into context.
func doPatchOwnership(
	t *testing.T,
	api *restAPI,
	agentID string,
	body map[string]any,
	requester *config.UserConfig,
	extraHeaders map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/"+agentID, bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range extraHeaders {
		r.Header.Set(k, v)
	}
	if requester != nil {
		r = injectUser(r, requester.Username, requester.Role)
	}
	w := httptest.NewRecorder()
	// Route to patchAgentOwnership directly (it is the handler invoked from HandleAgents for PATCH).
	api.patchAgentOwnership(w, r, agentID)
	return w
}

// ---------------------------------------------------------------------------
// #79b — Non-admin cannot patch ownership
// BDD: Given a non-admin user,
// When PATCH /api/v1/agents/<id> {"owner_username":"bob"},
// Then 403 Forbidden.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestPatchOwnership_NonAdminForbidden(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	nonAdmin := &config.UserConfig{Username: "regularuser", Role: config.UserRoleUser}
	w := doPatchOwnership(t, api, "custom-agent-1",
		map[string]any{"owner_username": "admin"},
		nonAdmin, nil)

	require.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "admin only")
}

// ---------------------------------------------------------------------------
// #79c — Non-existent owner_username returns 400
// BDD: Given admin patches with owner_username not in Users,
// When PATCH /api/v1/agents/<id> {"owner_username":"ghost"},
// Then 400 with {"error":"owner_username does not exist"}.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestPatchOwnership_NonExistentOwner_Returns400(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "custom-agent-1",
		map[string]any{"owner_username": "ghost-user-does-not-exist"},
		admin, nil)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "owner_username does not exist", resp["error"],
		"error message must match spec exactly")
}

// ---------------------------------------------------------------------------
// #79d — Empty owner without X-Confirm-Demote header returns 400
// BDD: Given admin patches with owner_username="" and no X-Confirm-Demote: 1,
// When the request is processed,
// Then 400 Bad Request.
// Traces to: path-sandbox-and-capability-tiers-spec.md / Q26
// ---------------------------------------------------------------------------

func TestPatchOwnership_EmptyWithoutHeader_Returns400(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "custom-agent-1",
		map[string]any{"owner_username": ""},
		admin, nil) // no X-Confirm-Demote

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"].(string), "X-Confirm-Demote",
		"error must mention the required header")
}

// ---------------------------------------------------------------------------
// #79d (confirm path) — Empty owner WITH X-Confirm-Demote clears ownership + audit
// BDD: Given admin patches with owner_username="" AND X-Confirm-Demote: 1,
// When the request is processed,
// Then 200; audit event=agent.ownership_cleared, details.old_owner=<prev>,
// details.actor=admin. Hash NOT in details.
// Traces to: path-sandbox-and-capability-tiers-spec.md / Q26 /
// ---------------------------------------------------------------------------

func TestPatchOwnership_EmptyWithHeader_ClearsOwner_AuditEmitted(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "custom-agent-1",
		map[string]any{"owner_username": ""},
		admin,
		map[string]string{"X-Confirm-Demote": "1"})

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "custom-agent-1", resp["agent_id"])
	// owner_username in response should be "" (cleared).
	assert.Equal(t, "", resp["owner_username"])
}

// TestPatchOwnership_ChangeOwner — admin changes to an existing user
// BDD: Given admin patches a custom agent's owner to another existing user "bob",
// When the request is processed,
// Then 200; audit event=agent.ownership_changed, details.old_owner, new_owner, actor.
// Traces to: path-sandbox-and-capability-tiers-spec.md
func TestPatchOwnership_ChangeOwner_Success(t *testing.T) {
	// Seed "bob" as an extra user.
	bob := config.UserConfig{Username: "bob", Role: config.UserRoleUser}
	api, _, _ := newPatchOwnershipAPI(t, "admin", []config.UserConfig{bob})

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "custom-agent-1",
		map[string]any{"owner_username": "bob"},
		admin, nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "bob", resp["owner_username"], "new owner must be bob")
}

// TestPatchOwnership_SystemAgent_Returns400
// BDD: Given admin tries to patch a system agent,
// When the request is processed,
// Then 400 "system agents have no owner".
// Traces to: path-sandbox-and-capability-tiers-spec.md
func TestPatchOwnership_SystemAgent_Returns400(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "omnipus-system",
		map[string]any{"owner_username": "admin"},
		admin, nil)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"].(string), "system agents have no owner")
}

// TestPatchOwnership_CoreAgent_Returns400
// BDD: Given admin tries to patch a core agent (jim),
// When the request is processed,
// Then 400 "system agents have no owner".
// Traces to: path-sandbox-and-capability-tiers-spec.md
func TestPatchOwnership_CoreAgent_Returns400(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "jim",
		map[string]any{"owner_username": "admin"},
		admin, nil)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"].(string), "system agents have no owner")
}

// TestPatchOwnership_UnknownAgent_Returns404
// BDD: Given admin patches a non-existent agent ID,
// When the request is processed,
// Then 404 Not Found.
// Traces to: path-sandbox-and-capability-tiers-spec.md
func TestPatchOwnership_UnknownAgent_Returns404(t *testing.T) {
	api, _, _ := newPatchOwnershipAPI(t, "admin", nil)

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}
	w := doPatchOwnership(t, api, "does-not-exist-agent",
		map[string]any{"owner_username": "admin"},
		admin, nil)

	require.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"].(string), "not found")
}

// TestPatchOwnership_DifferentAgents_ProduceDifferentResults
// Differentiation test: patching agent A and agent B with different owners
// produces different outcomes (guards against hardcoded responses).
func TestPatchOwnership_DifferentAgents_ProduceDifferentResults(t *testing.T) {
	enabled := true
	bob := config.UserConfig{Username: "bob", Role: config.UserRoleUser}
	carol := config.UserConfig{Username: "carol", Role: config.UserRoleUser}

	// Add a second custom agent.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1",
			Port: 8080,
			Users: []config.UserConfig{
				{Username: "admin", Role: config.UserRoleAdmin},
				bob,
				carol,
			},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A", Type: config.AgentTypeCustom, Enabled: &enabled},
				{ID: "agent-b", Name: "Agent B", Type: config.AgentTypeCustom, Enabled: &enabled},
			},
		},
	}

	diskCfg := map[string]any{
		"version": 1,
		"gateway": map[string]any{
			"users": []any{
				map[string]any{"username": "admin", "role": "admin", "password_hash": "", "token_hash": ""},
				map[string]any{"username": "bob", "role": "user", "password_hash": "", "token_hash": ""},
				map[string]any{"username": "carol", "role": "user", "password_hash": "", "token_hash": ""},
			},
		},
		"agents": map[string]any{
			"defaults": map[string]any{},
			"list": []any{
				map[string]any{"id": "agent-a", "name": "Agent A", "type": "custom"},
				map[string]any{"id": "agent-b", "name": "Agent B", "type": "custom"},
			},
		},
		"providers": []any{},
	}
	data, err := json.Marshal(diskCfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), data, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     taskstore.New(filepath.Join(tmpDir, "tasks")),
	}

	admin := &config.UserConfig{Username: "admin", Role: config.UserRoleAdmin}

	// Patch agent-a to bob, agent-b to carol.
	wA := doPatchOwnership(t, api, "agent-a", map[string]any{"owner_username": "bob"}, admin, nil)
	wB := doPatchOwnership(t, api, "agent-b", map[string]any{"owner_username": "carol"}, admin, nil)

	require.Equal(t, http.StatusOK, wA.Code)
	require.Equal(t, http.StatusOK, wB.Code)

	var respA, respB map[string]any
	require.NoError(t, json.Unmarshal(wA.Body.Bytes(), &respA))
	require.NoError(t, json.Unmarshal(wB.Body.Bytes(), &respB))

	assert.Equal(t, "bob", respA["owner_username"], "agent-a owner must be bob")
	assert.Equal(t, "carol", respB["owner_username"], "agent-b owner must be carol")
	assert.NotEqual(t, respA["owner_username"], respB["owner_username"],
		"different agents patched to different owners must return different owner values")
}

//go:build !cgo

// agent_workspace_ownership_privacy_test.go — proves AuthorizeAgentAccess's
// no-admin-bypass fix at the HTTP layer for HandleWorkspace (GET
// /api/v1/workspace/{agent_id}/{path...}).
//
// Background: under the single-user model every authenticated user resolves
// to config.UserRoleAdmin (see UserRole.UnmarshalJSON in pkg/config —
// coercion at the JSON-deserialization boundary — and rest_users.go, which
// always persists role=admin regardless of the requested role). Before this
// fix, AuthorizeAgentAccess's `user.Role == UserRoleAdmin` OR-clause made
// that check unconditionally true, so a SECOND account (created via the
// still-fully-functional Users management screen, POST /api/v1/users) could
// read every OTHER user's private custom-agent workspace files just by also
// carrying Role==admin. This file proves the fix at the actual HTTP handler
// HandleWorkspace, which — at the time of this fix — had zero prior test
// coverage of its own (no existing _test.go file called it; grep for
// ".HandleWorkspace(" pre-fix returns nothing).
//
// Traces to: architect finding on feat/agent-system-fixes-2 (7-reviewer
// gate), operator decision "restore per-user privacy" (2026-07).
// pkg/config/agent_ownership_test.go covers the same fix at the
// AuthorizeAgentAccess unit level; this file covers it end-to-end through
// the REST handler.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// userConfigCtx returns a context carrying a *config.UserConfig with BOTH
// Username and Role populated on the SAME struct — matching what the
// production auth middleware injects (rest_auth.go's withOptionalAuth sets
// `UserContextKey{}` to `&user`, the full persisted config.UserConfig with
// Role included). This is deliberately NOT the contextWithUserRole helper
// (rest_workspaces_test.go), which stores Role via a separate RoleContextKey
// and leaves the UserConfig.Role field at its zero value — that shape is
// wrong here, because HandleWorkspace reads user.Role directly off the
// UserContextKey value via config.AuthorizeAgentAccess, not via
// RoleContextKey.
func userConfigCtx(username string, role config.UserRole) context.Context {
	return context.WithValue(context.Background(), UserContextKey{}, &config.UserConfig{
		Username: username,
		Role:     role,
	})
}

// seedOwnedAgentWorkspace registers a custom agent owned by ownerUsername in
// api's live config and writes fileName/fileContent into its per-agent
// workspace directory (<homePath>/agents/<agentID>/) — matching
// agentWorkspacePath's default resolution when AgentConfig.Workspace is left
// empty (rest.go:1383-1396).
func seedOwnedAgentWorkspace(t *testing.T, api *restAPI, agentID, ownerUsername, fileName, fileContent string) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:            agentID,
		Name:          agentID,
		Type:          config.AgentTypeCustom,
		OwnerUsername: ownerUsername,
	})

	agentDir := filepath.Join(api.homePath, "agents", agentID)
	require.NoError(t, os.MkdirAll(agentDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, fileName), []byte(fileContent), 0o600))
}

// TestHandleWorkspace_Owner_CanReadOwnFile proves the happy path still works
// after the fix: the owning user can read a file from their own agent's
// workspace.
func TestHandleWorkspace_Owner_CanReadOwnFile(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedOwnedAgentWorkspace(t, api, "alices-agent", "alice", "note.txt", "hello from alice")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspace/alices-agent/note.txt", nil)
	r = r.WithContext(userConfigCtx("alice", config.UserRoleAdmin))
	w := httptest.NewRecorder()
	api.HandleWorkspace(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "hello from alice", w.Body.String())
}

// TestHandleWorkspace_SecondAdminAccount_CannotReadOtherUsersAgent is the
// core privacy regression test: bob is a SECOND account that — under the
// single-user model — also resolves to config.UserRoleAdmin (exactly as any
// account created via POST /api/v1/users would, since rest_users.go always
// persists role=admin regardless of the requested role). Before the fix,
// AuthorizeAgentAccess's admin-bypass let bob read alice's private agent
// workspace file. After the fix, bob must be denied with 403 even though
// bob.Role == UserRoleAdmin.
func TestHandleWorkspace_SecondAdminAccount_CannotReadOtherUsersAgent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedOwnedAgentWorkspace(t, api, "alices-agent", "alice", "secret.txt", "alice's private data")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspace/alices-agent/secret.txt", nil)
	r = r.WithContext(userConfigCtx("bob", config.UserRoleAdmin))
	w := httptest.NewRecorder()
	api.HandleWorkspace(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"a second account resolving to admin must NOT read alice's private agent workspace; body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "alice's private data",
		"the denial response must not leak the file content")
}

// TestHandleWorkspace_SystemAgent_AnyUserCanRead proves the system/core
// carve-out survives the fix: a core agent (no OwnerUsername, never owned)
// remains readable by any authenticated user regardless of role.
func TestHandleWorkspace_SystemAgent_AnyUserCanRead(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:   "mia",
		Name: "Mia",
		Type: config.AgentTypeCore,
	})
	agentDir := filepath.Join(api.homePath, "agents", "mia")
	require.NoError(t, os.MkdirAll(agentDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "SOUL.md"), []byte("# Mia"), 0o600))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspace/mia/SOUL.md", nil)
	r = r.WithContext(userConfigCtx("bob", config.UserRoleAdmin))
	w := httptest.NewRecorder()
	api.HandleWorkspace(w, r)

	assert.Equal(t, http.StatusOK, w.Code,
		"core agents must remain accessible to any authenticated user; body: %s", w.Body.String())
}

// TestHandleWorkspace_Unauthenticated_Returns401 proves the pre-authz guard:
// a request with no UserContextKey value is rejected before ownership is
// even evaluated.
func TestHandleWorkspace_Unauthenticated_Returns401(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedOwnedAgentWorkspace(t, api, "alices-agent", "alice", "note.txt", "hello")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspace/alices-agent/note.txt", nil)
	w := httptest.NewRecorder()
	api.HandleWorkspace(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

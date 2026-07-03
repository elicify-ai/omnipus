//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/task"
)

// canonicalTokenRE enforces the generateUserToken() format contract:
//
//	"omnipus_" + hex-encode(4 random id bytes) + "_" + hex-encode(32 random body bytes)
//
// SEC-1 / UAT #399: tokens now embed a non-secret 8-hex-char ID prefix so
// verification can index directly to the matching hash in the user's token
// SET. Any test that claims "login returns a canonical token" must verify
// against this pattern, not just non-emptiness. Violating the prefix, the ID
// segment, or the body length is a regression.
var canonicalTokenRE = regexp.MustCompile(`^omnipus_[0-9a-f]{8}_[0-9a-f]{64}$`)

// newUserMgmtAPI builds a restAPI with:
//   - Tmp dir with config.json seeded with the supplied user list.
//   - Audit logging enabled (sandbox.audit_log=true) so audit-capture tests
//     have a real Logger wired on the AgentLoop.
//   - dev_mode_bypass=false (default) unless overridden by the caller.
//
// Returns (api, tmpDir) so audit tests can read audit.jsonl directly.
func newUserMgmtAPI(t *testing.T, users []any) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: workspaceDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	// Seed the in-memory Users slice so that withAuth reads them before
	// any REST-initiated write triggers a refresh from disk.
	for _, u := range users {
		um := u.(map[string]any)
		cfg.Gateway.Users = append(cfg.Gateway.Users, config.UserConfig{
			Username:     asString(um["username"]),
			PasswordHash: asString(um["password_hash"]),
			TokenHash:    config.BcryptHash(asString(um["token_hash"])),
			Role:         config.UserRole(asString(um["role"])),
		})
	}

	diskCfg := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"sandbox":   map[string]any{"audit_log": true},
		"gateway":   map[string]any{"users": users},
	}
	data, err := json.Marshal(diskCfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), data, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
	}
	return api, tmpDir
}

// newUserMgmtAPIWithAdmin is the most common starting state: exactly one
// admin named "admin" with password "admin-pass-1234". Returns (api,
// tmpDir, adminPassword).
func newUserMgmtAPIWithAdmin(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	const pw = "admin-pass-1234"
	// MinCost here is intentional: tests that only care about the hash
	// existing tolerate the weaker cost in exchange for a ~100ms speedup.
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{
			"username":      "admin",
			"password_hash": string(hash),
			"token_hash":    "",
			"role":          "admin",
		},
	}
	api, tmpDir := newUserMgmtAPI(t, users)
	return api, tmpDir, pw
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// adminRequest constructs an *http.Request with admin role + user injected
// into the context, the way withAuth would after a successful bearer check.
func adminRequest(method, url, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, url, nil)
	} else {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), RoleContextKey{}, config.UserRoleAdmin)
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
		Username: "admin",
		Role:     config.UserRoleAdmin,
	})
	return r.WithContext(ctx)
}

func nonAdminRequest(method, url, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, url, nil)
	} else {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), RoleContextKey{}, config.UserRoleUser)
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
		Username: "bob",
		Role:     config.UserRoleUser,
	})
	return r.WithContext(ctx)
}

// readDiskUsers reads api.homePath/config.json and returns the users slice
// as a []map[string]any. Caller can then assert on specific user rows.
func readDiskUsers(t *testing.T, api *restAPI) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gw, _ := m["gateway"].(map[string]any)
	if gw == nil {
		return nil
	}
	usersRaw, _ := gw["users"].([]any)
	out := make([]map[string]any, 0, len(usersRaw))
	for _, u := range usersRaw {
		if um, ok := u.(map[string]any); ok {
			out = append(out, um)
		}
	}
	return out
}

// --- HandleUserCreate ---

// TestHandleUserCreate_ReturnsUserAndRoleOnly verifies that POST /users
// returns 201 with {username, role} — and crucially NO "token" field.
// No bearer token may be issued at creation time; the new user must log in
// with their password to obtain a token.
//
// Single-user model (operator directive, 2026-07): the wire contract still
// accepts "role":"user" as a legal request shape (validated, not rejected),
// but the value actually persisted — and returned — is always "admin"
// (rest_users.go HandleUserCreate's persistedRole). This assertion is
// deliberately flipped from "user" to "admin" to match that behavior.
func TestHandleUserCreate_ReturnsUserAndRoleOnly(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"username":"alice","role":"user","password":"alice-secret-123"}`
	w := httptest.NewRecorder()
	api.HandleUserCreate(w, adminRequest(http.MethodPost, "/api/v1/users", body))

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "alice", resp["username"])
	assert.Equal(t, "admin", resp["role"],
		"single-user model: requested role=user is always persisted/returned as admin")
	_, hasToken := resp["token"]
	assert.False(t, hasToken, "response MUST NOT include a token field")
	_, hasTokenHash := resp["token_hash"]
	assert.False(t, hasTokenHash, "response MUST NOT include a token_hash field")
	_, hasPasswordHash := resp["password_hash"]
	assert.False(t, hasPasswordHash, "response MUST NOT include a password_hash field")
}

// TestHandleUserCreate_ThenLoginWithPassword_IssuesToken verifies the
// create-then-login flow: create without token, then login with password
// returns a canonical bearer (omnipus_<64 hex>).
func TestHandleUserCreate_ThenLoginWithPassword_IssuesToken(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)

	createBody := `{"username":"alice","role":"user","password":"alice-secret-123"}`
	w1 := httptest.NewRecorder()
	api.HandleUserCreate(w1, adminRequest(http.MethodPost, "/api/v1/users", createBody))
	require.Equal(t, http.StatusCreated, w1.Code, "create: %s", w1.Body.String())

	// Confirm token_hash is empty on disk post-create.
	users := readDiskUsers(t, api)
	var alice map[string]any
	for _, u := range users {
		if u["username"] == "alice" {
			alice = u
			break
		}
	}
	require.NotNil(t, alice, "alice must be persisted on disk")
	assert.Equal(t, "", alice["token_hash"], "token_hash must be empty at creation time")

	// Now login as alice.
	loginBody := `{"username":"alice","password":"alice-secret-123"}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleLogin(w2, r2)

	require.Equal(t, http.StatusOK, w2.Code, "login: %s", w2.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	tok, ok := resp["token"].(string)
	require.True(t, ok, "login response must have token field")
	assert.Regexp(t, canonicalTokenRE, tok, "bearer must match omnipus_<64 hex> (canonical format)")
}

// TestHandleUserCreate_RejectsInvalidUsername verifies that usernames
// containing whitespace, slashes, leading dashes, or empty strings are
// rejected with 400.
func TestHandleUserCreate_RejectsInvalidUsername(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	// JSON-encode each username to keep the body valid even for odd input.
	cases := []string{"alice bob", "alice/bob", "", "-alice", ".alice", "_alice", "a", "a\n"}
	for _, bad := range cases {
		t.Run("bad="+bad, func(t *testing.T) {
			encUser, _ := json.Marshal(bad)
			body := `{"username":` + string(encUser) + `,"role":"user","password":"somepassword"}`
			w := httptest.NewRecorder()
			api.HandleUserCreate(w, adminRequest(http.MethodPost, "/api/v1/users", body))
			assert.Equal(t, http.StatusBadRequest, w.Code, "username=%q body: %s", bad, w.Body.String())
		})
	}
}

// TestHandleUserCreate_RejectsUppercaseRole verifies case-sensitivity of
// the role field: "ADMIN", "Admin", "User" all 400.
func TestHandleUserCreate_RejectsUppercaseRole(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	for _, r := range []string{"ADMIN", "Admin", "User", "guest", ""} {
		t.Run("role="+r, func(t *testing.T) {
			body := `{"username":"alice","role":"` + r + `","password":"somepassword"}`
			w := httptest.NewRecorder()
			api.HandleUserCreate(w, adminRequest(http.MethodPost, "/api/v1/users", body))
			assert.Equal(t, http.StatusBadRequest, w.Code, "role=%q body: %s", r, w.Body.String())
		})
	}
}

// TestHandleUserCreate_RejectsShortPassword verifies that a 7-character
// password is rejected (>= 8 required).
func TestHandleUserCreate_RejectsShortPassword(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"username":"alice","role":"user","password":"short"}`
	w := httptest.NewRecorder()
	api.HandleUserCreate(w, adminRequest(http.MethodPost, "/api/v1/users", body))
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "8 characters")
}

// TestHandleUserCreate_Duplicate_Returns409 verifies that a second create
// with the same username returns 409 Conflict.
func TestHandleUserCreate_Duplicate_Returns409(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"username":"alice","role":"user","password":"alice-pass-123"}`

	w1 := httptest.NewRecorder()
	api.HandleUserCreate(w1, adminRequest(http.MethodPost, "/api/v1/users", body))
	require.Equal(t, http.StatusCreated, w1.Code, "first create: %s", w1.Body.String())

	w2 := httptest.NewRecorder()
	api.HandleUserCreate(w2, adminRequest(http.MethodPost, "/api/v1/users", body))
	assert.Equal(t, http.StatusConflict, w2.Code, "duplicate create: %s", w2.Body.String())
}

// TestHandleUserCreate_NonAdmin403 used to verify a user-role caller gets
// 403. Single-user model (operator directive, 2026-07): a Gateway.Users
// entry that requests role="user" is normalized to admin by
// config.LoadConfig's load-time self-heal (config.normalizeAdminOnlyRoles)
// before it can ever reach request handling. RequireAdmin's code is
// unchanged (still denies a literal non-admin role) but no authenticated
// caller can carry one anymore. This test is deliberately flipped (not
// deleted) to prove the new outcome: the SAME "user"-configured account
// that used to be denied here now succeeds, via the real config-loading
// choke point rather than a hand-asserted role literal.
func TestHandleUserCreate_NonAdmin403(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)

	resolvedUser := normalizedUserForRole(t, "bob", "user")
	require.Equal(t, config.UserRoleAdmin, resolvedUser.Role,
		"single-user model: config.LoadConfig must normalize role=user to admin")

	body := `{"username":"alice","role":"user","password":"alice-pass-123"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), RoleContextKey{}, resolvedUser.Role)
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, resolvedUser)
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	middleware.RequireAdmin(http.HandlerFunc(api.HandleUserCreate)).ServeHTTP(w, r)
	assert.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
}

// --- HandleUsersList ---

// TestHandleUsersList_NoHashesInResponse verifies that the list response
// NEVER contains password_hash / token_hash strings — even substring
// leakage is a regression.
func TestHandleUsersList_NoHashesInResponse(t *testing.T) {
	pwHash, err := bcrypt.GenerateFromPassword([]byte("admin-pass-1234"), bcrypt.MinCost)
	require.NoError(t, err)
	tokHash, err := bcrypt.GenerateFromPassword([]byte("tok-raw-value"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{
			"username": "admin", "password_hash": string(pwHash),
			"token_hash": string(tokHash), "role": "admin",
		},
		map[string]any{
			"username": "bob", "password_hash": string(pwHash),
			"token_hash": "", "role": "user",
		},
	}
	api, _ := newUserMgmtAPI(t, users)

	w := httptest.NewRecorder()
	api.HandleUsersList(w, adminRequest(http.MethodGet, "/api/v1/users", ""))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	body := w.Body.String()
	assert.NotContains(t, body, "password_hash", "response must not include the password_hash field")
	assert.NotContains(t, body, "token_hash", "response must not include the token_hash field")
	assert.NotContains(t, body, string(pwHash), "response must not leak the bcrypt hash value")
	assert.NotContains(t, body, string(tokHash), "response must not leak the token hash value")

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	require.Len(t, entries, 2)
	for _, e := range entries {
		assert.Contains(t, e, "username")
		assert.Contains(t, e, "role")
		assert.Contains(t, e, "has_password")
		assert.Contains(t, e, "has_active_token")
	}
	// admin entry has both flags true; bob has password true, token false.
	byUser := map[string]map[string]any{}
	for _, e := range entries {
		byUser[e["username"].(string)] = e
	}
	assert.Equal(t, true, byUser["admin"]["has_password"])
	assert.Equal(t, true, byUser["admin"]["has_active_token"])
	assert.Equal(t, true, byUser["bob"]["has_password"])
	assert.Equal(t, false, byUser["bob"]["has_active_token"])
}

// TestHandleUsersList_NonAdmin403 verifies user-role callers receive 403.
func TestHandleUsersList_NonAdmin403(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	w := httptest.NewRecorder()
	middleware.RequireAdmin(http.HandlerFunc(api.HandleUsersList)).
		ServeHTTP(w, nonAdminRequest(http.MethodGet, "/api/v1/users", ""))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- HandleUserDelete ---

// TestHandleUserDelete_LastAdmin409 verifies that deleting the sole admin
// in a deployment returns 409 AND the on-disk config is unchanged.
func TestHandleUserDelete_LastAdmin409(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)

	beforeUsers := readDiskUsers(t, api)
	require.Len(t, beforeUsers, 1, "precondition: exactly one admin")

	w := httptest.NewRecorder()
	r := adminRequest(http.MethodDelete, "/api/v1/users/admin", "")
	api.HandleUserDelete(w, r)

	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "zero administrators")

	afterUsers := readDiskUsers(t, api)
	assert.Equal(t, beforeUsers, afterUsers, "on-disk config must be unchanged after 409")
}

// TestHandleUserDelete_NotFound_Returns404 verifies that deleting a
// non-existent username yields 404.
func TestHandleUserDelete_NotFound_Returns404(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	w := httptest.NewRecorder()
	api.HandleUserDelete(w, adminRequest(http.MethodDelete, "/api/v1/users/ghost", ""))
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// TestHandleUserDelete_HappyPath verifies that deleting a non-last-admin
// removes them from the on-disk users list.
func TestHandleUserDelete_HappyPath(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("admin-pw"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{"username": "admin1", "password_hash": string(hash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "admin2", "password_hash": string(hash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "bob", "password_hash": string(hash), "token_hash": "", "role": "user"},
	}
	api, _ := newUserMgmtAPI(t, users)

	w := httptest.NewRecorder()
	api.HandleUserDelete(w, adminRequest(http.MethodDelete, "/api/v1/users/bob", ""))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	disk := readDiskUsers(t, api)
	assert.Len(t, disk, 2, "bob must be removed from disk")
	for _, u := range disk {
		assert.NotEqual(t, "bob", u["username"])
	}
}

// TestHandleUserDelete_LastAdminGuardInsideWriteLock documents that the
// last-admin guard is evaluated INSIDE the safeUpdateConfigJSON callback
// against the JSON map just read from disk.
//
// A direct TOCTOU test requires hooks the callback does not expose; instead
// we run a happy-path scenario where the guard would fire if the callback
// observed a stale pre-lock snapshot. Starting state: one admin + one
// admin-being-demoted-concurrently. The guard MUST see the freshly-read
// slice and fire correctly.
//
// See rest_users_race_test.go for the actual concurrent race harness.
func TestHandleUserDelete_LastAdminGuardInsideWriteLock(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("admin-pw"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{"username": "admin1", "password_hash": string(hash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "admin2", "password_hash": string(hash), "token_hash": "", "role": "admin"},
	}
	api, _ := newUserMgmtAPI(t, users)

	// Delete admin1 — should succeed (admin2 remains).
	w1 := httptest.NewRecorder()
	api.HandleUserDelete(w1, adminRequest(http.MethodDelete, "/api/v1/users/admin1", ""))
	require.Equal(t, http.StatusOK, w1.Code, "first delete: %s", w1.Body.String())

	// Delete admin2 — MUST fire the guard (last admin).
	w2 := httptest.NewRecorder()
	api.HandleUserDelete(w2, adminRequest(http.MethodDelete, "/api/v1/users/admin2", ""))
	assert.Equal(t, http.StatusConflict, w2.Code, "second delete must hit last-admin guard: %s", w2.Body.String())

	// On-disk config must still contain admin2 (guard blocked the write).
	disk := readDiskUsers(t, api)
	var foundAdmin2 bool
	for _, u := range disk {
		if u["username"] == "admin2" {
			foundAdmin2 = true
			break
		}
	}
	assert.True(t, foundAdmin2, "admin2 must remain on disk — guard must block the write")
}

// TestHandleUserDelete_AuditEntry_OmitsHashes verifies the captured audit
// record's old_value contains {username, role} ONLY — no password_hash
// or token_hash.
func TestHandleUserDelete_AuditEntry_OmitsHashes(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("admin-pw"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{
			"username":      "admin1",
			"password_hash": string(hash),
			"token_hash":    string(hash),
			"role":          "admin",
		},
		map[string]any{"username": "admin2", "password_hash": string(hash), "token_hash": "", "role": "admin"},
	}
	api, tmpDir := newUserMgmtAPI(t, users)

	w := httptest.NewRecorder()
	api.HandleUserDelete(w, adminRequest(http.MethodDelete, "/api/v1/users/admin1", ""))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	rec := findAuditRecord(t, tmpDir, "gateway.users.admin1")
	require.NotNil(t, rec, "audit record must be emitted")

	oldVal, ok := rec["old_value"].(map[string]any)
	require.True(t, ok, "old_value must be a map, got %T", rec["old_value"])
	// Allowed fields: username, role. Disallowed: any *hash field.
	for key := range oldVal {
		assert.NotContains(t, key, "hash", "old_value must not include any *_hash field, got %q", key)
	}
	assert.Equal(t, "admin1", oldVal["username"])
	assert.Equal(t, "admin", oldVal["role"])
}

// --- HandleUserChangeRole ---

// TestHandleUserChangeRole_AdminToUser used to verify the happy path: an
// admin is demoted to user when another admin remains. Single-user model
// (operator directive, 2026-07): a role-change request to "user" is now a
// no-op with respect to actual authority — rest_users.go
// HandleUserChangeRole always persists "admin" regardless of body.Role. This
// test is deliberately flipped (not deleted) to prove the new outcome: the
// PATCH still succeeds (200), but admin2's on-disk role stays "admin"
// instead of becoming "user".
func TestHandleUserChangeRole_AdminToUser(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{"username": "admin1", "password_hash": string(hash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "admin2", "password_hash": string(hash), "token_hash": "", "role": "admin"},
	}
	api, _ := newUserMgmtAPI(t, users)

	body := `{"role":"user"}`
	w := httptest.NewRecorder()
	api.HandleUserChangeRole(w, adminRequest(http.MethodPatch, "/api/v1/users/admin2/role", body))

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin", resp["role"], "single-user model: demotion request is a no-op")
	disk := readDiskUsers(t, api)
	for _, u := range disk {
		if u["username"] == "admin2" {
			assert.Equal(t, "admin", u["role"],
				"single-user model: admin2 must remain admin on disk — demotion is a no-op")
		}
	}
}

// TestHandleUserChangeRole_LastAdminDemotion409 used to verify that the sole
// admin demoting themselves is blocked with 409 and the on-disk role is
// unchanged. Single-user model (operator directive, 2026-07): since a
// role-change request to "user" now always persists "admin" (no actual
// demotion occurs), the ErrLastAdmin/countAdmins guard this test exercised
// is unreachable dead code — countAdmins can never drop to zero as a result
// of this handler. This test is deliberately flipped (not deleted) to prove
// the new outcome: the PATCH now succeeds (200, not 409), and the admin's
// on-disk role — which was never actually going to change — stays "admin".
func TestHandleUserChangeRole_LastAdminDemotion409(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)

	body := `{"role":"user"}`
	w := httptest.NewRecorder()
	api.HandleUserChangeRole(w, adminRequest(http.MethodPatch, "/api/v1/users/admin/role", body))

	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	disk := readDiskUsers(t, api)
	require.Len(t, disk, 1)
	assert.Equal(t, "admin", disk[0]["role"], "sole admin must still be admin on disk")
}

// TestHandleUserChangeRole_NotFound_Returns404 verifies unknown user → 404.
func TestHandleUserChangeRole_NotFound_Returns404(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"role":"user"}`
	w := httptest.NewRecorder()
	api.HandleUserChangeRole(w, adminRequest(http.MethodPatch, "/api/v1/users/ghost/role", body))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleUserChangeRole_RejectsInvalidRole verifies case-sensitivity.
func TestHandleUserChangeRole_RejectsInvalidRole(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"role":"ADMIN"}`
	w := httptest.NewRecorder()
	api.HandleUserChangeRole(w, adminRequest(http.MethodPatch, "/api/v1/users/admin/role", body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleUserDelete_NonAdmin403 verifies that a user-role (non-admin) caller
// receives 403 when attempting DELETE /api/v1/users/{username}.
//
// The matrix test TestAdminRoutes_AdminOnly already covers this route through
// the inner middleware chain. This per-handler test makes the admin gate visible
// to anyone reading rest_users_test.go and exercises the same check at the
// handler level, providing defense-in-depth against a middleware rewiring.
//
// Traces to: temporal-puzzling-melody.md Wave 1C — per-handler NonAdmin403
// coverage gap identified by test-analyzer #3.
func TestHandleUserDelete_NonAdmin403(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	w := httptest.NewRecorder()
	middleware.RequireAdmin(
		http.HandlerFunc(api.HandleUserDelete),
	).ServeHTTP(w, nonAdminRequest(http.MethodDelete, "/api/v1/users/sometarget", ""))
	assert.Equal(t, http.StatusForbidden, w.Code,
		"user-role caller must receive 403 on DELETE /api/v1/users/{username}")
}

// TestHandleUserChangeRole_NonAdmin403 used to verify that a user-role
// (non-admin) caller receives 403 when attempting PATCH
// /api/v1/users/{username}/role. Single-user model (operator directive,
// 2026-07): a Gateway.Users entry that requests role="user" is normalized to
// admin by config.LoadConfig's load-time self-heal
// (config.normalizeAdminOnlyRoles) before it can ever reach request
// handling. RequireAdmin's code is unchanged (still denies a literal
// non-admin role) but no authenticated caller can carry one anymore. This
// test is deliberately flipped (not deleted) to prove the new outcome: the
// SAME "user"-configured account that used to be denied 403 at the
// RequireAdmin gate now passes through to the handler — which returns 404
// because "sometarget" is not a seeded user, proving the request reached
// real handler logic rather than being denied at the door.
//
// Traces to: temporal-puzzling-melody.md Wave 1C — per-handler NonAdmin403
// coverage gap identified by test-analyzer #3.
func TestHandleUserChangeRole_NonAdmin403(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)

	resolvedUser := normalizedUserForRole(t, "bob", "user")
	require.Equal(t, config.UserRoleAdmin, resolvedUser.Role,
		"single-user model: config.LoadConfig must normalize role=user to admin")

	body := `{"role":"admin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/users/sometarget/role", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), RoleContextKey{}, resolvedUser.Role)
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, resolvedUser)
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	middleware.RequireAdmin(
		http.HandlerFunc(api.HandleUserChangeRole),
	).ServeHTTP(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code,
		"user-role caller now passes RequireAdmin; handler 404s because sometarget doesn't exist")
}

// --- HandleUserResetPassword ---

// TestHandleUserResetPassword_ZeroesTokenHash verifies that after PUT the
// on-disk config has token_hash = "" for the target user.
func TestHandleUserResetPassword_ZeroesTokenHash(t *testing.T) {
	pwHash, err := bcrypt.GenerateFromPassword([]byte("old-password-xx"), bcrypt.MinCost)
	require.NoError(t, err)
	tokHash, err := bcrypt.GenerateFromPassword([]byte("issued-token"), bcrypt.MinCost)
	require.NoError(t, err)

	users := []any{
		map[string]any{
			"username":      "admin",
			"password_hash": string(pwHash),
			"token_hash":    string(tokHash),
			"role":          "admin",
		},
		map[string]any{
			"username":      "alice",
			"password_hash": string(pwHash),
			"token_hash":    string(tokHash),
			"role":          "user",
		},
	}
	api, _ := newUserMgmtAPI(t, users)

	body := `{"password":"new-password-yy"}`
	w := httptest.NewRecorder()
	api.HandleUserResetPassword(w, adminRequest(http.MethodPut, "/api/v1/users/alice/password", body))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	disk := readDiskUsers(t, api)
	var alice map[string]any
	for _, u := range disk {
		if u["username"] == "alice" {
			alice = u
			break
		}
	}
	require.NotNil(t, alice)
	assert.Equal(t, "", alice["token_hash"], "target user token_hash must be zeroed after admin reset")
	assert.NotEqual(t, string(pwHash), alice["password_hash"], "password_hash must change")
	assert.NotEmpty(t, alice["password_hash"], "password_hash must be non-empty")
}

// TestHandleUserResetPassword_PriorTokenReturns401 verifies the full
// revocation semantics: capture a valid bearer for the target user, admin
// resets the password, and the prior bearer then 401s against any
// withAuth-gated endpoint (we use HandleValidateToken which sits behind
// withAuth in production).
func TestHandleUserResetPassword_PriorTokenReturns401(t *testing.T) {
	// Seed admin + alice; login as alice to capture her bearer.
	pwHash, err := bcrypt.GenerateFromPassword([]byte("admin-pw-xxx"), bcrypt.MinCost)
	require.NoError(t, err)
	alicePwHash, err := bcrypt.GenerateFromPassword([]byte("alice-pw-yyy"), bcrypt.MinCost)
	require.NoError(t, err)

	users := []any{
		map[string]any{"username": "admin", "password_hash": string(pwHash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "alice", "password_hash": string(alicePwHash), "token_hash": "", "role": "user"},
	}
	api, _ := newUserMgmtAPI(t, users)

	// Log alice in to obtain a canonical bearer.
	loginBody := `{"username":"alice","password":"alice-pw-yyy"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code, "login: %s", loginW.Body.String())
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	aliceToken, _ := loginResp["token"].(string)
	require.NotEmpty(t, aliceToken)

	// Verify the token works BEFORE the reset: issue a request through
	// withAuth to HandleValidateToken.
	preW := httptest.NewRecorder()
	preR := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	preR.Header.Set("Authorization", "Bearer "+aliceToken)
	api.withAuth(api.HandleValidateToken)(preW, preR)
	require.Equal(t, http.StatusOK, preW.Code, "pre-reset validate: %s", preW.Body.String())

	// Admin resets alice's password — same transaction zeros token_hash.
	resetBody := `{"password":"new-alice-pw-12345"}`
	resetW := httptest.NewRecorder()
	api.HandleUserResetPassword(resetW, adminRequest(http.MethodPut, "/api/v1/users/alice/password", resetBody))
	require.Equal(t, http.StatusOK, resetW.Code, "reset: %s", resetW.Body.String())

	// Alice's prior bearer must now 401.
	postW := httptest.NewRecorder()
	postR := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	postR.Header.Set("Authorization", "Bearer "+aliceToken)
	api.withAuth(api.HandleValidateToken)(postW, postR)
	assert.Equal(t, http.StatusUnauthorized, postW.Code,
		"alice's prior bearer must 401 after admin password reset (body: %s)", postW.Body.String())
}

// TestHandleUserResetPassword_CanLoginWithNewPassword verifies that after
// the admin reset the target user can login with their NEW password and
// receive a fresh bearer.
func TestHandleUserResetPassword_CanLoginWithNewPassword(t *testing.T) {
	pwHash, err := bcrypt.GenerateFromPassword([]byte("old-pw-xxxxx"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{"username": "admin", "password_hash": string(pwHash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "alice", "password_hash": string(pwHash), "token_hash": "", "role": "user"},
	}
	api, _ := newUserMgmtAPI(t, users)

	resetBody := `{"password":"new-fresh-pw-88"}`
	resetW := httptest.NewRecorder()
	api.HandleUserResetPassword(resetW, adminRequest(http.MethodPut, "/api/v1/users/alice/password", resetBody))
	require.Equal(t, http.StatusOK, resetW.Code, "reset: %s", resetW.Body.String())

	loginW := httptest.NewRecorder()
	loginR := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":"new-fresh-pw-88"}`))
	loginR.Header.Set("Content-Type", "application/json")
	api.HandleLogin(loginW, loginR)

	require.Equal(t, http.StatusOK, loginW.Code, "login with new password: %s", loginW.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &resp))
	assert.Regexp(t, canonicalTokenRE, resp["token"], "login must issue a canonical bearer")
}

// TestHandleUserResetPassword_NonAdmin403 verifies user-role → 403.
func TestHandleUserResetPassword_NonAdmin403(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"password":"new-password-22"}`
	w := httptest.NewRecorder()
	middleware.RequireAdmin(http.HandlerFunc(api.HandleUserResetPassword)).
		ServeHTTP(w, nonAdminRequest(http.MethodPut, "/api/v1/users/admin/password", body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestHandleUserResetPassword_NotFound_Returns404 verifies unknown user → 404.
func TestHandleUserResetPassword_NotFound_Returns404(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"password":"new-password-22"}`
	w := httptest.NewRecorder()
	api.HandleUserResetPassword(w, adminRequest(http.MethodPut, "/api/v1/users/ghost/password", body))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleUserResetPassword_RejectsShortPassword verifies ≥ 8 requirement.
func TestHandleUserResetPassword_RejectsShortPassword(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	body := `{"password":"short"}`
	w := httptest.NewRecorder()
	api.HandleUserResetPassword(w, adminRequest(http.MethodPut, "/api/v1/users/admin/password", body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleUserResetPassword_AuditRedacted verifies that the captured
// audit record's new_value has the password replaced with the
// "***redacted***" sentinel from the audit redactor.
func TestHandleUserResetPassword_AuditRedacted(t *testing.T) {
	pwHash, err := bcrypt.GenerateFromPassword([]byte("admin-pw"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{"username": "admin", "password_hash": string(pwHash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "alice", "password_hash": string(pwHash), "token_hash": "", "role": "user"},
	}
	api, tmpDir := newUserMgmtAPI(t, users)

	body := `{"password":"new-secret-reset"}`
	w := httptest.NewRecorder()
	api.HandleUserResetPassword(w, adminRequest(http.MethodPut, "/api/v1/users/alice/password", body))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	rec := findAuditRecord(t, tmpDir, "gateway.users.alice.password")
	require.NotNil(t, rec, "audit record must be emitted")

	newVal, ok := rec["new_value"].(map[string]any)
	require.True(t, ok, "new_value must be a map, got %T", rec["new_value"])
	assert.Equal(t, "***redacted***", newVal["password"],
		"password field in new_value must be redacted to ***redacted***")

	// The raw password must NOT appear anywhere in the serialized record.
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "new-secret-reset",
		"raw password must not appear anywhere in the audit record")
}

// --- method-not-allowed sanity checks ---

func TestHandleUsersEndpoints_MethodNotAllowed(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)
	type tc struct {
		name    string
		method  string
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}
	cases := []tc{
		{"list-put", http.MethodPut, "/api/v1/users", api.HandleUsersList},
		{"create-get", http.MethodGet, "/api/v1/users", api.HandleUserCreate},
		{"delete-get", http.MethodGet, "/api/v1/users/admin", api.HandleUserDelete},
		{"role-get", http.MethodGet, "/api/v1/users/admin/role", api.HandleUserChangeRole},
		{"password-get", http.MethodGet, "/api/v1/users/admin/password", api.HandleUserResetPassword},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c.handler(w, adminRequest(c.method, c.path, ""))
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// --- Route dispatch regression: /api/v1/users method dispatcher ---

// TestRoute_POST_Users_Reaches_HandleUserCreate verifies the three-way method
// dispatch registered at /api/v1/users. Without this test a future collapse of
// the dispatcher back to a single handler (GET only) would silently regress the
// Route dispatch test for POST /api/v1/users (found POST returning 405 in E2E).
//
// The test calls the handlers directly with the correct method — the same
// method guard that the adminWrap dispatcher invokes — which exercises the
// exact code path that was broken: HandleUserCreate was implemented but
// unreachable because the route only wired HandleUsersList.
func TestRoute_POST_Users_Reaches_HandleUserCreate(t *testing.T) {
	api, _, _ := newUserMgmtAPIWithAdmin(t)

	t.Run("GET returns 200 list", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.HandleUsersList(w, adminRequest(http.MethodGet, "/api/v1/users", ""))
		assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var entries []map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
		assert.NotEmpty(t, entries, "list must return at least the seeded admin")
	})

	t.Run("POST returns 201 create", func(t *testing.T) {
		body := `{"username":"newuser","role":"user","password":"newuser-pw-99"}`
		w := httptest.NewRecorder()
		api.HandleUserCreate(w, adminRequest(http.MethodPost, "/api/v1/users", body))
		assert.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "newuser", resp["username"])
		// Single-user model: requested role=user is always persisted/returned
		// as admin (see TestHandleUserCreate_ReturnsUserAndRoleOnly).
		assert.Equal(t, "admin", resp["role"])
	})

	t.Run("PUT at collection path returns 405", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.HandleUsersList(w, adminRequest(http.MethodPut, "/api/v1/users", ""))
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "body: %s", w.Body.String())
	})
}

// --- helper: scan audit JSONL for a resource match ---

func findAuditRecord(t *testing.T, homeDir, resource string) map[string]any {
	t.Helper()
	systemDir := filepath.Join(homeDir, "system")
	entries, err := os.ReadDir(systemDir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(systemDir, e.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		var match map[string]any
		for sc.Scan() {
			var rec map[string]any
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				continue
			}
			if rec["resource"] == resource {
				match = rec
			}
		}
		f.Close()
		if match != nil {
			return match
		}
	}
	return nil
}

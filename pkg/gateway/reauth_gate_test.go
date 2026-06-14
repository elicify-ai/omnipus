//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// reauthGateAdminUser is the username re-auth-gated tests authenticate as. It is
// distinct from the bcrypt-password user in rest_integrations_auth_test.go so
// these helpers can mint a consent token directly via the in-memory store
// without round-tripping a password (mint is keyed purely by username string).
const reauthGateAdminUser = "reauth-admin"

// withReAuthAdmin attaches BOTH (a) an admin *config.UserConfig under
// UserContextKey{} and the admin RoleContextKey{} role, and (b) a freshly
// minted, valid single-use re-auth consent token under the X-Reauth-Token
// header. Tests that exercise a re-auth-gated PUT handler directly (bypassing
// withAuth/withReAuth middleware) use this so the handler's requireReAuth gate
// passes. The token is minted straight from the API's in-memory reauth store —
// no password verification is exercised here; that path is covered by the
// dedicated re-auth handler tests.
func withReAuthAdmin(t *testing.T, api *restAPI, r *http.Request) *http.Request {
	t.Helper()
	token, err := api.reauthStoreOrInit().mint(reauthGateAdminUser)
	require.NoError(t, err, "minting a re-auth consent token must not fail")
	r.Header.Set(reAuthHeader, token)
	user := &config.UserConfig{Username: reauthGateAdminUser, Role: config.UserRoleAdmin}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)
	return r.WithContext(ctx)
}

// withReAuthAdminNoToken attaches the admin user + role to the request context
// but DELIBERATELY omits the re-auth consent token — used to prove the gate
// rejects a sensitive mutation that lacks the token.
func withReAuthAdminNoToken(r *http.Request) *http.Request {
	user := &config.UserConfig{Username: reauthGateAdminUser, Role: config.UserRoleAdmin}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)
	return r.WithContext(ctx)
}

// --- Performance (Spec-3 FR-6.6 / Spec-6 FR-12.2) ---

// TestPerformancePUT_RequiresReAuth proves the max-parallel-agents PUT rejects a
// request that carries the admin user but no re-auth consent token (403).
func TestPerformancePUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":4}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"performance PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestPerformancePUT_WithReAuth_Succeeds proves the same PUT succeeds once a
// valid consent token is supplied.
func TestPerformancePUT_WithReAuth_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":4}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 4, resp["max_parallel_agents"])
}

// TestPerformancePUT_Unauthenticated_Rejected proves the handler rejects a PUT
// with no authenticated user in context (401), independent of the token gate.
func TestPerformancePUT_Unauthenticated_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":4}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Sandbox config (Spec-6 FR-12.2) ---

// TestSandboxConfigPUT_RequiresReAuth proves the sandbox-config PUT rejects a
// request that carries the admin user but no re-auth consent token (403).
func TestSandboxConfigPUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"mode":"permissive"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleSandboxConfig(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"sandbox-config PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestSandboxConfigPUT_WithReAuth_Succeeds proves the same PUT succeeds once a
// valid consent token is supplied.
func TestSandboxConfigPUT_WithReAuth_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"mode":"permissive"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleSandboxConfig(w, r)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())
}

// --- Global tool policies (Spec-3 FR-3.3 / Spec-6 FR-12.2) ---

// TestToolPoliciesPUT_RequiresReAuth proves the global tool-policy PUT rejects a
// request that carries the admin role/user but no re-auth consent token (403).
func TestToolPoliciesPUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`{"default_policy":"ask","policies":{"exec":"deny"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"tool-policies PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestToolPoliciesPUT_WithReAuth_Succeeds proves the same PUT succeeds once a
// valid consent token is supplied.
func TestToolPoliciesPUT_WithReAuth_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`{"default_policy":"ask","policies":{"exec":"deny"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ask", resp["default_policy"])
}

// --- Provider / model API-key PUT (Spec-6 FR-12.2 / FR-6.6) ---
//
// HandleProviders PUT mutates a model/provider API key — the highest-blast-radius
// credential write reachable from Settings. The happy path (token supplied) is
// covered elsewhere; this is the missing NEGATIVE proof that the gate REJECTS a
// post-onboarding provider edit lacking the consent token. The 403 fires before
// the request body is decoded (rest.go: gate precedes decodeAndValidate), so an
// empty body still exercises the gate. A future removal of requireReAuth on this
// route would flip this test red.

// TestProvidersPUT_RequiresReAuth proves a provider/model API-key PUT that carries
// the admin user but no re-auth consent token is rejected (403). onboarding is
// incomplete in this fixture, but the gate keys on "a user is in context", which
// withReAuthAdminNoToken supplies — so the token requirement still applies.
func TestProvidersPUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/openai",
		strings.NewReader(`{"api_key":"sk-test","model":"gpt-4o"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleProviders(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"provider-key PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// --- Agent tool-grant PUT (Spec-3 FR-3.3 / Spec-6 FR-12.2) ---
//
// updateAgentTools changes which tools an agent may call — a capability grant.
// The re-auth gate sits AFTER the agent-exists and not-locked checks but BEFORE
// the request body is decoded, so the negative test needs a real, non-locked
// agent in config; an empty/minimal body is fine because the 403 short-circuits
// before decode. The happy path is covered elsewhere; this is the missing
// NEGATIVE proof that the gate rejects an ungated capability grant (403).

// TestAgentToolsPUT_RequiresReAuth proves an agent tool-grant PUT that carries the
// admin user but no re-auth consent token is rejected (403). It reuses
// newTestRestAPIWithAgent (rest_board_test.go), which seeds a single non-locked
// custom agent, so updateAgentTools reaches its re-auth gate (found + unlocked).
func TestAgentToolsPUT_RequiresReAuth(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools",
		strings.NewReader(`{"builtin":{"default_policy":"allow"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"agent tool-grant PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// --- Re-auth token TTL / expiry (Spec-6 FR-12.2) ---
//
// The store mints tokens with a 5-minute TTL (reAuthTokenTTL) and prunes/rejects
// expired ones. Existing tests cover single-use consumption and the expires_in
// field but never the TTL sweep. These drive expiry deterministically by minting
// an entry whose expiresAt is already in the past — exercising the production
// expiry branch in consume and the pruneLocked sweep — WITHOUT advancing any
// clock or touching production behavior.

// TestReAuthToken_Expired_RejectedByConsume proves an expired token is rejected by
// consume (the path requireReAuth takes), even for the correct username, and is
// removed (single-use) so it cannot be retried.
func TestReAuthToken_Expired_RejectedByConsume(t *testing.T) {
	store := newReAuthStore()
	const user = "reauth-admin"

	// Mint a token directly into the store with an already-elapsed expiry. This is
	// the same shape mint() produces; we backdate expiresAt to drive the TTL branch
	// deterministically rather than sleeping for reAuthTokenTTL.
	const expired = "reauth_expired_deadbeef"
	store.mu.Lock()
	store.tokens[expired] = reauthEntry{username: user, expiresAt: time.Now().Add(-time.Second)}
	store.mu.Unlock()

	assert.False(t, store.consume(expired, user),
		"an expired token must be rejected by consume even for the right user")

	// Single-use: consume must also have deleted it, so a retry is impossible.
	store.mu.Lock()
	_, stillPresent := store.tokens[expired]
	store.mu.Unlock()
	assert.False(t, stillPresent, "consume must delete a token on lookup (single-use), even when expired")
}

// TestReAuthToken_Expired_PrunedOnMint proves the pruneLocked sweep (invoked on
// every mint) evicts an expired entry, so the store does not accumulate dead
// tokens. A fresh, unexpired token minted afterwards still consumes successfully.
func TestReAuthToken_Expired_PrunedOnMint(t *testing.T) {
	store := newReAuthStore()
	const user = "reauth-admin"

	const stale = "reauth_stale_cafebabe"
	store.mu.Lock()
	store.tokens[stale] = reauthEntry{username: user, expiresAt: time.Now().Add(-time.Minute)}
	store.mu.Unlock()

	// mint runs pruneLocked, which must drop the stale entry.
	fresh, err := store.mint(user)
	require.NoError(t, err)

	store.mu.Lock()
	_, stalePresent := store.tokens[stale]
	store.mu.Unlock()
	assert.False(t, stalePresent, "pruneLocked (run on mint) must evict the expired entry")

	// The freshly minted, unexpired token still works — expiry pruning did not
	// damage live tokens.
	assert.True(t, store.consume(fresh, user), "a freshly minted unexpired token must still consume")
}

// TestReAuthToken_Fresh_NotExpired is a positive control: a token minted via the
// real mint() path (full reAuthTokenTTL) is NOT treated as expired and consumes
// successfully, proving the expiry tests above fail for the right reason (elapsed
// TTL) rather than a blanket reject.
func TestReAuthToken_Fresh_NotExpired(t *testing.T) {
	store := newReAuthStore()
	const user = "reauth-admin"
	token, err := store.mint(user)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	// The TTL is 5 minutes; the entry must be live immediately after minting.
	assert.True(t, store.consume(token, user), "a token within its TTL must consume successfully")
}

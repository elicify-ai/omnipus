// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// reauthGateAdminUser is the username re-auth-gated tests authenticate as. It is
// distinct from the bcrypt-password user in rest_integrations_auth_test.go so
// these helpers can mint a consent token directly via the in-memory store
// without round-tripping a password (mint is keyed purely by username string).
const reauthGateAdminUser = "reauth-admin"

// withReAuthAdmin attaches BOTH (a) an authenticated *config.UserConfig
// under UserContextKey{}, and (b) a freshly minted, valid single-use
// re-auth consent token under the X-Reauth-Token header. Tests that
// exercise a re-auth-gated PUT handler directly (bypassing
// withAuth/withReAuth middleware) use this so the handler's requireReAuth
// gate passes. The token is minted straight from the API's in-memory reauth
// store — no password verification is exercised here; that path is covered
// by the dedicated re-auth handler tests.
//
// Single-user model (operator directive, 2026-07): there is no admin role
// left to inject — requireReAuth and every handler it gates only read the
// caller's identity (UserContextKey / user.Username), never a role.
func withReAuthAdmin(t *testing.T, api *restAPI, r *http.Request) *http.Request {
	t.Helper()
	token, err := api.reauthStoreOrInit().mint(reauthGateAdminUser)
	require.NoError(t, err, "minting a re-auth consent token must not fail")
	r.Header.Set(reAuthHeader, token)
	user := &config.UserConfig{Username: reauthGateAdminUser}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
	return r.WithContext(ctx)
}

// withReAuthAdminNoToken attaches the authenticated user to the request
// context but DELIBERATELY omits the re-auth consent token — used to prove
// the gate rejects a sensitive mutation that lacks the token.
func withReAuthAdminNoToken(r *http.Request) *http.Request {
	user := &config.UserConfig{Username: reauthGateAdminUser}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
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

// --- Global tool policies (UAT: step-up re-auth REMOVED) ---
//
// The step-up re-auth gate was intentionally removed from the global tool-policy
// PUT per UAT feedback (operator found re-typing the password to change a tool
// permission to be unnecessary friction). Authorization is unchanged: the PUT
// still requires an authenticated caller (withAuth). These tests now PROVE the
// gate is gone — a valid authenticated session with NO X-Reauth-Token succeeds —
// while the re-auth gate on OTHER sensitive routes (sandbox/providers/credentials/...)
// keeps its dedicated tests above and below.

// TestToolPoliciesPUT_NoReAuthToken_Succeeds proves the global tool-policy PUT now
// succeeds with a valid admin session and NO re-auth consent token — the step-up
// gate was removed per UAT. This would fail (403) if the gate were restored.
func TestToolPoliciesPUT_NoReAuthToken_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// There is no default_policy field on the wire any more (CLAUDE.md hard
	// constraint 6) — this body used to send one (and assert it echoed back)
	// before that field was removed; a bare "policies" map is the current
	// contract. newTestRestAPIWithHome seeds an empty agent list, so
	// config.ValidateToolPolicyCoverage has nothing to check regardless of
	// what this map contains.
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`{"policies":{"bash":"ask"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r) // admin user/role, but deliberately NO token
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"tool-policies PUT must succeed without a re-auth token (gate removed per UAT); body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	policies, _ := resp["policies"].(map[string]any)
	assert.Equal(t, "ask", policies["bash"])
}

// TestToolPoliciesPUT_WithReAuthToken_StillSucceeds proves a stale/legacy client
// that still sends an X-Reauth-Token is not penalized — the PUT succeeds whether
// or not the token is present (the gate no longer consumes it).
func TestToolPoliciesPUT_WithReAuthToken_StillSucceeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// See TestToolPoliciesPUT_NoReAuthToken_Succeeds — no default_policy field
	// on the wire any more.
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`{"policies":{"bash":"ask"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)
	require.Equal(t, http.StatusOK, w.Code, "tool-policies PUT must succeed; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	policies, _ := resp["policies"].(map[string]any)
	assert.Equal(t, "ask", policies["bash"])
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

// --- Agent tool-grant PUT (UAT: step-up re-auth REMOVED) ---
//
// updateAgentTools changes which tools an agent may call — a capability grant.
// The step-up re-auth gate was intentionally removed from this PUT per UAT
// feedback (operator found re-typing the password to change a tool permission to
// be unnecessary friction). Authorization is unchanged: the handler still runs
// behind withAuth (a valid session is required) and rejects locked/core agents.
// This test now PROVES the gate is gone — a valid admin session with NO
// X-Reauth-Token succeeds.

// TestAgentToolsPUT_NoReAuthToken_Succeeds proves an agent tool-grant PUT now
// succeeds with a valid admin session and NO re-auth consent token — the step-up
// gate was removed per UAT. It reuses newTestRestAPIWithAgent (which seeds a
// single non-locked custom agent) and additionally writes that agent into the
// on-disk config.json so updateAgentTools reaches AND completes its persist path
// (the original fixture's minimal config.json has an empty agents.list, which
// only mattered before because the re-auth 403 short-circuited ahead of persist).
// This would fail (403) if the gate were restored.
func TestAgentToolsPUT_NoReAuthToken_Succeeds(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)

	// ADR-054: agents are per-entity records under entities/agents/<id>.json,
	// not config.json's agents.list. updateAgentTools's persist step now
	// writes through agentstore.Store.Update (rest.go's coverage-guard persist
	// closure), which does a read-modify-write of a REAL entity record — it
	// 500s with "not found in agent store" if none exists. newTestRestAPIWithAgent
	// only seeds the in-memory cfg.Agents.List (sufficient for the handler's
	// initial 404/locked/external-subagent lookup, which still reads
	// a.agentLoop.GetConfig().Agents.List directly) — it does NOT create an
	// entity record. The pre-ADR-054 fixture shape (a raw config.json splice
	// with an agents.list entry) no longer has any effect on the persist path
	// at all, since config.LoadConfig strips agents.list on every load and
	// the persist step never reads config.json's raw map for this agent in
	// the first place. Seed the real entity record instead.
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Test Agent",
		Type: config.AgentTypeCustom,
	}))

	// There is no default_policy field on the wire any more (CLAUDE.md hard
	// constraint 6) — this body used to send one (which decodes to a no-op
	// now, leaving builtinPolicies empty). updateAgentTools fully replaces
	// the agent's builtin tools_cfg on persist, so a complete, explicit
	// policies map is required for config.ValidateToolPolicyCoverage to pass
	// (this fixture has no global sandbox.tool_policies floor either).
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
	r = withReAuthAdminNoToken(r) // admin user/role, but deliberately NO token
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code,
		"agent tool-grant PUT must succeed without a re-auth token (gate removed per UAT); body=%s", w.Body.String())

	// Persistence check (anti-shortcut): a 200 alone does not prove the write
	// landed — read the entity record back and confirm the policies actually
	// changed on disk, not just that the HTTP call was accepted.
	updated, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, updated.Tools, "entity record must have a Tools block after the PUT")
	assert.Equal(t, config.ToolPolicyAllow, updated.Tools.Builtin.Policies["bash"],
		"tool policy sent in the request must be persisted to the entity record")
	assert.Equal(t, config.ToolPolicyAllow, updated.Tools.Builtin.Policies["read_file"],
		"tool policy sent in the request must be persisted to the entity record")
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

// --- Credential vault (Spec-6 FR-12.2 / ADR-022) ---
//
// setCredential and deleteCredential mutate the encrypted credential vault —
// the highest-blast-radius excluded mutation in ADR-021's scope (they store API
// keys and channel tokens). ADR-022 expands ADR-021's v0.2 deferral to v0.1.0,
// gating both with requireReAuth. These mirror the existing 6-gate pattern:
// negative (no token → 403) and positive (valid token → success).

// newCredVaultReAuthTestAPI builds a restAPI whose credential store is present
// AND unlocked, so the positive path can actually write/delete a credential.
// Without an unlocked store the gate would still fire first (negative path),
// but the positive path needs Set/Delete to succeed to prove the gate passed.
func newCredVaultReAuthTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	if err := credentials.Unlock(credStore); err != nil {
		t.Fatalf("unlock credential store: %v", err)
	}
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
		credStore:     credStore,
	}
}

// TestSetCredential_RequiresReAuth proves a credential-vault write (POST
// /api/v1/credentials) carrying the admin user but no re-auth consent token is
// rejected (403). The gate fires before the request body is decoded, so an empty
// body still exercises the gate.
func TestSetCredential_RequiresReAuth(t *testing.T) {
	api := newCredVaultReAuthTestAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/credentials",
		strings.NewReader(`{"key":"TEST_KEY","value":"hunter2"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"credential POST without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestSetCredential_WithReAuth_Succeeds proves the same POST succeeds once a
// valid consent token is supplied — the credential is written to the encrypted
// store and 201 is returned.
func TestSetCredential_WithReAuth_Succeeds(t *testing.T) {
	api := newCredVaultReAuthTestAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/credentials",
		strings.NewReader(`{"key":"TEST_KEY","value":"hunter2"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	require.Equal(t, http.StatusCreated, w.Code,
		"valid re-auth must allow the credential write; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "TEST_KEY", resp["key"])

	// Confirm the secret actually landed in the store (and is retrievable).
	val, err := api.credStore.Get("TEST_KEY")
	require.NoError(t, err)
	assert.Equal(t, "hunter2", val)
}

// TestDeleteCredential_RequiresReAuth proves a credential-vault delete (DELETE
// /api/v1/credentials/{key}) carrying the admin user but no re-auth consent
// token is rejected (403). Deleting a stored secret mid-session can revoke a
// channel or provider, so it is gated just like the write.
func TestDeleteCredential_RequiresReAuth(t *testing.T) {
	api := newCredVaultReAuthTestAPI(t)
	// Seed a credential so the delete would otherwise succeed — proving the 403
	// is from the gate, not a missing-key 404.
	require.NoError(t, api.credStore.Set("DOOMED_KEY", "secret"))

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/DOOMED_KEY", nil)
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"credential DELETE without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")

	// The gate must have fired BEFORE the delete — the seeded key still exists.
	keys, err := api.credStore.List()
	require.NoError(t, err)
	assert.Contains(t, keys, "DOOMED_KEY", "the gate must not have deleted the credential")
}

// TestDeleteCredential_WithReAuth_Succeeds proves the same DELETE succeeds once
// a valid consent token is supplied — the credential is removed and 200 returned.
func TestDeleteCredential_WithReAuth_Succeeds(t *testing.T) {
	api := newCredVaultReAuthTestAPI(t)
	require.NoError(t, api.credStore.Set("DOOMED_KEY", "secret"))

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/DOOMED_KEY", nil)
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"valid re-auth must allow the credential delete; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "removed", resp["status"])

	// Confirm the secret is actually gone from the store.
	keys, err := api.credStore.List()
	require.NoError(t, err)
	assert.NotContains(t, keys, "DOOMED_KEY", "the credential must have been deleted")
}

// TestRotateCredentials_RequiresReAuth (G5) proves POST /api/v1/credentials/rotate
// carrying the admin user but no re-auth consent token is rejected (403). Rotation
// re-encrypts the whole vault, so it is gated like set/delete.
func TestRotateCredentials_RequiresReAuth(t *testing.T) {
	api := newCredVaultReAuthTestAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/rotate",
		strings.NewReader(`{"new_passphrase":"new-pass"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"rotate without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestRotateCredentials_WithReAuth_Succeeds (G5) proves rotation succeeds with a
// valid consent token and that existing secrets remain retrievable afterward
// (re-encrypted under the new key, not lost).
func TestRotateCredentials_WithReAuth_Succeeds(t *testing.T) {
	api := newCredVaultReAuthTestAPI(t)
	require.NoError(t, api.credStore.Set("KEEP_KEY", "keep-me"))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/rotate",
		strings.NewReader(`{"new_passphrase":"a-brand-new-passphrase"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"valid re-auth must allow rotation; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "rotated", resp["status"])

	// The pre-existing secret must survive rotation (re-encrypted, not dropped).
	val, err := api.credStore.Get("KEEP_KEY")
	require.NoError(t, err)
	assert.Equal(t, "keep-me", val)
}

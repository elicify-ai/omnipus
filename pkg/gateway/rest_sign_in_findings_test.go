// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_sign_in_findings_test.go covers three code-review findings against
// rest_sign_in.go:
//
//   - O6:  provider.signed_in / provider.signed_out audit entries must carry
//     the authenticated actor (User) and the caller's source_ip, matching
//     the pattern already used by auditCopilotProbe (rest_signin_copilot.go).
//   - O12: interval_seconds must never leave any producer above the
//     contract's declared maximum of 30
//     (contracts/components/schemas/SignInStartResponseDeviceCode.yaml,
//     SignInPollResponse.yaml).
//   - O18: importing a non-JWT codex-cli credential must not leave
//     ExpiresAt zero — it must fall back to ReadCodexCliCredentials' own
//     mtime+1h estimate.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// --- shared test helpers ---------------------------------------------------

// doJSONAs is doJSON (rest_sign_in_test.go) plus an authenticated actor and
// an explicit RemoteAddr, so O6's audit-actor/source-ip assertions have a
// concrete, non-empty value to check against.
func doJSONAs(t *testing.T, api *restAPI, method, path, remoteAddr, username string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	r.RemoteAddr = remoteAddr
	cfg := api.agentLoop.GetConfig()
	ctx := context.WithValue(r.Context(), configContextKey{}, cfg)
	ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: username})
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, r))
	return w
}

// --- O6: audit actor + source_ip -------------------------------------------
//
// attachTestAuditor / readAuditEntries(t, dir, event) are shared helpers
// from rest_authexpo_fix_test.go.

// TestSignInPoll_SignedInAudit_RecordsActorAndSourceIP proves
// handleProviderSignInPoll's "provider.signed_in" entry carries both the
// authenticated actor and the caller's source_ip, matching
// auditCopilotProbe's pattern (rest_signin_copilot.go).
func TestSignInPoll_SignedInAudit_RecordsActorAndSourceIP(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	auditDir := attachTestAuditor(t, api)

	state := "signed_in"
	server := httptest.NewServer(deviceCodeVendorMux(t, &state))
	defer server.Close()
	withDeviceCodeVendor(t, server)

	startW := doJSONAs(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in",
		"198.51.100.9:4242", "alice", nil)
	require.Equal(t, http.StatusOK, startW.Code, startW.Body.String())
	var startResp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(startW.Body.Bytes(), &startResp))
	deviceCode, err := startResp.AsSignInStartResponseDeviceCode()
	require.NoError(t, err)

	pollBody := gen.SignInPollRequest{DeviceAuthId: deviceCode.DeviceAuthId}
	pollW := doJSONAs(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll",
		"198.51.100.9:4242", "alice", pollBody)
	require.Equal(t, http.StatusOK, pollW.Code, pollW.Body.String())
	var pollResp gen.SignInPollResponse
	require.NoError(t, json.Unmarshal(pollW.Body.Bytes(), &pollResp))
	require.Equal(t, gen.SignInPollResponseStateSignedIn, pollResp.State)

	entries := readAuditEntries(t, auditDir, "provider.signed_in")
	require.NotEmpty(t, entries, "provider.signed_in must be recorded")
	entry := entries[len(entries)-1]
	assert.Equal(t, "alice", entry["user"],
		"O6: the audit entry must record the authenticated actor, not be indistinguishable from an anonymous caller")
	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "entry must carry a details object")
	assert.Equal(t, "198.51.100.9", details["source_ip"],
		"O6: the audit entry must record the caller's source_ip")
}

// TestSignOut_Audit_RecordsActorAndSourceIP proves handleProviderSignOut's
// "provider.signed_out" entry carries both the actor and source_ip.
func TestSignOut_Audit_RecordsActorAndSourceIP(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	auditDir := attachTestAuditor(t, api)

	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	blob, err := json.Marshal(map[string]any{"access_token": "tok", "account_id": "acc"})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(blob)))

	w := doJSONAs(t, api, http.MethodDelete, "/api/v1/providers/openai-chatgpt/sign-in",
		"203.0.113.42:9999", "bob", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	entries := readAuditEntries(t, auditDir, "provider.signed_out")
	require.NotEmpty(t, entries, "provider.signed_out must be recorded")
	entry := entries[len(entries)-1]
	assert.Equal(t, "bob", entry["user"],
		"O6: an operator investigating 'who signed me out' must see the real actor, not an entry indistinguishable from an anonymous pre-auth caller")
	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "entry must carry a details object")
	assert.Equal(t, "203.0.113.42", details["source_ip"])
}

// TestSignInImport_Audit_RecordsActorAndSourceIP proves
// handleProviderSignInImport's "provider.signed_in" (via codex_cli_import)
// entry carries both the actor and source_ip.
func TestSignInImport_Audit_RecordsActorAndSourceIP(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	auditDir := attachTestAuditor(t, api)

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	authPath := filepath.Join(codexHome, "auth.json")
	require.NoError(t, os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"codex-tok","account_id":"codex-acc"}}`), 0o600))

	w := doJSONAs(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/import",
		"192.0.2.55:1111", "carol", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	entries := readAuditEntries(t, auditDir, "provider.signed_in")
	require.NotEmpty(t, entries, "provider.signed_in must be recorded")
	entry := entries[len(entries)-1]
	assert.Equal(t, "carol", entry["user"])
	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "entry must carry a details object")
	assert.Equal(t, "192.0.2.55", details["source_ip"])
}

// --- O12: interval_seconds clamped to the contract's maximum: 30 -----------

// TestSignInStart_IntervalSeconds_ClampedToContractMax proves the start
// response never emits a vendor-advertised interval above the contract's
// declared maximum (SignInStartResponseDeviceCode.yaml: maximum: 30).
func TestSignInStart_IntervalSeconds_ClampedToContractMax(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// A standalone mux (not deviceCodeVendorMux, whose usercode pattern
	// would collide with this one) whose usercode endpoint advertises an
	// interval the contract forbids (60s > the 30s ceiling).
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"device_auth_id": "vendor_das_2", "user_code": "AAAA-BBBB", "interval": 60}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	withDeviceCodeVendor(t, server)

	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	variant, err := resp.AsSignInStartResponseDeviceCode()
	require.NoError(t, err)
	assert.LessOrEqual(t, variant.IntervalSeconds, maxSignInIntervalSeconds,
		"O12: a vendor-advertised interval above the contract's maximum:30 must be clamped")
}

// TestClampSignInIntervalSeconds is a direct table-driven proof of the
// shared clamp helper every O12 producer (start, widen, and the poll
// handler's !stored fallback) routes through.
func TestClampSignInIntervalSeconds(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"below floor clamps to 1", 0, 1},
		{"negative clamps to 1", -5, 1},
		{"in range passes through", 15, 15},
		{"exactly at ceiling passes through", 30, 30},
		{"above ceiling clamps to 30", 31, 30},
		{"first slow_down widening (5*2=10) stays unclamped", 10, 10},
		{"third slow_down widening (20*2=40) clamps to 30", 40, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clampSignInIntervalSeconds(tc.in))
		})
	}
}

// TestWidenDeviceSessionInterval_ClampsToContractMax proves the stored
// per-session interval itself never exceeds the contract's maximum after a
// slow_down widening — this is the field a CONCURRENT status read (or the
// next poll) would also observe, so the stored value must be clamped, not
// just the value handed back on this one response.
func TestWidenDeviceSessionInterval_ClampsToContractMax(t *testing.T) {
	api := &restAPI{}
	api.putDeviceSession("h1", deviceCodeSession{
		providerID:      "openai-chatgpt",
		intervalSeconds: 20, // 20*2 = 40, which exceeds the 30s contract ceiling
		expiresAt:       time.Now().Add(time.Hour),
	})

	widened, ok := api.widenDeviceSessionInterval("h1")
	require.True(t, ok)
	assert.LessOrEqual(t, widened, maxSignInIntervalSeconds,
		"O12: widening must clamp, not just apply *2/+5 unbounded")

	// The clamp must have been persisted to the stored session too, not just
	// the return value — a concurrent status read must see the same bound.
	sess, found := api.getDeviceSession("h1")
	require.True(t, found)
	assert.LessOrEqual(t, sess.intervalSeconds, maxSignInIntervalSeconds)
}

// --- O18: codex-cli import must not leave ExpiresAt zero --------------------

// TestSignInImport_NonJWTToken_UsesMtimeFallbackExpiry proves that importing
// a codex-cli access token that is NOT a parseable JWT (so
// jwtUnverifiedExpiry fails) still ends up with a non-zero ExpiresAt on the
// stored credential — ReadCodexCliCredentials' own mtime+1h estimate —
// instead of the permanent "signed in forever" zero value.
func TestSignInImport_NonJWTToken_UsesMtimeFallbackExpiry(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	authPath := filepath.Join(codexHome, "auth.json")
	// "not-a-jwt-token" has no "." separators, so jwtUnverifiedExpiry returns
	// ok=false and the handler must fall back to the mtime-based estimate.
	require.NoError(t, os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"not-a-jwt-token","account_id":"codex-acc"}}`), 0o600))

	beforeCall := time.Now()
	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/import", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	raw, getErr := store.Get(credentials.OAuthEntryName("openai"))
	require.NoError(t, getErr)

	var stored struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))

	assert.False(t, stored.ExpiresAt.IsZero(),
		"O18: a non-JWT import must not leave ExpiresAt zero — needsOAuthRefresh treats zero as 'never needs refresh'")
	// ReadCodexCliCredentials' fallback is auth.json's mtime + 1h; the file
	// was just written, so the expiry must land close to now+1h.
	assert.WithinDuration(t, beforeCall.Add(time.Hour), stored.ExpiresAt, 2*time.Minute,
		"expiry should be the auth.json mtime+1h fallback, not some other value")
}

// TestSignInImport_JWTToken_StillUsesJWTExpiry is a guard against a
// regression where the mtime fallback is used unconditionally: a genuine
// JWT's own exp claim must still win.
func TestSignInImport_JWTToken_StillUsesJWTExpiry(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	authPath := filepath.Join(codexHome, "auth.json")

	// A minimal unsigned JWT with exp far in the future (2099-01-01), so it
	// is trivially distinguishable from the mtime+1h fallback.
	// header: {"alg":"none"} / payload: {"exp":4070908800}
	jwt := "eyJhbGciOiJub25lIn0.eyJleHAiOjQwNzA5MDg4MDB9.sig"
	require.NoError(t, os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"`+jwt+`","account_id":"codex-acc"}}`), 0o600))

	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/import", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	raw, getErr := store.Get(credentials.OAuthEntryName("openai"))
	require.NoError(t, getErr)

	var stored struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	assert.Equal(t, int64(4070908800), stored.ExpiresAt.Unix(),
		"a real JWT's own exp claim must still be used, not overridden by the mtime fallback")
}

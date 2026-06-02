//go:build !cgo

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// newLockedStoreChannelAPI builds a channel API whose credential store is
// present but NOT unlocked, so storeCredential / credentialRefResolves return
// ErrStoreLocked — used to exercise the store-fault paths deterministically.
func newLockedStoreChannelAPI(t *testing.T, configJSON string) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(configJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		credStore:     credentials.NewStore(tmpDir + "/credentials.json"), // locked
	}
}

func newChannelTestAPI(t *testing.T, configJSON string) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(configJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return newOnboardingTestAPI(t, tmpDir, al)
}

// TestConfigureChannel_RoutesSecretToCredentialStore is the core #289 guard:
// configuring a token-based channel via the UI must store the secret in the
// encrypted credential store and persist only its <field>_ref in config.json —
// never the plaintext (SEC-23) — and "Test" must then report success.
func TestConfigureChannel_RoutesSecretToCredentialStore(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"super-secret-123"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// 1. The plaintext secret must NOT appear anywhere in config.json (SEC-23).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "super-secret-123", "plaintext token leaked into config.json")

	// 2. token_ref must be set to the conventional credential name; no inline token.
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	tg := diskCfg["channels"].(map[string]any)["telegram"].(map[string]any)
	assert.Equal(t, "channel_telegram_token", tg["token_ref"])
	_, hasInline := tg["token"]
	assert.False(t, hasInline, "inline token must not be persisted to config.json")

	// 3. The secret must be retrievable from the credential store.
	got, err := api.credStore.Get("channel_telegram_token")
	require.NoError(t, err)
	assert.Equal(t, "super-secret-123", got)

	// 4. testChannel now reports success because the ref resolves.
	w2 := httptest.NewRecorder()
	api.testChannel(w2, "telegram")
	require.Equal(t, http.StatusOK, w2.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.True(t, resp.Success, "test should pass once the credential is stored: %s", resp.Message)
}

// TestConfigureChannel_MultiSecretChannel covers a channel with two secret
// fields (slack: bot_token + app_token) — both must be routed to refs.
func TestConfigureChannel_MultiSecretChannel(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/slack/configure",
		strings.NewReader(`{"bot_token":"xoxb-aaa","app_token":"xapp-bbb"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "slack")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "xoxb-aaa")
	assert.NotContains(t, string(raw), "xapp-bbb")

	for field, want := range map[string]string{"bot_token": "xoxb-aaa", "app_token": "xapp-bbb"} {
		got, err := api.credStore.Get("channel_slack_" + field)
		require.NoError(t, err, "field %s", field)
		assert.Equal(t, want, got, "field %s", field)
	}
}

// TestTestChannel_FailsWhenSecretMissing confirms "Test" reports failure (not a
// false success) when a required secret has no stored credential — the exact
// lie #289 set out to fix.
func TestTestChannel_FailsWhenSecretMissing(t *testing.T) {
	api := newChannelTestAPI(t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false}}}`)

	w := httptest.NewRecorder()
	api.testChannel(w, "telegram")
	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "test must fail when the token credential is absent")
	assert.Contains(t, resp.Message, "token")
}

// TestConfigureChannel_ScrubsStaleInlinePlaintext is the key security-remediation
// guard: a config left with an inline plaintext secret by the pre-#289 bug must
// be scrubbed on the next save — even when the user edits an UNRELATED field and
// does not re-supply the secret (the realistic "[configured]" UI flow).
func TestConfigureChannel_ScrubsStaleInlinePlaintext(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false,"token":"LEAKED-OLD-PLAINTEXT","parse_mode":"Markdown"}}}`,
	)

	// Edit only a non-secret field; do NOT re-send the token.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"parse_mode":"HTML"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "LEAKED-OLD-PLAINTEXT", "stale inline plaintext survived the save (SEC-23)")

	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	tg := diskCfg["channels"].(map[string]any)["telegram"].(map[string]any)
	_, hasInline := tg["token"]
	assert.False(t, hasInline, "stale inline token key must be removed")
	assert.Equal(t, "HTML", tg["parse_mode"], "the unrelated edit must still apply")
}

// TestConfigureChannel_ClearSecretDeletesCredential covers the clear/rotate path:
// re-configuring with an empty value clears the ref AND deletes the stored
// credential so a "removed" token does not linger encrypted at rest.
func TestConfigureChannel_ClearSecretDeletesCredential(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	// Set a token, then clear it.
	for _, body := range []string{`{"token":"secret-1"}`, `{"token":"   "}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "telegram")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	}

	// Ref cleared in config; credential gone from the store.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	tg := diskCfg["channels"].(map[string]any)["telegram"].(map[string]any)
	assert.Equal(t, "", tg["token_ref"], "token_ref must be cleared")
	_, err = api.credStore.Get("channel_telegram_token")
	assert.Error(t, err, "the stored credential must be deleted on clear")

	// Test now reports failure (the channel is unconfigured again).
	w := httptest.NewRecorder()
	api.testChannel(w, "telegram")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
}

// TestTestChannel_MatrixMixedRequiredFields locks in the inline-vs-ref
// discrimination: matrix requires homeserver + user_id (non-secret, inline) AND
// access_token (secret, ref). Both kinds must be validated in their own way.
func TestTestChannel_MatrixMixedRequiredFields(t *testing.T) {
	// access_token routed to a stored ref; homeserver + user_id inline.
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"matrix":{"enabled":false,"homeserver":"https://m.org","user_id":"@a:m.org","access_token_ref":"channel_matrix_access_token"}}}`,
	)
	_, err := api.storeCredential("channel_matrix_access_token", "syt-token")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	api.testChannel(w, "matrix")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success, "all required fields present (inline + ref): %s", resp.Message)

	// Drop the non-secret user_id → Test must fail on the inline field.
	api2 := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"matrix":{"enabled":false,"homeserver":"https://m.org","access_token_ref":"channel_matrix_access_token"}}}`,
	)
	_, err = api2.storeCredential("channel_matrix_access_token", "syt-token")
	require.NoError(t, err)
	w2 := httptest.NewRecorder()
	api2.testChannel(w2, "matrix")
	var resp2 gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.False(t, resp2.Success)
	assert.Contains(t, resp2.Message, "user_id")
}

// TestGetChannelConfig_ShowsConfiguredMarker guards MAJ-001: after the secret
// moves to the credential store (only token_ref in config), GET must still show
// the "[configured]" marker so the UI knows a secret is set — never the secret.
func TestGetChannelConfig_ShowsConfiguredMarker(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"super-secret-123"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	api.getChannelConfig(w2, "telegram")
	require.Equal(t, http.StatusOK, w2.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &got))
	assert.Equal(t, "[configured]", got["token"], "GET must show the configured marker via the ref")
	assert.NotContains(t, w2.Body.String(), "super-secret-123", "GET must never echo the secret")
}

// TestConfigureChannel_RejectsNonStringSecret guards MIN-002: a non-string
// secret value must be rejected, not silently treated as a clear (which would
// revoke a working credential).
func TestConfigureChannel_RejectsNonStringSecret(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":12345}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	assert.Equal(t, http.StatusBadRequest, w.Code, "non-string secret must be rejected")
}

// TestConfigureChannel_StoreFaultFailsClosed guards MIN-001: when the credential
// store is unavailable (locked), configure must fail with 500 and NOT modify
// config.json — no ref written, no plaintext written (fail-closed).
func TestConfigureChannel_StoreFaultFailsClosed(t *testing.T) {
	api := newLockedStoreChannelAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	before, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-x"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	assert.Equal(t, http.StatusInternalServerError, w.Code, "store fault must fail the request")

	after, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "config.json must be unchanged on store fault (fail-closed)")
	assert.NotContains(t, string(after), "secret-x")
}

// TestTestChannel_StoreUnavailableIsDistinct guards MIN-001 + the (bool,error)
// contract: a locked store must report "credential store unavailable", NOT a
// misleading "missing token", so the user isn't told to re-enter a present secret.
func TestTestChannel_StoreUnavailableIsDistinct(t *testing.T) {
	api := newLockedStoreChannelAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false,"token_ref":"channel_telegram_token"}}}`,
	)

	w := httptest.NewRecorder()
	api.testChannel(w, "telegram")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "unavailable", "store fault must be reported distinctly, not as 'missing'")
	assert.NotContains(t, resp.Message, "missing")
}

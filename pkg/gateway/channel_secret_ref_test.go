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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
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
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	tg, tgOk := channelsMap["telegram"].(map[string]any)
	require.True(t, tgOk, "config.channels.telegram must be an object")
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
//
// The fixture also carries a legitimate token_ref (with its credential
// pre-stored) alongside the stale inline plaintext: since Stage 1 of the
// channel-Test redesign rejects an incomplete save, the config here must be
// genuinely complete (required "token" satisfied via token_ref) for the save
// to succeed — the scrub assertions below are otherwise unchanged.
func TestConfigureChannel_ScrubsStaleInlinePlaintext(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false,"token":"LEAKED-OLD-PLAINTEXT","token_ref":"channel_telegram_token","parse_mode":"Markdown"}}}`,
	)
	_, err := api.storeCredential("channel_telegram_token", "legit-secret")
	require.NoError(t, err)

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
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	tg, tgOk := channelsMap["telegram"].(map[string]any)
	require.True(t, tgOk, "config.channels.telegram must be an object")
	_, hasInline := tg["token"]
	assert.False(t, hasInline, "stale inline token key must be removed")
	assert.Equal(t, "HTML", tg["parse_mode"], "the unrelated edit must still apply")
}

// TestConfigureChannel_ClearSecretDeletesCredential covers the clear/rotate path:
// re-configuring with an empty value clears the ref AND deletes the stored
// credential so a "removed" secret does not linger encrypted at rest.
//
// Clears a NON-required sensitive field — matrix's crypto_passphrase
// (channelSensitiveFields["matrix"] includes it, channelRequiredFields does
// not) — because Stage 1 of the channel-Test redesign now rejects a save that
// would clear a REQUIRED secret (see
// TestConfigureChannel_RejectsClearingRequiredSecret for that path).
func TestConfigureChannel_ClearSecretDeletesCredential(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	// Configure matrix fully (all three required fields) plus the optional
	// crypto_passphrase, then clear only crypto_passphrase.
	for _, body := range []string{
		`{"homeserver":"https://m.org","user_id":"@a:m.org","access_token":"syt-token","crypto_passphrase":"pp-1"}`,
		`{"crypto_passphrase":"   "}`,
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/matrix/configure", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "matrix")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	}

	// Ref cleared in config; credential gone from the store.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	mx, mxOk := channelsMap["matrix"].(map[string]any)
	require.True(t, mxOk, "config.channels.matrix must be an object")
	assert.Equal(t, "", mx["crypto_passphrase_ref"], "crypto_passphrase_ref must be cleared")
	_, err = api.credStore.Get("channel_matrix_crypto_passphrase")
	assert.Error(t, err, "the stored credential must be deleted on clear")

	// Test still reports success: crypto_passphrase is not required, and
	// homeserver/user_id/access_token remain configured.
	w := httptest.NewRecorder()
	api.testChannel(w, "matrix")
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(
		t,
		resp.Success,
		"matrix must still pass Test after clearing the non-required crypto_passphrase: %s",
		resp.Message,
	)
}

// TestConfigureChannel_RejectsClearingRequiredSecret guards Stage 1 of the
// channel-Test redesign: clearing a REQUIRED secret field must be rejected —
// it would leave the persisted config unable to construct the channel — and
// the rejection must be a true no-op: the existing credential is left
// resolvable, unchanged, in the store.
func TestConfigureChannel_RejectsClearingRequiredSecret(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-1"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"   "}`))
	r2.Header.Set("Content-Type", "application/json")
	api.configureChannel(w2, r2, "telegram")
	assert.Equal(
		t,
		http.StatusBadRequest,
		w2.Code,
		"clearing a required secret must be rejected, body=%s",
		w2.Body.String(),
	)
	assert.Contains(t, w2.Body.String(), "token")

	// Zero-side-effect proof: the existing credential still resolves unchanged.
	got, err := api.credStore.Get("channel_telegram_token")
	require.NoError(t, err)
	assert.Equal(t, "secret-1", got, "a rejected clear must not touch the existing credential")
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

// TestConfigureChannel_GoogleChatWebhookRoutesToCredentialStore is the MAJ-1
// guard (Epic #314): a Google Chat incoming-webhook URL is a possession-based
// bearer secret (SEC-23 class). Configuring it via the UI must route it into the
// encrypted credential store and persist only webhook_url_ref — never the
// plaintext URL — and "Test" must then report the secret as resolvable.
func TestConfigureChannel_GoogleChatWebhookRoutesToCredentialStore(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	const secretURL = "https://chat.googleapis.com/v1/spaces/AAA/messages?key=KKK&token=TTT"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/google-chat/configure",
		strings.NewReader(`{"mode":"webhook","webhook_url":"`+secretURL+`"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "google-chat")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// 1. The plaintext webhook URL must NOT appear anywhere in config.json (SEC-23).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secretURL, "plaintext webhook_url leaked into config.json")
	assert.NotContains(t, string(raw), "token=TTT", "webhook secret query param leaked into config.json")

	// 2. webhook_url_ref must be set; no inline webhook_url.
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	gc, gcOk := channelsMap["google-chat"].(map[string]any)
	require.True(t, gcOk, "config.channels.google-chat must be an object")
	assert.Equal(t, "channel_google-chat_webhook_url", gc["webhook_url_ref"])
	_, hasInline := gc["webhook_url"]
	assert.False(t, hasInline, "inline webhook_url must not be persisted to config.json")

	// 3. The secret must be retrievable from the credential store.
	got, err := api.credStore.Get("channel_google-chat_webhook_url")
	require.NoError(t, err)
	assert.Equal(t, secretURL, got)

	// 4. credentialRefResolves (the Test path) must report the webhook ref as resolvable.
	ok, err := api.credentialRefResolves("channel_google-chat_webhook_url")
	require.NoError(t, err)
	assert.True(t, ok, "Test must see the stored webhook credential as resolvable")
}

// TestConfigureChannel_GoogleChatServiceAccountRoutesToCredentialStore covers the
// service-account-JSON secret path, which was effectively broken before MAJ-1:
// configureChannel wrote service_account_json_ref, but GoogleChatConfig had no
// *Ref struct field and ResolveAll did not list it, so the secret was dropped.
func TestConfigureChannel_GoogleChatServiceAccountRoutesToCredentialStore(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	const saJSON = `{"client_email":"bot@proj.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nSECRETKEYMATERIAL\n-----END PRIVATE KEY-----\n"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/google-chat/configure",
		strings.NewReader(`{"mode":"bot","service_account_json":`+mustJSONString(t, saJSON)+`}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "google-chat")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "SECRETKEYMATERIAL", "plaintext service account key leaked into config.json")

	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channelsMap, channelsMapOk := diskCfg["channels"].(map[string]any)
	require.True(t, channelsMapOk, "config.channels must be an object")
	gc, gcOk := channelsMap["google-chat"].(map[string]any)
	require.True(t, gcOk, "config.channels.google-chat must be an object")
	assert.Equal(t, "channel_google-chat_service_account_json", gc["service_account_json_ref"])
	_, hasInline := gc["service_account_json"]
	assert.False(t, hasInline, "inline service_account_json must not be persisted to config.json")

	got, err := api.credStore.Get("channel_google-chat_service_account_json")
	require.NoError(t, err)
	assert.Equal(t, saJSON, got)
}

// mustJSONString returns s encoded as a JSON string literal (with quotes).
func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

// TestTestChannel_SlackRequiresAppToken guards the channelRequiredFields fix:
// the slack constructor (pkg/channels/slack/slack.go NewSlackChannel) hard
// requires BOTH bot_token and app_token, so "Test" reporting success with only
// bot_token configured was a lie — an operator would enable slack and it would
// fail to construct at boot. Both refs must resolve for success.
//
// Both fields are supplied in a single configure call because Stage 1 of the
// channel-Test redesign now rejects a save that would leave a
// multi-field-required channel partially configured — see
// TestConfigureChannel_RejectsPartialMultiFieldRequired for that rejection.
func TestTestChannel_SlackRequiresAppToken(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/slack/configure",
		strings.NewReader(`{"bot_token":"xoxb-aaa","app_token":"xapp-bbb"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "slack")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	w2 := httptest.NewRecorder()
	api.testChannel(w2, "slack")
	require.Equal(t, http.StatusOK, w2.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.True(
		t,
		resp.Success,
		"slack must pass Test once both bot_token and app_token are configured: %s",
		resp.Message,
	)
}

// TestConfigureChannel_RejectsPartialMultiFieldRequired guards Stage 1 of the
// channel-Test redesign: slack requires BOTH bot_token and app_token
// (channelRequiredFields["slack"]); configuring only one must be rejected
// before either the config entry or the credential reaches disk.
func TestConfigureChannel_RejectsPartialMultiFieldRequired(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/slack/configure",
		strings.NewReader(`{"bot_token":"xoxb-aaa"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "slack")
	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"a partial multi-field-required save must be rejected, body=%s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "app_token")

	// Nothing persisted: no slack config entry, no bot_token credential.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	_, hasSlack := channels["slack"]
	assert.False(t, hasSlack, "a rejected partial save must persist nothing")

	_, err = api.credStore.Get("channel_slack_bot_token")
	assert.Error(t, err, "a rejected partial save must not store the bot_token credential")
}

// TestConfigureChannel_GoogleChatEitherOr guards Stage 1 of the channel-Test
// redesign applied to Google Chat's either/or auth-path requirement (mirrors
// testChannel's special case below): a save naming neither webhook_url nor a
// service-account field must be rejected, naming both alternatives in the
// message; adding webhook_url then satisfies the requirement.
func TestConfigureChannel_GoogleChatEitherOr(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/google-chat/configure",
		strings.NewReader(`{"space":"spaces/AAA"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "google-chat")
	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"gchat save with neither auth path must be rejected, body=%s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "webhook_url")
	assert.Contains(t, w.Body.String(), "service_account")

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/google-chat/configure",
		strings.NewReader(
			`{"space":"spaces/AAA","webhook_url":"https://chat.googleapis.com/v1/spaces/AAA/messages?key=K&token=T"}`,
		),
	)
	r2.Header.Set("Content-Type", "application/json")
	api.configureChannel(w2, r2, "google-chat")
	assert.Equal(
		t,
		http.StatusOK,
		w2.Code,
		"adding webhook_url must satisfy the auth-path requirement, body=%s",
		w2.Body.String(),
	)
}

// TestTestChannel_GoogleChatRequiresAuthPath guards the special-case fix in
// testChannel: channelRequiredFields["google-chat"] is deliberately {} because
// the real requirement is either/or (webhook_url OR service_account_json OR
// service_account_file), which the flat AND-list can't express. Before this
// fix "Test" reported success on a completely blank google-chat instance.
func TestTestChannel_GoogleChatRequiresAuthPath(t *testing.T) {
	// A blank instance (no auth path configured at all) must fail with a clear
	// message, not silently report success.
	blank := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"google-chat":{"enabled":false}}}`,
	)
	w := httptest.NewRecorder()
	blank.testChannel(w, "google-chat")
	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "a blank google-chat instance must fail Test")
	assert.Contains(t, resp.Message, "webhook_url")
	assert.Contains(t, resp.Message, "service_account")

	// webhook_url configured (via credential store, ref-routed) satisfies the
	// requirement on its own.
	webhookAPI := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	wc := httptest.NewRecorder()
	rc := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/google-chat/configure",
		strings.NewReader(
			`{"mode":"webhook","webhook_url":"https://chat.googleapis.com/v1/spaces/AAA/messages?key=K&token=T"}`,
		),
	)
	rc.Header.Set("Content-Type", "application/json")
	webhookAPI.configureChannel(wc, rc, "google-chat")
	require.Equal(t, http.StatusOK, wc.Code, "body=%s", wc.Body.String())
	w2 := httptest.NewRecorder()
	webhookAPI.testChannel(w2, "google-chat")
	var resp2 gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.True(t, resp2.Success, "webhook_url alone must satisfy the auth-path requirement: %s", resp2.Message)

	// service_account_file (a non-secret, inline path field — not credential
	// routed) also satisfies the requirement on its own.
	saFileAPI := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"google-chat":{"enabled":false,"mode":"bot","service_account_file":"/etc/omnipus/gchat-sa.json"}}}`,
	)
	w3 := httptest.NewRecorder()
	saFileAPI.testChannel(w3, "google-chat")
	var resp3 gen.ChannelTestResponse
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &resp3))
	assert.True(
		t,
		resp3.Success,
		"service_account_file alone must satisfy the auth-path requirement: %s",
		resp3.Message,
	)
}

// TestChannels_EmailIsUnknown guards the M11 retirement of the legacy
// /channels/email/* surface: email is NOT a channel (config.knownChannelTypes
// deliberately excludes it; the SPA uses /agents/{id}/mailbox exclusively).
// validChannelIDs, channelSensitiveFields, and channelRequiredFields must not
// carry an "email" entry — the routing gate in HandleChannels must 404 it as
// an unknown channel type, not treat it as a valid-but-misconfigured channel.
func TestChannels_EmailIsUnknown(t *testing.T) {
	assert.False(t, validChannelIDs["email"], "email must not be a valid channel ID")
	_, hasSensitive := channelSensitiveFields["email"]
	assert.False(t, hasSensitive, "channelSensitiveFields must not carry a legacy email entry")
	_, hasRequired := channelRequiredFields["email"]
	assert.False(t, hasRequired, "channelRequiredFields must not carry a legacy email entry")

	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/email", nil)
	api.HandleChannels(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code, "GET /channels/email must 404 as an unknown channel type")
}

// TestSetChannelEnabled_RejectsIncompleteConfig guards Stage 1 of the
// channel-Test redesign: enabling a channel whose persisted config is
// incomplete must be rejected — an enabled-but-incomplete channel would fail
// to construct on the next reload/boot. Completing the config then allows
// enable to succeed. Disabling never validates (s1_whatsapp_enable_reload_test.go
// exercises that path; whatsapp's empty required-fields list also always
// passes here regardless).
func TestSetChannelEnabled_RejectsIncompleteConfig(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false}}}`,
	)

	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "telegram", true)
	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"enabling an incomplete telegram config must be rejected, body=%s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "token")

	// Complete the config, then enable must succeed.
	wc := httptest.NewRecorder()
	rc := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-1"}`))
	rc.Header.Set("Content-Type", "application/json")
	api.configureChannel(wc, rc, "telegram")
	require.Equal(t, http.StatusOK, wc.Code, "body=%s", wc.Body.String())

	w2 := httptest.NewRecorder()
	api.setChannelEnabled(w2, "telegram", true)
	assert.Equal(t, http.StatusOK, w2.Code, "enabling a complete config must succeed, body=%s", w2.Body.String())
}

// rest_config_test.go: tests for read and write config.json, credential refs, and service rewiring

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledChannelCredential
// pins Task 1: refreshConfigAndRewireServices must reject the in-memory
// refresh (return an error, do NOT SwapConfig) when an ENABLED channel's
// credential ref is present in the store but fails to decrypt — a corrupted
// store entry / wrong-master-key scenario, distinct from (and worse than) a
// simple missing ref. This is the REST-write-path counterpart to
// TestExecuteReload_RejectsOnCorruptedEnabledChannelCredential
// (reload_rollback_test.go) and
// TestGatewayBoot_CorruptedCredentialForEnabledChannelFailsFast
// (boot_order_test.go).
func TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledChannelCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "TELEGRAM_TOKEN")

	credStore := credentials.NewStore(credsPath)
	require.NoError(t, credentials.Unlock(credStore))

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": tmpDir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 19989},
		"providers": []any{},
		"channels": map[string]any{
			"telegram": map[string]any{"type": "telegram", "enabled": true, "token_ref": "TELEGRAM_TOKEN"},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19989},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, baseCfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	err = api.refreshConfigAndRewireServices(configPath)
	require.Error(t, err,
		"expected refreshConfigAndRewireServices to reject a corrupted enabled-channel credential")
	assert.Contains(t, err.Error(), "TELEGRAM_TOKEN")
	assert.Contains(t, err.Error(), "failed to resolve")

	// The broken config must NOT have been swapped into the live in-memory
	// pointer — the gateway keeps serving the previous good config even
	// though config.json on disk was already durably written before this
	// method ran.
	assert.Same(t, baseCfg, al.GetConfig(), "in-memory config must not be swapped on rejection")
}

// TestRefreshConfigAndRewireServices_RejectsOnMissingEnabledChannelCredential
// pins the other half of Task 1: a genuinely missing (NotFoundError)
// credential ref on an ENABLED channel must also be rejected, not merely the
// "worse than missing" corrupted case above — this path previously
// Warn-logged even a flatly missing ref on an enabled channel.
func TestRefreshConfigAndRewireServices_RejectsOnMissingEnabledChannelCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore))
	// Deliberately do NOT store "TELEGRAM_TOKEN" — the ref is genuinely absent.

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": tmpDir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 19990},
		"providers": []any{},
		"channels": map[string]any{
			"telegram": map[string]any{"type": "telegram", "enabled": true, "token_ref": "TELEGRAM_TOKEN"},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19990},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, baseCfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	err = api.refreshConfigAndRewireServices(configPath)
	require.Error(t, err,
		"expected rejection when an enabled channel's credential ref is genuinely missing, not just Warn-logged")
	assert.Contains(t, err.Error(), "TELEGRAM_TOKEN")
	assert.Contains(t, err.Error(), "not found in store")

	assert.Same(t, baseCfg, al.GetConfig(), "in-memory config must not be swapped on rejection")
}

// TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledMailboxCredential
// pins the mailbox gap in buildEnabledRefMap: cfg.Mailboxes[agentID]
// [workspaceID].PasswordRef is resolved by credentials.ResolveAll via its
// own dedicated per-(agent,workspace) loop (pkg/credentials/inject.go) — NOT
// part of the nonChannelRefs slice — so buildEnabledRefMap omitted mailbox
// refs entirely despite its doc comment's (factually wrong) claim to mirror
// everything ResolveAll's nonChannelRefs can error on.
//
// bootCredentials and executeReload happened to be masked from this gap by
// their own unconditional InjectFromConfig call, which independently walks
// cfg.Mailboxes and treats ANY mailbox resolution failure as fatal
// regardless of Enabled — see InjectFromConfig, pkg/credentials/inject.go.
// refreshConfigAndRewireServices has no InjectFromConfig call, only the
// buildEnabledRefMap-gated ResolveBundle check below, so this is the call
// site the gap was actually unmasked at: a corrupted enabled-mailbox
// credential ref fell through to a Warn-only log instead of being escalated
// — the opposite of what this whole credential-escalation cluster
// guarantees for every other in-use credential category. This is reached in
// production by setAgentMailbox (rest_mailbox.go) itself, via
// safeUpdateConfigJSON -> updateConfigJSONLocked ->
// refreshConfigAndRewireServices.
func TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledMailboxCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "MAILBOX_PW_REF")

	credStore := credentials.NewStore(credsPath)
	require.NoError(t, credentials.Unlock(credStore))

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": tmpDir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 19991},
		"providers": []any{},
		"mailboxes": map[string]any{
			"mia": map[string]any{
				"default": map[string]any{
					"enabled":      true,
					"workspace_id": "default",
					"password_ref": "MAILBOX_PW_REF",
				},
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19991},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, baseCfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	err = api.refreshConfigAndRewireServices(configPath)
	require.Error(t, err,
		"expected refreshConfigAndRewireServices to reject a corrupted enabled-mailbox credential")
	assert.Contains(t, err.Error(), "MAILBOX_PW_REF")
	assert.Contains(t, err.Error(), "failed to resolve")

	// The broken config must NOT have been swapped into the live in-memory
	// pointer, same invariant as the enabled-channel counterpart above.
	assert.Same(t, baseCfg, al.GetConfig(), "in-memory config must not be swapped on rejection")
}

// TestSanitizeConfigForWire pins the shared wire-sanitizer getConfig (and
// any future config-serving endpoint) relies on: every key listed in
// wireExcludedConfigFields is removed, everything else is untouched, and
// the current exclusion list still covers seeded_skill_grants (the ADR-074 D4
// internal-only marker that must never cross the wire).
func TestSanitizeConfigForWire(t *testing.T) {
	m := map[string]any{
		"gateway":                    map[string]any{"port": float64(5000)},
		"seeded_skill_grants":        []any{"define-skill-allowlist"},
		"seeded_tool_policy_updates": []any{"adr084-worker-goal-claim-allow"},
	}
	for _, k := range wireExcludedConfigFields {
		m[k] = "internal"
	}

	sanitizeConfigForWire(m)

	for _, k := range wireExcludedConfigFields {
		if _, present := m[k]; present {
			t.Fatalf("wire-excluded key %q survived sanitizeConfigForWire", k)
		}
	}
	if _, present := m["seeded_skill_grants"]; present {
		t.Fatal("seeded_skill_grants must be in the exclusion list and stripped from the wire")
	}
	if _, present := m["seeded_tool_policy_updates"]; present {
		t.Fatal("seeded_tool_policy_updates must be in the exclusion list and stripped from the wire")
	}
	if _, present := m["gateway"]; !present {
		t.Fatal("sanitizeConfigForWire must not touch keys outside wireExcludedConfigFields")
	}
}

// --- E10: HandleConfig GET redaction test ---

// TestHandleConfigGETRedaction verifies that GET /api/v1/config returns valid JSON
// without raw credential values.
// BDD: Given a running gateway,
// When GET /api/v1/config is called,
// Then 200 with valid JSON and no raw API key patterns in the response body.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23) (E10)
func TestHandleConfigGETRedaction(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Must be valid JSON.
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &cfg),
		"config response must be valid JSON")

	// Must not contain raw API key patterns.
	for _, pattern := range []string{"sk-ant-", "sk-proj-", "ghp_", "sk-or-"} {
		assert.NotContains(t, body, pattern,
			"config response must not contain credential pattern %q", pattern)
	}
}

// TestHandleConfigGETFieldsWithSensitiveNamesAreRedacted verifies that any
// config fields whose names contain "key", "token", "secret", or "password"
// are redacted if non-empty.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Redact sensitive field names (SEC-23) (E10)
func TestHandleConfigGETFieldsWithSensitiveNamesAreRedacted(t *testing.T) {
	// We test this via the redactSensitiveFields helper directly (covered by rest_test.go).
	// This test verifies the same property through the HandleConfig endpoint.
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cfg))

	// Walk all string values in the response recursively; none should look like a raw credential.
	var checkNoRawCredentials func(m map[string]any)
	checkNoRawCredentials = func(m map[string]any) {
		for k, v := range m {
			if strVal, ok := v.(string); ok && strVal != "" {
				kl := strings.ToLower(k)
				isSensitiveKey := strings.Contains(kl, "key") ||
					strings.Contains(kl, "token") ||
					strings.Contains(kl, "secret") ||
					strings.Contains(kl, "password")
				if isSensitiveKey {
					assert.Equal(t, "[redacted]", strVal,
						"field %q with sensitive name must be redacted", k)
				}
			}
			if sub, ok := v.(map[string]any); ok {
				checkNoRawCredentials(sub)
			}
		}
	}
	checkNoRawCredentials(cfg)
}

// --- redactSensitiveFields tests ---

// TestRedactSensitiveFields verifies that credential fields are redacted in config responses.
// BDD: Given a config map containing fields named "api_key", "token", "secret",
// When redactSensitiveFields is called,
// Then those fields are replaced with "[redacted]" and non-sensitive fields are unchanged.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23)
func TestRedactSensitiveFields(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]any
		wantKey   string
		wantValue any
	}{
		// Dataset: Redaction — row 1
		{
			name:      "api_key is redacted",
			input:     map[string]any{"api_key": "sk-abc-123"},
			wantKey:   "api_key",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 2
		{
			name:      "token is redacted",
			input:     map[string]any{"bearer_token": "tok-xyz"},
			wantKey:   "bearer_token",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 3
		{
			name:      "secret is redacted",
			input:     map[string]any{"client_secret": "very-secret"},
			wantKey:   "client_secret",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 4
		{
			name:      "password is redacted",
			input:     map[string]any{"password": "hunter2"},
			wantKey:   "password",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 5 (empty string not redacted)
		{
			name:      "empty string value not redacted",
			input:     map[string]any{"api_key": ""},
			wantKey:   "api_key",
			wantValue: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			redactSensitiveFields(tc.input)
			assert.Equal(t, tc.wantValue, tc.input[tc.wantKey])
		})
	}
}

// TestRedactSensitiveFieldsPreservesNonSensitive verifies safe fields are unchanged.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23)
func TestRedactSensitiveFieldsPreservesNonSensitive(t *testing.T) {
	m := map[string]any{
		"host":    "localhost",
		"port":    8080,
		"api_key": "should-be-gone",
		"version": "1.0.0",
	}
	redactSensitiveFields(m)

	assert.Equal(t, "localhost", m["host"])
	assert.Equal(t, 8080, m["port"])
	assert.Equal(t, "[redacted]", m["api_key"])
	assert.Equal(t, "1.0.0", m["version"])
}

// TestRedactSensitiveFieldsNested verifies nested maps are recursively redacted.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23)
func TestRedactSensitiveFieldsNested(t *testing.T) {
	m := map[string]any{
		"provider": map[string]any{
			"name":    "anthropic",
			"api_key": "sk-nested",
		},
	}
	redactSensitiveFields(m)

	provider, ok := m["provider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "anthropic", provider["name"])
	assert.Equal(t, "[redacted]", provider["api_key"])
}

// ── fix-V endpoint handler response-shape tests ────────────────────────────────

// TestRotateGatewayToken_ResponseShape verifies that POST /config/gateway/rotate-token
// returns a body that unmarshal-cleanly maps to gen.RotateTokenResponse, and that
// the token field is non-empty.
//
// BDD:
//
//	Given a gateway with a writable config.json and a wired reload func,
//	When POST /api/v1/config/gateway/rotate-token is called,
//	Then the response is 200 and the body has a non-empty "token" field matching gen.RotateTokenResponse.
//
// Traces to: rest.go rotateGatewayToken handler — wire shape must match RotateTokenResponse.
func TestRotateGatewayToken_ResponseShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// Wire a no-op reload func so TriggerReload does not fail with
	// "reload not configured" — the handler calls TriggerReload after saving the
	// new token to disk; in unit tests AgentLoop.Run() is never called so the
	// real reload trigger is absent. A no-op reload is acceptable here because
	// the test is focused on the HTTP response shape (wire format), not reload
	// semantics.
	api.agentLoop.SetReloadFunc(func() error { return nil })

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/config/gateway/rotate-token", nil)
	r = withAdminRole(r)

	api.rotateGatewayToken(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"rotate-token must return 200: %s", w.Body.String())

	// The response body must unmarshal into gen.RotateTokenResponse.
	var resp gen.RotateTokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must unmarshal into gen.RotateTokenResponse — wire shape must match contract")

	// Per the BearerToken schema, the token must be exactly 72 chars: "omnipus_" + 64-hex.
	assert.NotEmpty(t, resp.Token, "RotateTokenResponse.Token must be non-empty")
	assert.Len(t, resp.Token, 72,
		"RotateTokenResponse.Token must be 72 chars: omnipus_ + 64 hex chars (BearerToken schema)")
	assert.Regexp(t, `^omnipus_[a-f0-9]{64}$`, resp.Token,
		"RotateTokenResponse.Token must match BearerToken pattern")

	// Differentiation: call twice — tokens must differ (not hardcoded).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/config/gateway/rotate-token", nil)
	r2 = withAdminRole(r2)
	api.rotateGatewayToken(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 gen.RotateTokenResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.NotEqual(t, resp.Token, resp2.Token,
		"rotate-token must produce a different token on each call (not hardcoded)")
}

// TestHandleConfigGET_StripsSeededSkillGrants is spec test 16c (US-4 S6 /
// R2-04): the seeded_skill_grants marker is internal-only bookkeeping — it must
// be absent from the GET /api/v1/config response while config.json on disk
// carries it.
func TestHandleConfigGET_StripsSeededSkillGrants(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Marker present in the live config (as after a real boot's SeedConfig)…
	api.agentLoop.GetConfig().SeededSkillGrants = []string{coreagent.SkillsMigrationDefineDone}
	// …and durably recorded on disk.
	configPath := filepath.Join(api.homePath, "config.json")
	require.NoError(t, persistSeededSkillGrants(configPath,
		[]string{coreagent.SkillsMigrationDefineDone}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, onWire := resp["seeded_skill_grants"]
	assert.False(t, onWire,
		"seeded_skill_grants is internal-only and must be stripped from the config response")

	// The disk file still carries it — the strip is wire-only, not a delete.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	assert.Equal(t, []any{coreagent.SkillsMigrationDefineDone}, onDisk["seeded_skill_grants"],
		"config.json on disk must keep the marker")
}

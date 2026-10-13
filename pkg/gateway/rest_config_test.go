// rest_config_test.go: tests for read and write config.json, credential refs, and service rewiring

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// #1277 founder decision 2026-10-12: one unreadable channel secret must not reject a Settings refresh.
func TestRefreshConfigAndRewireServices_UnreadableChannelCredentialApplies(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-before-tamper"))
	cfg, bundle, _ := f.boot(t)
	api, _, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277TamperSingleEntry(t, f, issue1277BrokenRef)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath),
		"single unreadable secret must not reject an unrelated Settings edit")
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireHealthyProvider(t, api.agentLoop)
	_, errs := credentials.ResolveBundle(api.agentLoop.GetConfig(), f.store)
	var entryErr *credentials.EntryAuthError
	found := false
	for _, err := range errs {
		if errors.As(err, &entryErr) && entryErr.Name == issue1277BrokenRef {
			found = true
		}
	}
	require.True(t, found, "server-side resolution must retain the ref-naming error, never swallow it")
}

// #1277 founder decision 2026-10-12: a missing enabled-channel secret must not reject refresh.
func TestRefreshConfigAndRewireServices_MissingChannelCredentialApplies(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-before-delete"))
	cfg, bundle, _ := f.boot(t)
	api, _, _, _ := f.startRuntime(t, cfg, bundle)
	require.NoError(t, f.store.Delete(issue1277BrokenRef))
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath),
		"a deleted integration secret must not reject an unrelated Settings edit")
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireHealthyProvider(t, api.agentLoop)
	_, err := f.store.Get(issue1277BrokenRef)
	var missing *credentials.NotFoundError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, issue1277BrokenRef, missing.Name)
}

// #1277 founder decision 2026-10-12: an unreadable mailbox secret is one integration, not the store.
func TestRefreshConfigAndRewireServices_UnreadableMailboxCredentialApplies(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-healthy-line"))
	const badRef = "QA1277_MAILBOX_UNREADABLE"
	const healthyRef = "QA1277_MAILBOX_HEALTHY"
	t.Setenv(badRef, "")
	t.Setenv(healthyRef, "")
	require.NoError(t, f.store.Set(badRef, "qa1277-before-tamper"))
	require.NoError(t, f.store.Set(healthyRef, "qa1277-healthy-mailbox"))
	f.cfg.Mailboxes = config.MailboxesConfig{
		"main": {
			"default": {Enabled: true, WorkspaceID: "default", PasswordRef: badRef, IMAPHost: "127.0.0.1", IMAPPort: 993, SMTPHost: "127.0.0.1", Username: "qa1277-bad@example.invalid"},
			"healthy": {Enabled: true, WorkspaceID: "healthy", PasswordRef: healthyRef, IMAPHost: "127.0.0.1", IMAPPort: 993, SMTPHost: "127.0.0.1", Username: "qa1277-healthy@example.invalid"},
		},
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	api, _, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277TamperSingleEntry(t, f, badRef)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath),
		"one unreadable mailbox password must not reject refresh or affect other integrations")
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireHealthyProvider(t, api.agentLoop)
	// Existing mailbox wire contract has no reason field. Pin the real
	// server-side scoped resolution and runtime exclusion; report the gap.
	errs := credentials.InjectFromConfig(api.agentLoop.GetConfig(), f.store)
	require.Len(t, errs, 1)
	var scoped *credentials.CredentialRefError
	require.ErrorAs(t, errs[0], &scoped)
	require.Equal(t, credentials.ScopeMailbox, scoped.Scope)
	require.Equal(t, "main", scoped.Owner)
	require.Equal(t, "default", scoped.SubOwner)
	require.Equal(t, badRef, scoped.Ref)
	require.Contains(t, scoped.Error(), badRef)
	ready := buildMailboxes(api.agentLoop.GetConfig(), f.store, nil, nil)
	require.Len(t, ready, 1, "only the bad mailbox must be excluded from runtime")
	require.Equal(t, "healthy", ready[0].WorkspaceID)
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

// TestDescribeCredentialResolutionError_EntryAuthNamesTheEntry pins the third
// branch of the classifier: a stored entry that does not authenticate under its
// own name (edited on disk, moved from another entry, or written before the AAD
// name binding existed) must name that entry and ask for it to be re-entered —
// never fall through to the "unlock and retry" advice, which is wrong when the
// store is open and only the bytes are at fault.
//
// *credentials.EntryAuthError unwraps to credentials.ErrWrongKey, so this also
// pins the match ORDER: the type must be tested, not the sentinel.
func TestDescribeCredentialResolutionError_EntryAuthNamesTheEntry(t *testing.T) {
	authErr := &credentials.EntryAuthError{Name: "ANTHROPIC_API_KEY"}

	// The wrapped shape the REST path actually sees (resolveCredentialRef wraps
	// the store error with %w).
	wrapped := fmt.Errorf("credential store: %w", authErr)

	msg := describeCredentialResolutionError(wrapped)

	assert.Contains(t, msg, "ANTHROPIC_API_KEY",
		"the operator-facing message must name the entry at fault")
	assert.Contains(t, msg, "re-enter",
		"the correct remediation for an unusable entry is to enter it again")
	assert.NotContains(t, msg, "unlock and retry",
		"unlock-and-retry is the wrong advice when the store is already readable")

	// The two pre-existing branches are unchanged.
	notFoundMsg := describeCredentialResolutionError(
		fmt.Errorf("credential store: %w", &credentials.NotFoundError{Name: "MISSING_REF"}))
	assert.Equal(t,
		"the configured credential reference no longer exists — re-enter the API key.",
		notFoundMsg)

	otherMsg := describeCredentialResolutionError(errors.New("store is locked"))
	assert.Contains(t, otherMsg, "unlock and retry",
		"an unclassified failure keeps the unlock-and-retry advice")
}

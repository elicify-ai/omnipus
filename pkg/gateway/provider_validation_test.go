// Provider-validation gateway integration tests (spec: provider-validation-centralization-spec.md).
// Tests #10–#16, #28–#30 from the TDD plan (gateway layer only).
//
// ADR-067 T067-12 re-keyed the fixture ids here from "testprovider" to
// "my-proxy": an id the registry catalog does not carry is an operator-named
// CUSTOM row (FR-035), and every check in the product is on that row's
// `custom: true` flag, never on a literal id (X-13). These rows therefore
// exercise the custom half of key validation — the one whose probe candidates
// can only come from the live endpoint, because no catalog document describes
// it. The CATALOG half is T39, in rest_probe_catalog_test.go.
//
// Each test drives a real restAPI instance against an httptest upstream stub.
// No live provider calls are made. Credential store is wired with a test master key.
//
// SC-002 baseline: grep after these tests run in CI confirms 0 occurrences of the
// deleted symbols (fetchUpstreamModels / testProviderAuth) in pkg/gateway.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// testMasterKeyValidation is a test-only hex master key for provider validation tests.
// Distinct from testMasterKey in rest_onboarding_test.go to avoid cross-test dependency.
const testMasterKeyValidation = "1112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f30"

// asString is a shared test helper (also used by uat_fixes_test.go) that
// type-asserts a decoded JSON value to string, returning "" for nil/non-string.
func asString(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// newProviderValidationTestAPI creates a restAPI wired with a credential store,
// a live reload function, and a seeded provider config entry so PUT /providers/{id}
// can validate, store, and reload without hitting a 500.
//
// The upstream argument is the httptest server URL; the provider's api_base
// is NOT written to the config (ProviderUpdateRequest has no api_base field —
// the handler resolves the base from the in-memory config or providers.APIBaseFor).
// For a custom/unknown provider ID, providers.APIBaseFor returns "" and ValidateKey
// skips the SSRF check + falls back to Unreachable without a base URL.
//
// To exercise validation against a real httptest upstream, callers should use a
// provider ID that maps to a known default base (e.g. "openrouter") and override
// the mapping, OR seed the config's api_base field in the in-memory provider list.
// This helper seeds api_base in the in-memory ModelConfig so ValidateKey picks it up.
func newProviderValidationTestAPI(t *testing.T, providerID, upstreamBaseURL string) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)

	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	// Pre-seed the in-memory provider list with api_base pointing at the stub server.
	if providerID != "" && upstreamBaseURL != "" {
		cfg.Providers = []*config.ModelConfig{
			{
				Name:     providerID,
				Provider: providerID,
				Model:    "test-model",
				APIBase:  upstreamBaseURL,
			},
		}
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Wire reload so the PUT handler does not return 500. This func runs
	// synchronously (unlike production's async reload pipeline), so it must
	// clear the pending flag itself — otherwise triggerReloadAndWait's
	// IsReloadPending() poll would never observe a clear and every PUT would
	// block for its full 5s timeout (see seedProviderConfig in
	// uat_fixes_test.go for the same fix with more detail).
	al.SetReloadFunc(func() error {
		defer al.ClearReloadPending()
		raw, err := os.ReadFile(tmpDir + "/config.json")
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		// Re-seed the in-memory provider list preserving the api_base.
		list, _ := m["providers"].([]any)
		c := al.GetConfig()
		c.Providers = c.Providers[:0]
		for _, item := range list {
			e, ok := item.(map[string]any)
			if !ok {
				continue
			}
			mc := &config.ModelConfig{
				Name:     asString(e["model_name"]),
				Provider: asString(e["provider"]),
				Model:    asString(e["model"]),
				APIBase:  upstreamBaseURL, // preserve the test stub base
			}
			c.Providers = append(c.Providers, mc)
		}
		return nil
	})

	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	if err := credentials.Unlock(credStore); err != nil {
		t.Fatalf("unlock credential store: %v", err)
	}

	// Seed the provider into config.json so the PUT handler's "found" check passes.
	err := writeMinimalProviderConfig(t, tmpDir, providerID)
	require.NoError(t, err, "seed config.json with provider entry")

	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}
}

// writeMinimalProviderConfig writes a config.json that includes a pre-seeded
// provider entry (no api_key, just enough for the PUT "found" check to pass).
func writeMinimalProviderConfig(t *testing.T, tmpDir, providerID string) error {
	t.Helper()
	entry := map[string]any{
		"model_name": providerID,
		"provider":   providerID,
		"model":      "test-model",
	}
	cfg := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{entry},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(tmpDir+"/config.json", b, 0o600)
}

// doPutProvider sends PUT /api/v1/providers/{id} with the given JSON body and
// returns the response recorder. The request has no user in context (bypasses
// re-auth gate since onboarding is not yet complete).
func doPutProvider(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/providers/" + id
	api.HandleProviders(w, r)
	return w
}

// doProviderTest sends POST /api/v1/providers/{id}/test.
func doProviderTest(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/providers/"+id+"/test", nil)
	r.URL.Path = "/api/v1/providers/" + id + "/test"
	api.HandleProviders(w, r)
	return w
}

// --- Test #12: PUT /providers/{id} — InvalidKey returns 422 and does NOT persist ---

// TestPutProvider_InvalidKey422NotPersisted asserts that PUT /providers/{id} with a
// key that is classified as InvalidKey (401 from the upstream) returns HTTP 422 and
// leaves the credential store byte-identical (SC-004 / FR-011 / spec Test #12).
func TestPutProvider_InvalidKey422NotPersisted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"No auth credentials found"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

	// Record credential store state before the PUT.
	credPathBefore := api.credentialsStorePath()
	beforeStat, statErr := os.Stat(credPathBefore)
	beforeSize := int64(0)
	if statErr == nil {
		beforeSize = beforeStat.Size()
	}

	body := `{"api_key":"bad-key-401"}`
	w := doPutProvider(t, api, "my-proxy", body)

	// Must return 422 (InvalidKey blocks save).
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"InvalidKey must return 422; body=%s", w.Body.String())

	// Error field must contain the curated message (SEC-16: not the raw upstream body).
	var errResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	errMsg, _ := errResp["error"].(string)
	assert.Contains(t, errMsg, "rejected",
		"422 error must state the key was rejected (curated FR-7 message)")
	assert.NotContains(t, errMsg, "bad-key-401",
		"SEC-16: 422 error must not contain the API key")
	assert.NotContains(t, errMsg, "No auth credentials found",
		"SEC-16: 422 error must not echo the raw upstream body")

	// Credential store must be unchanged (key was NOT persisted).
	afterStat, afterStatErr := os.Stat(credPathBefore)
	if statErr != nil && afterStatErr != nil {
		// Store did not exist before and does not exist after — correct.
		return
	}
	if afterStatErr == nil {
		assert.Equal(t, beforeSize, afterStat.Size(),
			"credential store must be unchanged after a 422 rejection (SC-004)")
	}
}

// --- Test #13: PUT /providers/{id} — NoCredit key saves with 200 + validation ---

// TestPutProvider_NoCredit200Persisted asserts that PUT with a no-credit key (402)
// returns HTTP 200 with validation.outcome=no_credit and the key IS persisted.
// (spec Test #13 / FR-011 / US6.2)
func TestPutProvider_NoCredit200Persisted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			// 402 = NoCredit — key is valid but account has no funds.
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

	body := `{"api_key":"valid-key-no-credit"}`
	w := doPutProvider(t, api, "my-proxy", body)

	// Must return 200 (NoCredit proceeds).
	require.Equal(t, http.StatusOK, w.Code,
		"NoCredit must return 200; body=%s", w.Body.String())

	var provider gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &provider))
	assert.Equal(t, "my-proxy", provider.Id)
	assert.Equal(t, gen.ProviderStatusConnected, provider.Status,
		"provider must be connected (key was persisted)")

	// validation object must be present with no_credit outcome (R-B / FR-013).
	require.NotNil(t, provider.Validation, "validation must be populated for NoCredit")
	assert.Equal(t, gen.ProviderValidationOutcome("no_credit"), provider.Validation.Outcome,
		"validation.outcome must be no_credit")
	require.NotNil(t, provider.Validation.Message,
		"validation.message must be present for non-valid outcome")
	assert.Contains(t, *provider.Validation.Message, "credit",
		"validation.message must describe the no-credit condition")
	assert.NotContains(t, *provider.Validation.Message, "valid-key-no-credit",
		"SEC-16: validation.message must not contain the API key")

	// The key must have been persisted (credential store written).
	credPath := api.credentialsStorePath()
	_, err := os.Stat(credPath)
	assert.NoError(t, err, "credential store must exist after a successful 200 save")
}

// --- Test #14: PUT that omits api_key does not fire any probe ---

// TestPutProvider_KeyUnchangedNoProbe asserts that a PUT whose body omits api_key
// (or sends it empty) fires zero /chat/completions requests (SC-005 / FR-011 / US6.3).
func TestPutProvider_KeyUnchangedNoProbe(t *testing.T) {
	var completionHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			completionHits.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

	// PUT changing only the model (no api_key in body).
	body := `{"model":"other-model"}`
	w := doPutProvider(t, api, "my-proxy", body)

	assert.Equal(t, http.StatusOK, w.Code,
		"model-only edit must succeed; body=%s", w.Body.String())
	assert.Equal(t, int64(0), completionHits.Load(),
		"SC-005: zero /chat/completions probes when api_key is absent from the PUT body")

	// Response must carry no validation field (no probe was run).
	var provider gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &provider))
	assert.Nil(t, provider.Validation,
		"validation must be absent when no key was submitted (no probe ran)")
}

// --- Test #15: PUT with SSRF-blocked persisted api_base is rejected ---

// TestPutProvider_SSRFPersistedApiBase asserts that when the in-memory provider entry
// carries a loopback api_base, the PUT handler rejects the save before probing.
// (FR-012 / US6.4 / spec Test #15)
func TestPutProvider_SSRFPersistedApiBase(t *testing.T) {
	// Seed an in-memory provider with a loopback api_base.
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
	tmpDir := t.TempDir()
	minimalCfg := []byte(
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[{"model_name":"my-proxy","provider":"my-proxy","model":"test-model"}]}`,
	)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		// Provider with a loopback api_base that the SSRF checker must block.
		Providers: []*config.ModelConfig{
			{Name: "my-proxy", Provider: "my-proxy", Model: "test-model", APIBase: "http://127.0.0.1:9"},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	al.SetReloadFunc(func() error { return nil })

	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))

	// Wire a real SSRF checker — it must block loopback addresses.
	ssrfChecker := security.NewSSRFChecker(nil)

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
		ssrfChecker:   ssrfChecker,
	}

	// PUT with a new api_key — SSRF check on the persisted 127.0.0.1 base should block.
	body := `{"api_key":"sk-test-key"}`
	w := doPutProvider(t, api, "my-proxy", body)

	// SSRF rejection returns 422 (the persisted api_base is loopback — blocked pre-probe).
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"loopback persisted api_base must be SSRF-blocked; body=%s", w.Body.String())
	// Assert the response body names the SSRF cause, not a generic validation error.
	var errResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	errMsg, _ := errResp["error"].(string)
	assert.Contains(t, strings.ToLower(errMsg), "ssrf",
		"SSRF-rejected 422 body must contain 'ssrf' to distinguish it from a key-invalid 422; got: %q", errMsg)
}

// --- Test #16: Settings Test returns classified outcome ---

// TestProviderTest_ClassifiedOutcome asserts that POST /providers/{id}/test
// returns an OperationResult carrying the classified validation field when the
// key probe returns a non-valid outcome. (FR-013 / US1.3 / spec Test #16)
func TestProviderTest_ClassifiedOutcome(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			// 402 = NoCredit — key works but no funds.
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
	tmpDir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			tmpDir+"/config.json",
			[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`),
			0o600,
		),
	)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Providers: []*config.ModelConfig{
			{Name: "my-proxy", Provider: "my-proxy", Model: "test-model", APIBase: upstream.URL},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))
	// Pre-store a credential so the Test handler can resolve the API key.
	require.NoError(t, credStore.Set("my-proxy_API_KEY", "valid-key-no-credit"))
	// Write the api_key_ref into config.json for the Test handler to pick up.
	cfgJSON := map[string]any{
		"version": 1,
		"agents":  map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{
			map[string]any{
				"model_name":  "my-proxy",
				"provider":    "my-proxy",
				"model":       "test-model",
				"api_key_ref": "my-proxy_API_KEY",
				"api_base":    upstream.URL,
			},
		},
	}
	b, err := json.Marshal(cfgJSON)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", b, 0o600))

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	w := doProviderTest(t, api, "my-proxy")

	require.Equal(t, http.StatusOK, w.Code, "provider test must return 200; body=%s", w.Body.String())

	var result gen.OperationResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	// NoCredit key — test reports success=true (key is valid) with validation.
	assert.True(t, result.Success,
		"NoCredit key must report success=true (key is valid, not a wrong key)")
	require.NotNil(t, result.Validation, "validation must be present for a probed outcome")
	assert.Equal(t, gen.OperationResultValidationOutcome("no_credit"), result.Validation.Outcome,
		"validation.outcome must be no_credit")
	require.NotNil(t, result.Validation.Message, "validation.message must be present")
	assert.Contains(t, *result.Validation.Message, "credit",
		"validation.message must describe the no-credit condition")
}

// --- Test: Settings-Test never persists credentials ---

// TestProviderTest_DoesNotPersistCredential asserts that POST /providers/{id}/test
// (the Settings Test) is informational only and does NOT modify the credential store.
// (O2 / spec: only persisting PUT flows write to the store)
func TestProviderTest_DoesNotPersistCredential(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Providers: []*config.ModelConfig{
			{Name: "my-proxy", Provider: "my-proxy", Model: "test-model", APIBase: upstream.URL},
		},
	}
	require.NoError(t, os.WriteFile(tmpDir+"/config.json",
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))
	// Pre-store a credential so the Test handler can resolve the API key.
	require.NoError(t, credStore.Set("my-proxy_API_KEY", "existing-key"))
	// Write api_key_ref into config.json for the Test handler.
	cfgJSON := map[string]any{
		"version": 1,
		"agents":  map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{map[string]any{
			"model_name":  "my-proxy",
			"provider":    "my-proxy",
			"model":       "test-model",
			"api_key_ref": "my-proxy_API_KEY",
			"api_base":    upstream.URL,
		}},
	}
	b, err := json.Marshal(cfgJSON)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", b, 0o600))

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	// Record credential store state before the test.
	credPathBefore := api.credentialsStorePath()
	beforeStat, err := os.Stat(credPathBefore)
	require.NoError(t, err, "credential store must exist before the test")
	beforeMtime := beforeStat.ModTime()

	w := doProviderTest(t, api, "my-proxy")
	require.Equal(t, http.StatusOK, w.Code, "provider test must return 200; body=%s", w.Body.String())

	// Credential store must NOT have been modified.
	afterStat, err := os.Stat(credPathBefore)
	require.NoError(t, err, "credential store must still exist after the test")
	assert.Equal(t, beforeMtime, afterStat.ModTime(),
		"credential store must be unchanged after a Settings Test (informational only)")
}

// --- Test: Probe and Test are not audited (O2) ---

// TestAudit_ProbeAndTestNotAudited asserts that neither the onboarding probe
// (HandleOnboardingProbeProvider) nor the Settings Test (POST /providers/{id}/test)
// emit a provider_key_validated audit entry — only the persisting PUT flow audits.
// (O2 / spec Test #10-bis)
func TestAudit_ProbeAndTestNotAudited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		MaxSizeBytes:  1 << 20,
		RetentionDays: 1,
	})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = auditLogger.Close() })

	// --- Sub-test A: onboarding probe does not audit ---
	t.Run("onboarding_probe", func(t *testing.T) {
		tmpDir := t.TempDir()
		api := newOnboardingTestAPI(t, tmpDir, nil)
		api.auditor = auditLogger

		body := `{"id":"openai","auth":"api_key","api_key":"valid-key","api_base":"` + upstream.URL + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleOnboardingProbeProvider(w, withFreshInstallConfig(req))
		require.Equal(t, http.StatusOK, w.Code)
	})

	// --- Sub-test B: Settings Test does not audit ---
	t.Run("settings_test", func(t *testing.T) {
		t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
		tmpDir := t.TempDir()
		cfg := &config.Config{
			Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			},
			Providers: []*config.ModelConfig{
				{Name: "my-proxy-2", Provider: "my-proxy-2", Model: "test-model", APIBase: upstream.URL},
			},
		}
		msgBus := bus.NewMessageBus()
		al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
		credStore := credentials.NewStore(tmpDir + "/credentials.json")
		require.NoError(t, credentials.Unlock(credStore))
		require.NoError(t, credStore.Set("my-proxy-2_API_KEY", "valid-key"))
		cfgJSON := map[string]any{
			"version": 1,
			"agents":  map[string]any{"defaults": map[string]any{}, "list": []any{}},
			"providers": []any{map[string]any{
				"model_name":  "my-proxy-2",
				"provider":    "my-proxy-2",
				"model":       "test-model",
				"api_key_ref": "my-proxy-2_API_KEY",
				"api_base":    upstream.URL,
			}},
		}
		b, err := json.Marshal(cfgJSON)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(tmpDir+"/config.json", b, 0o600))

		api := &restAPI{
			agentLoop:     al,
			homePath:      tmpDir,
			allowedOrigin: "http://localhost:3000",
			onboardingMgr: onboarding.NewManager(tmpDir),
			taskStore:     task.New(tmpDir + "/tasks"),
			credStore:     credStore,
			auditor:       auditLogger,
		}

		w := doProviderTest(t, api, "my-proxy-2")
		require.Equal(t, http.StatusOK, w.Code, "settings test must return 200; body=%s", w.Body.String())
	})

	// Flush and read audit log — neither probe nor test must have emitted provider_key_validated.
	_ = auditLogger.Close()
	entries := readAuditLog(t, auditDir)
	for _, line := range entries {
		assert.NotEqual(t, "provider_key_validated", line["event"],
			"O2: provider_key_validated must NOT be audited for probe or Settings Test; got entry: %v", line)
	}
}

// --- Test #28: PUT re-sending the same key value re-probes; omitting key → 0 probes ---

// TestPutProvider_SameKeyReprobes asserts that re-sending the same key value fires
// exactly 1 probe (R-C: "changed" = non-empty key in the body, not a plaintext diff)
// while a PUT omitting api_key fires 0 probes (SC-005 / SC-012).
func TestPutProvider_SameKeyReprobes(t *testing.T) {
	var completionHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			completionHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

	// First PUT: submit a key (fires 1 probe — valid response).
	w1 := doPutProvider(t, api, "my-proxy", `{"api_key":"same-key"}`)
	require.Equal(t, http.StatusOK, w1.Code, "first PUT must succeed; body=%s", w1.Body.String())
	assert.Equal(t, int64(1), completionHits.Load(), "first PUT must fire exactly 1 probe")

	// Second PUT: re-submit the same key (must fire 1 more probe — R-C).
	completionHits.Store(0)
	w2 := doPutProvider(t, api, "my-proxy", `{"api_key":"same-key"}`)
	require.Equal(t, http.StatusOK, w2.Code, "re-submitting same key must succeed; body=%s", w2.Body.String())
	assert.Equal(t, int64(1), completionHits.Load(),
		"SC-012: re-sending the same key value must re-probe (not skip)")

	// Third PUT: omit api_key (model-only) — must fire 0 probes.
	completionHits.Store(0)
	w3 := doPutProvider(t, api, "my-proxy", `{"model":"other-model"}`)
	require.Equal(t, http.StatusOK, w3.Code, "model-only PUT must succeed; body=%s", w3.Body.String())
	assert.Equal(t, int64(0), completionHits.Load(),
		"SC-005: omitting api_key must fire 0 probes")
}

// --- Test #29: Store-locked → 503 (persist nothing); reload-fail → 500 (key persisted) ---

// TestPutProvider_StoreLockedAndReloadFail covers R-D/M3 error paths:
//   - A locked credential store returns 503 and persists nothing (validation passes).
//   - A reload failure returns 500 but the key has already been persisted.
//
// (spec Test #29)
func TestPutProvider_StoreLockedAndReloadFail(t *testing.T) {
	// Sub-test A: locked credential store.
	t.Run("store_locked_503", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// All probes return 200 (valid key) — so validation passes.
			if strings.HasSuffix(r.URL.Path, "/models") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
		defer upstream.Close()

		// Ensure no master-key env vars are set for this sub-test.
		t.Setenv("OMNIPUS_MASTER_KEY", "")
		t.Setenv("OMNIPUS_KEY_FILE", "")
		tmpDir := t.TempDir()
		require.NoError(
			t,
			os.WriteFile(
				tmpDir+"/config.json",
				[]byte(
					`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[{"model_name":"my-proxy","provider":"my-proxy","model":"test-model"}]}`,
				),
				0o600,
			),
		)

		// Pre-create a credentials.json stub so Mode-4 auto-generate does NOT fire.
		// With no master key, no key file, and a pre-existing credentials.json, the
		// credentials.Unlock function hits Mode 5 (TTY prompt) and returns an error
		// when stdin is not a TTY (as in test runners). This makes storeCredential
		// return a locked error → handler returns 503.
		require.NoError(t, os.WriteFile(tmpDir+"/credentials.json", []byte(`{"salt":""}`), 0o600))

		cfg := &config.Config{
			Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			},
			Providers: []*config.ModelConfig{
				{Name: "my-proxy", Provider: "my-proxy", Model: "test-model", APIBase: upstream.URL},
			},
		}
		msgBus := bus.NewMessageBus()
		al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
		al.SetReloadFunc(func() error { return nil })

		api := &restAPI{
			agentLoop:     al,
			homePath:      tmpDir,
			allowedOrigin: "http://localhost:3000",
			onboardingMgr: onboarding.NewManager(tmpDir),
			taskStore:     task.New(tmpDir + "/tasks"),
			// credStore intentionally nil — storeCredential auto-unlocks via Unlock(),
			// which fails in this scenario (no key, no TTY) and returns an error → 503.
		}

		w := doPutProvider(t, api, "my-proxy", `{"api_key":"valid-key"}`)
		// With no master key and a pre-existing credentials.json, Unlock fails → 503.
		assert.Equal(t, http.StatusServiceUnavailable, w.Code,
			"locked credential store must return 503; body=%s", w.Body.String())
	})

	// Sub-test B: TriggerReload failure returns 500 but key was persisted.
	t.Run("reload_fail_500_key_persisted", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/models") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
		defer upstream.Close()

		api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

		// Override reload to fail.
		api.agentLoop.SetReloadFunc(func() error {
			return os.ErrNotExist // simulated reload failure
		})

		w := doPutProvider(t, api, "my-proxy", `{"api_key":"valid-key"}`)
		// Reload failure returns 500 (existing behavior — key has been persisted).
		assert.Equal(t, http.StatusInternalServerError, w.Code,
			"reload failure must return 500; body=%s", w.Body.String())

		// The key must have been persisted (storeCredential ran before TriggerReload).
		credPath := api.credentialsStorePath()
		_, err := os.Stat(credPath)
		assert.NoError(t, err, "credential store must exist — key was persisted before reload failed")
	})
}

// --- Test #30: 422 burns the single-use re-auth token ---

// TestPutProvider_ReauthTokenBurnedOn422 verifies that a 422 rejection consumes the
// single-use re-auth consent token, so a retry without a fresh token is rejected
// at the re-auth gate (R-D/M4 / spec Test #30).
//
// Approach: the re-auth gate fires only when a user IS in the request context
// (UserContextKey{}). We use withReAuthAdmin to mint a real token AND seed the
// user, then submit a PUT with a bad key. After the 422, the token must be
// consumed. A subsequent PUT with the SAME (now-stale) token is rejected at
// the re-auth gate (403), NOT at the validation layer.
func TestPutProvider_ReauthTokenBurnedOn422(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		// Upstream returns 401 — key is invalid.
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"No auth credentials found"}}`))
	}))
	defer upstream.Close()

	api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

	// Step 1: mint a single-use re-auth token directly from the store and inject
	// it alongside an admin user in the request context (mirrors withReAuthAdmin
	// from reauth_gate_test.go). This activates the re-auth gate in the PUT handler.
	token, err := api.reauthStoreOrInit().mint(reauthGateAdminUser)
	require.NoError(t, err, "minting a re-auth consent token must not fail")

	body := `{"api_key":"bad-key"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/providers/my-proxy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(reAuthHeader, token)
	req = withReAuthAdminNoToken(req) // sets the user context (token is in the header above)
	// Replace the header-set token with our minted one (withReAuthAdminNoToken doesn't mint).
	req.Header.Set(reAuthHeader, token)
	req.URL.Path = "/api/v1/providers/my-proxy"

	w1 := httptest.NewRecorder()
	api.HandleProviders(w1, req)

	// Step 2: first request must return 422 (InvalidKey blocks save).
	assert.Equal(t, http.StatusUnprocessableEntity, w1.Code,
		"InvalidKey must be 422; body=%s", w1.Body.String())

	// Step 3: assert the token was consumed — a direct store.consume call with the
	// same token must now return false (single-use: consumed on the first attempt).
	alreadyConsumed := api.reauthStoreOrInit().consume(token, reauthGateAdminUser)
	assert.False(t, alreadyConsumed,
		"re-auth token must have been consumed by the 422 handler; a second consume must fail (single-use)")

	// Step 4: retry the PUT with the stale (now-deleted) token — must be rejected
	// at the re-auth gate (403 Forbidden), not at the validation layer (422).
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/providers/my-proxy", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(reAuthHeader, token) // same stale token
	req2 = withReAuthAdminNoToken(req2)
	req2.Header.Set(reAuthHeader, token)
	req2.URL.Path = "/api/v1/providers/my-proxy"
	w2 := httptest.NewRecorder()
	api.HandleProviders(w2, req2)
	assert.Equal(t, http.StatusForbidden, w2.Code,
		"retry with stale token must be rejected at the re-auth gate (403); body=%s", w2.Body.String())
}

// --- Test #10: Onboarding probe — bad key blocks (SC-003 regression) ---
// NOTE: covered by TestHandleOnboardingProbeProvider_PublicModelsBadKey in rest_onboarding_test.go.

// --- Test #11: Onboarding probe — no-credit warns ---

// TestOnboardingProbe_NoCreditWarns asserts that when the upstream returns 402
// (no credit), the onboarding probe returns success=true with validation.outcome=no_credit.
// (spec Test #11 / US3.2 / FR-013)
func TestOnboardingProbe_NoCreditWarns(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	require.False(t, api.onboardingMgr.IsComplete())

	body := `{"id":"openai","auth":"api_key","api_key":"valid-key","api_base":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleOnboardingProbeProvider(w, withFreshInstallConfig(req))

	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.ProbeProviderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// NoCredit proceeds — success=true.
	assert.True(t, resp.Success,
		"NoCredit key must produce success=true (key is valid, just no funds)")
	// validation field must carry the classified outcome (R-B / FR-013).
	require.NotNil(t, resp.Validation, "validation must be present")
	assert.Equal(t, gen.ProbeProviderResponseValidationOutcome("no_credit"), resp.Validation.Outcome,
		"validation.outcome must be no_credit")
	require.NotNil(t, resp.Validation.Message, "validation.message must be present")
	assert.Contains(t, *resp.Validation.Message, "credit",
		"validation.message must describe the no-credit condition")
}

// --- Audit test: warning-proceed emits provider_key_validated ---

// TestAudit_WarningProceedNoSecret asserts that a PUT producing a warning outcome
// (NoCredit) emits an audit entry with provider/outcome/action and no API key.
// (spec Test #19 / FR-017 / US9.1)
func TestAudit_WarningProceedNoSecret(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
	}))
	defer upstream.Close()

	api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)

	// Wire a file-backed audit logger so we can inspect emitted entries.
	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		MaxSizeBytes:  1 << 20,
		RetentionDays: 1,
	})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = auditLogger.Close() })
	api.auditor = auditLogger

	w := doPutProvider(t, api, "my-proxy", `{"api_key":"no-credit-key"}`)
	require.Equal(t, http.StatusOK, w.Code, "NoCredit PUT must return 200; body=%s", w.Body.String())

	// Flush and read audit log.
	_ = auditLogger.Close()
	entries := readAuditLog(t, auditDir)
	require.NotEmpty(t, entries, "audit log must contain at least one entry")

	var found bool
	for _, line := range entries {
		if line["event"] == "provider_key_validated" {
			found = true
			details, _ := line["details"].(map[string]any)
			assert.Equal(t, "my-proxy", details["provider"],
				"audit entry must name the provider")
			assert.Equal(t, "no_credit", details["outcome"],
				"audit entry must record the outcome")
			assert.Equal(t, "proceeded", details["action"],
				"audit entry must record action=proceeded")
			// SEC-16: audit entry must not contain the key.
			lineJSON, err := json.Marshal(line)
			require.NoError(t, err)
			assert.NotContains(t, string(lineJSON), "no-credit-key",
				"SEC-16: audit entry must not contain the API key")
			break
		}
	}
	assert.True(t, found, "provider_key_validated event must be present in audit log")
}

// readAuditLog reads all JSONL lines from the most recent file in the audit dir.
func readAuditLog(t *testing.T, dir string) []map[string]any {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read audit dir: %v", err)
	}
	var lines []map[string]any
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(dir + "/" + entry.Name())
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				lines = append(lines, m)
			}
		}
	}
	return lines
}

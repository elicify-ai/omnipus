// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_signin_settings_gap_test.go closes two verified backend gaps that
// made Settings-side provider sign-in a dead end (found by the SPA sign-in
// agent, reproduced against feat/context-budget-and-tool-result-routing):
//
//   - GAP 1: GET /api/v1/providers never wired a configured sign_in row's
//     real status/account_label to T068-14's sign-in status computation —
//     it always fell back to the key-derived "disconnected". The fix must
//     use ONLY cheap, local checks (no vendor fan-out) — computing
//     github-copilot's real sign-in state costs a PREMIUM vendor request,
//     and that cost belongs exclusively to the explicit "Check sign-in"
//     action (rest_signin_copilot.go), never a background list render.
//   - GAP 2: PUT /api/v1/providers/{id} never read req.AuthMethod at all,
//     so a brand-new sign_in-only row (no api_key) hard-rejected with 422
//     "api_key is required" — a signed-in ChatGPT session could never
//     materialize a provider row for Settings to display.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// --- GAP 1: GET /providers sign-in status wiring ------------------------

// TestListProviders_SignInRow_SignedIn proves a configured sign_in row
// (device_code shape: openai-chatgpt) with a fresh, unexpired stored OAuth
// entry is reported as signed_in with its account label — the exact SPA row
// state ("Signed in as <label>" + Sign out) that could never fire before
// this fix (status was always derived from api-key presence, which a
// sign_in row never has).
func TestListProviders_SignInRow_SignedIn(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	blob, err := json.Marshal(map[string]any{
		"access_token": "tok1", "account_id": "user@example.com",
		"expires_at": "2099-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(blob)))

	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "openai-chatgpt", Model: "gpt-5", AuthMethod: config.AuthMethodSignIn,
	})

	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	p := provs[0]
	assert.Equal(t, gen.ProviderStatusSignedIn, p.Status,
		"a configured sign_in row with a live stored OAuth entry must report signed_in, not the key-derived disconnected")
	require.NotNil(t, p.AccountLabel, "account_label must be populated from the stored OAuth entry")
	assert.Equal(t, "user@example.com", *p.AccountLabel)
}

// TestListProviders_SignInRow_Expired proves the same wiring for an expired
// stored entry — the SPA's other real row state ("Session expired" + Sign
// in again).
func TestListProviders_SignInRow_Expired(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	blob, err := json.Marshal(map[string]any{
		"access_token": "tok1", "account_id": "user@example.com",
		"expires_at": "2000-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(blob)))

	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "openai-chatgpt", Model: "gpt-5", AuthMethod: config.AuthMethodSignIn,
	})

	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	assert.Equal(t, gen.ProviderStatusExpired, provs[0].Status)
}

// TestListProviders_NoStoredEntry_StaysDisconnected proves a sign_in row
// with nothing stored yet still reports the plain disconnected default —
// the fix must not invent a state for a provider that was never signed in.
func TestListProviders_NoStoredEntry_StaysDisconnected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "openai-chatgpt", Model: "gpt-5", AuthMethod: config.AuthMethodSignIn,
	})

	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	assert.Equal(t, gen.ProviderStatusDisconnected, provs[0].Status)
	assert.Nil(t, provs[0].AccountLabel)
}

// TestListProviders_NoCopilotVendorFanOut is the CRITICAL DESIGN CONSTRAINT
// assertion: computing github-copilot's real sign-in state runs the vendor
// CLI and spends one premium request against the operator's subscription
// (rest_signin_copilot.go's handleCopilotSignInStatus). GET /providers must
// NEVER pay that cost for a background list render. A fake `copilot` binary
// on PATH leaves a sentinel file behind if it is ever invoked; the test
// fails if that file exists after the GET.
func TestListProviders_NoCopilotVendorFanOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI uses a #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
	api := newTestRestAPIWithHome(t)

	binDir := t.TempDir()
	sentinel := filepath.Join(binDir, "invoked")
	script := "#!/bin/bash\ntouch '" + sentinel + "'\necho ok\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "copilot"), []byte(script), 0o755))
	t.Setenv("PATH", binDir)

	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "github-copilot", Model: "default", AuthMethod: config.AuthMethodSignIn,
	})

	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	// The CLI is (fake-)available and never signed in via any local, costless
	// check github-copilot supports, so the row stays at its disconnected
	// default — never signed_in/expired, and never by running the CLI.
	assert.Equal(t, gen.ProviderStatusDisconnected, provs[0].Status)
	assert.Nil(t, provs[0].AccountLabel)

	_, statErr := os.Stat(sentinel)
	assert.True(t, os.IsNotExist(statErr),
		"GET /providers must never invoke the Copilot CLI — sentinel file exists, meaning it was invoked")
}

// --- GAP 2: PUT /providers/{id} auth_method: sign_in wiring --------------

// TestPutProvider_SignIn_NoAPIKey_Persists proves a brand-new sign_in-only
// provider row (auth_method: sign_in, no api_key) is accepted with 200 and
// actually persisted — round-tripped through config.json, not just the
// in-memory config, and readable back via GET.
func TestPutProvider_SignIn_NoAPIKey_Persists(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := putProviderRaw(t, api, "openai-chatgpt", `{"auth_method":"sign_in"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, gen.ProviderAuthMethodSignIn, resp.AuthMethod,
		"the PUT response must echo the sign_in auth method, not the hardcoded api_key default")

	// Persisted in the in-memory config...
	cfg := api.agentLoop.GetConfig()
	found := false
	for _, m := range cfg.Providers {
		if m.Provider == "openai-chatgpt" {
			found = true
			assert.Equal(t, config.AuthMethodSignIn, m.AuthMethod)
		}
	}
	assert.True(t, found, "the sign_in row must be persisted into cfg.Providers")

	// ...AND on disk in config.json (the actual write path this gap never reached).
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	provsRaw, _ := onDisk["providers"].([]any)
	diskFound := false
	for _, pr := range provsRaw {
		pm, _ := pr.(map[string]any)
		if pm["provider"] == "openai-chatgpt" {
			diskFound = true
			assert.Equal(t, "sign_in", pm["auth_method"])
		}
	}
	assert.True(t, diskFound, "the sign_in row must be persisted to config.json on disk")

	// And readable back via GET.
	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	assert.Equal(t, gen.ProviderAuthMethodSignIn, provs[0].AuthMethod)
}

// TestPutProvider_SignIn_WithAPIKey_Rejected proves sign_in combined with
// api_key is refused — the two are mutually exclusive per
// ProviderUpdateRequest.yaml — and that nothing is persisted.
func TestPutProvider_SignIn_WithAPIKey_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := putProviderRaw(t, api, "openai-chatgpt", `{"auth_method":"sign_in","api_key":"sk-should-not-work"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	cfg := api.agentLoop.GetConfig()
	for _, m := range cfg.Providers {
		assert.NotEqual(t, "openai-chatgpt", m.Provider, "a rejected combo must persist nothing")
	}
}

// TestPutProvider_SignIn_UnsupportedProvider_Rejected proves sign_in is
// refused for a provider whose catalog row does not offer it (or, absent a
// catalog document, isn't in the small built-in sign-in set) — and that
// nothing is persisted.
func TestPutProvider_SignIn_UnsupportedProvider_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := putProviderRaw(t, api, "openrouter", `{"auth_method":"sign_in"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	cfg := api.agentLoop.GetConfig()
	for _, m := range cfg.Providers {
		assert.NotEqual(t, "openrouter", m.Provider, "a rejected sign_in row must persist nothing")
	}
}

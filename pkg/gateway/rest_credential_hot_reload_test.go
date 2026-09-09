// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for UAT full-tool-catalog batch3 2026-09-02, finding #6
// (report §2 "Anything that got through", item 6, "near-miss"): configuring a
// provider under a test API key silently clobbered the same credential ref a
// real, working provider config depended on, and — separately — a plain
// `credentials set` (POST /api/v1/credentials) alone did NOT take effect
// live. ModelConfig.APIKey() (pkg/config/config.go) resolves a provider's key
// via os.Getenv(APIKeyRef), a value only ever (re-)populated by
// credentials.InjectFromConfig, which previously ran ONLY at boot and at an
// explicit /reload. A credential-vault write that changed the plaintext
// behind an already-referenced ref left the OLD value live in-process until
// some UNRELATED config write happened to trigger a reload — a stale-
// credential window the tester had to close by hand with a forced /reload.
//
// This file proves the fix: setCredential (pkg/gateway/rest_settings.go) now
// triggers and WAITS for a reload the same way HandleProviders' PUT branch
// does, and that reload's executeReload step re-runs
// credentials.InjectFromConfig — so a plain credential POST alone makes the
// new value observable via os.Getenv(ref) by the time the handler returns,
// no separate /reload call required.
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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSetCredential_HotReloadsProviderAPIKeyWithoutExplicitReloadCall drives
// the REAL production reload pipeline end to end — setupAndStartServices'
// reloadTrigger/manualReloadChan, the same manual-reload consumer-loop shape
// RunContextWithOptions installs (see reload_coalescing_test.go for the
// identical pattern), and the real executeReload (not a substitute, so
// credentials.InjectFromConfig genuinely runs) — then exercises POST
// /api/v1/credentials through the real HTTP handler and asserts the new
// value is already live in the process environment the instant the handler
// returns, with no separate /reload call anywhere in the test.
func TestSetCredential_HotReloadsProviderAPIKeyWithoutExplicitReloadCall(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	const ref = "TESTPROV_HOTRELOAD_API_KEY"

	credStore := newUnlockedStore(t, tmpDir)
	require.NoError(t, credStore.Set(ref, "old-value"))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir,
				// Deliberately zero (IsZero()==true): with allowEmptyStartup=true
				// (passed below), createStartupProvider's reload path then takes
				// the clean "no default model configured" limited-mode branch
				// (returns a startupBlockedProvider, nil error) instead of
				// providers.CreateProvider's unrelated "default model not found"
				// error — this test's fixture has no real, catalog-resolvable
				// provider, and that failure path is not what's under test here.
				// A non-zero, resolvable DefaultModel is unnecessary: the
				// credential-injection step under test (executeReload's
				// credentials.InjectFromConfig call) runs BEFORE
				// createStartupProvider regardless of which branch that takes.
				DefaultModel: config.DefaultModel{},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{Provider: "testprov", Model: "testprov", APIKeyRef: ref},
		},
	}
	// Simulate the boot-time injection bootCredentials performs BEFORE
	// setupAndStartServices runs (RunContextWithOptions calls them in that
	// order) — setupAndStartServices itself does not inject provider
	// credentials, only executeReload (on a later reload) does.
	require.Empty(t, credentials.InjectFromConfig(cfg, credStore))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	builtinReg := tools.NewBuiltinRegistry()
	mcpReg := tools.NewMCPRegistry()
	provider := providers.LLMProvider(&restMockProvider{})

	rs, err := setupAndStartServices(
		context.Background(),
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		credStore,
		&SandboxApplyResult{},
		builtinReg,
		mcpReg,
		false, // allowGodMode
	)
	require.NoError(t, err, "setupAndStartServices must boot cleanly")
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })

	al.SetReloadFunc(rs.reloadTrigger)

	// Manual-reload consumer loop — mirrors RunContextWithOptions' own
	// manualReloadChan arm verbatim (reload_coalescing_test.go's harness uses
	// the identical shape). runOneReload calls the REAL executeReload, not a
	// stand-in — the credential-injection step under test lives there.
	runOneReload := func(c *config.Config) error {
		return executeReload(context.Background(), al, c, &provider, rs, msgBus, true)
	}
	loadNext := func() (*config.Config, error) { return cfg, nil }
	stop := make(chan struct{})
	drained := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		select {
		case <-drained:
		case <-time.After(10 * time.Second):
			t.Error("reload consumer goroutine did not exit within 10s")
		}
	})
	go func() {
		defer close(drained)
		for {
			select {
			case <-stop:
				return
			case <-rs.manualReloadChan:
				runReloadCycle(al, rs, nil, runOneReload, loadNext)
			}
		}
	}()

	// Sanity: boot must have already injected the initial value.
	require.Equal(t, "old-value", os.Getenv(ref),
		"sanity: boot must have injected the initial credential value")

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		credStore:     credStore,
	}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/credentials",
		strings.NewReader(`{"key":"`+ref+`","value":"new-value"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotContains(t, resp, "reload_warning",
		"the reload must have confirmed cleanly with no warning; got %v", resp)

	// The critical assertion: WITHOUT any explicit /reload call, the new
	// value must already be live in the process environment the instant the
	// POST returns — this is what ModelConfig.APIKey() reads for the next
	// provider call.
	assert.Equal(t, "new-value", os.Getenv(ref),
		"credential set must hot-reload the provider's live API key without a separate /reload call")
}

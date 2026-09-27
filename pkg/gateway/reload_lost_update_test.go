// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// sessionCookieResolves reports whether a browser presenting token as its
// omnipus-session cookie authenticates against the LIVE in-memory config —
// the exact check checkBearerAuth's cookie branch performs.
func sessionCookieResolves(t *testing.T, cfg *config.Config, token string) bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: token})
	user, err := middleware.ResolveUserFromCookie(r, cfg.Gateway.Users)
	return err == nil && user != nil
}

// TestExecuteReload_KeepsConfigWriteCommittedBetweenLoadAndSwap pins the
// reload lost-update fix (E2E retention.spec.ts:234, run 36260103324): a
// reload loads config.json, stops services, and only then swaps the loaded
// config into memory. A config write committed through safeUpdateConfigJSON
// in between — here a login storing its session-cookie hash, exactly what
// HandleLogin does — must survive the reload's swap. Before the fix the swap
// installed the reload's stale snapshot, the fresh session hash vanished from
// memory, and the just-logged-in browser got 401 on its next request.
//
// The interleaving is forced deterministically: the reload's snapshot is
// loaded first, the write is committed second, the reload executes third.
func TestExecuteReload_KeepsConfigWriteCommittedBetweenLoadAndSwap(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	seed := &config.Config{
		Gateway: config.GatewayConfig{
			Host:  "127.0.0.1",
			Port:  0,
			Users: []config.UserConfig{{Username: "admin", PasswordHash: "$2a$10$unusedunusedunusedunuseuW6c1O2h6x8QeY9eQ4bq6x1a2b3c4d5e"}},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, MaxTokens: 4096},
		},
	}
	require.NoError(t, config.SaveConfig(configPath, seed))

	credStore := newUnlockedStore(t, tmpDir)
	bootCfg, err := config.LoadConfigWithStore(configPath, credStore)
	require.NoError(t, err)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, bootCfg, msgBus, &restMockProvider{})
	provider := providers.LLMProvider(&restMockProvider{})

	rs, err := setupAndStartServices(
		context.Background(), bootCfg, credentials.SecretBundle{}, al, msgBus, tmpDir, credStore,
		&SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false,
	)
	require.NoError(t, err, "setupAndStartServices must boot cleanly")
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })
	api := rs.restAPIRef
	require.NotNil(t, api, "setupAndStartServices must wire the restAPI the reload shares configMu with")
	require.Equal(t, configPath, api.configPath(), "setup: the REST writer and the reload must share config.json")
	// serveReloadLoop's wiring: every reload reads config.json through this
	// loader, including handleConfigReload's swap-time re-read.
	rs.loadConfigForSwap = newReloadConfigLoader(configPath, tmpDir, credStore, rs)

	// 1. The reload loads its snapshot (what loadReloadConfig / the watcher do).
	reloadSnapshot, err := config.LoadConfigWithStore(configPath, credStore)
	require.NoError(t, err)

	// 2. A login commits its session-cookie hash (HandleLogin's write).
	sessionToken, sessionHash, err := middleware.MintSessionToken()
	require.NoError(t, err)
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			return errors.New("gateway is not an object")
		}
		users, ok := gw["users"].([]any)
		if !ok || len(users) == 0 {
			return errors.New("gateway.users is not a non-empty array")
		}
		admin, ok := users[0].(map[string]any)
		if !ok {
			return errors.New("gateway.users[0] is not an object")
		}
		admin["session_token_hash"] = string(sessionHash)
		return nil
	}))
	require.True(t, sessionCookieResolves(t, al.GetConfig(), sessionToken),
		"setup: the login's session must be live right after its write")

	// 3. The reload executes with its (now stale) snapshot.
	rs.reloadInFlight = true
	require.NoError(t, executeReload(context.Background(), al, reloadSnapshot, &provider, rs, msgBus, true))

	require.True(t, sessionCookieResolves(t, al.GetConfig(), sessionToken),
		"a login committed between the reload's config load and its swap was undone by the swap: "+
			"the just-issued session cookie no longer authenticates (lost update)")
}

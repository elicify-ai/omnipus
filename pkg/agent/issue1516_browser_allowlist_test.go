// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// issue1516_browser_allowlist_test.go — regression tests for #1516.
//
// The operator's SSRF allow list (sandbox.ssrf.allow_internal) must be
// honoured by the agent browser and by fetch_url whether or not SSRF
// protection (sandbox.ssrf.enabled) is switched on. Expected values come
// from the founder's stated expectation in #1516 and from the config doc on
// OmnipusSSRFConfig.AllowInternal ("exempt from SSRF blocking"), never from
// the implementation. Both surfaces keep their always-on private-address
// block for every address that is NOT on the list.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestIssue1516_BrowserHonoursAllowInternal_SSRFOff_GodModeOn mirrors the
// founder's live config: sandbox.ssrf.enabled absent (false),
// sandbox.ssrf.allow_internal = ["127.0.0.2"], sandbox.god_mode = true (and
// available). The agent browser's URL gate (BrowserManager.ValidateURL — the
// same gate browser_navigate, browser_open_tab and the live view use) must
// let http://127.0.0.2:15600/ through.
func TestIssue1516_BrowserHonoursAllowInternal_SSRFOff_GodModeOn(t *testing.T) {
	withGodModeAvailable(t, true)

	cfg := minimalTestConfig(t)
	cfg.Sandbox.SSRF.Enabled = false
	cfg.Sandbox.SSRF.AllowInternal = []string{"127.0.0.2"}
	cfg.Sandbox.GodMode = true
	cfg.Gateway.Port = 54321

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	require.True(t, GodModeActive(cfg), "fixture: God Mode must be active")

	defaultAgent := al.GetRegistry().primaryChatAgentForTest()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, BrowserResolveOK, outcome)

	err := mgr.ValidateURL(context.Background(), "http://127.0.0.2:15600/")
	require.NoError(t, err,
		"#1516: with SSRF protection off, God Mode on and 127.0.0.2 on "+
			"sandbox.ssrf.allow_internal, the agent browser must reach 127.0.0.2")
}

// TestIssue1516_Browser_SSRFOff_KeepsAlwaysOnBlock: with SSRF protection off
// the browser keeps its always-on block (browser wiring spec FR-004) for
// every private address NOT on the allow list, and keeps the gateway preview
// exception. God Mode off, so the result cannot depend on it.
func TestIssue1516_Browser_SSRFOff_KeepsAlwaysOnBlock(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Sandbox.SSRF.Enabled = false
	cfg.Sandbox.SSRF.AllowInternal = []string{"127.0.0.2"}
	cfg.Gateway.Port = 54321

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defaultAgent := al.GetRegistry().primaryChatAgentForTest()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, BrowserResolveOK, outcome)

	require.NoError(t, mgr.ValidateURL(context.Background(), "http://127.0.0.2:15600/"),
		"listed address must be allowed with SSRF protection off")
	require.Error(t, mgr.ValidateURL(context.Background(), "http://127.0.0.3:15600/"),
		"a loopback address NOT on the list must stay blocked with SSRF protection off")
	require.Error(t, mgr.ValidateURL(context.Background(), "http://169.254.169.254/latest/meta-data/"),
		"cloud metadata must stay blocked with SSRF protection off")
	require.NoError(t, mgr.ValidateURL(context.Background(), "http://localhost:54321/preview/x"),
		"the gateway preview exception must survive")
}

// TestIssue1516_Control_SSRFOn_AllowListHonoured is the control: the SAME
// allow list with SSRF protection switched ON is honoured, and a non-listed
// loopback address stays blocked.
func TestIssue1516_Control_SSRFOn_AllowListHonoured(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Sandbox.SSRF.Enabled = true
	cfg.Sandbox.SSRF.AllowInternal = []string{"127.0.0.2"}
	cfg.Gateway.Port = 54321

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defaultAgent := al.GetRegistry().primaryChatAgentForTest()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, BrowserResolveOK, outcome)

	require.NoError(t, mgr.ValidateURL(context.Background(), "http://127.0.0.2:15600/"))
	// And a non-listed loopback address stays blocked (the list is not a blanket allow).
	require.Error(t, mgr.ValidateURL(context.Background(), "http://127.0.0.3:15600/"))
}

// fetchURLFromDefaultAgent runs the registered fetch_url tool of the default
// chat agent against rawURL.
func fetchURLFromDefaultAgent(t *testing.T, al *AgentLoop, rawURL string) (string, bool) {
	t.Helper()
	defaultAgent := al.GetRegistry().primaryChatAgentForTest()
	require.NotNil(t, defaultAgent)
	tool, ok := defaultAgent.Tools.Get("fetch_url")
	require.True(t, ok, "fetch_url must be registered on the default agent")
	res := tool.Execute(context.Background(), map[string]any{"url": rawURL})
	require.NotNil(t, res)
	return res.ForLLM, res.IsError
}

// TestIssue1516_FetchURLHonoursAllowInternal covers F2: fetch_url honours
// sandbox.ssrf.allow_internal with SSRF protection off AND on, while an
// address not on the list stays refused.
func TestIssue1516_FetchURLHonoursAllowInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("issue1516-fetch-ok"))
	}))
	t.Cleanup(srv.Close)
	require.True(t, strings.HasPrefix(srv.URL, "http://127.0.0.1:"), "fixture: server on 127.0.0.1, got %s", srv.URL)

	for _, ssrfOn := range []bool{false, true} {
		name := "ssrf_off"
		if ssrfOn {
			name = "ssrf_on"
		}
		t.Run(name, func(t *testing.T) {
			cfg := minimalTestConfig(t)
			cfg.Sandbox.SSRF.Enabled = ssrfOn
			cfg.Sandbox.SSRF.AllowInternal = []string{"127.0.0.1"}
			al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})

			out, isErr := fetchURLFromDefaultAgent(t, al, srv.URL+"/")
			require.False(t, isErr, "listed 127.0.0.1 must be fetchable, got: %s", out)
			require.Contains(t, out, "issue1516-fetch-ok")
		})
	}

	t.Run("not_listed_stays_blocked", func(t *testing.T) {
		cfg := minimalTestConfig(t)
		cfg.Sandbox.SSRF.Enabled = false
		cfg.Sandbox.SSRF.AllowInternal = []string{"127.0.0.2"}
		al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})

		out, isErr := fetchURLFromDefaultAgent(t, al, srv.URL+"/")
		require.True(t, isErr, "127.0.0.1 is not on the list and must stay refused, got: %s", out)
		require.NotContains(t, out, "issue1516-fetch-ok")
	})
}

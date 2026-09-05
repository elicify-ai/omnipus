// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// browser_ssrf_wiring_test.go — regression guard for code-review finding M2
// (preview-on-main-listener, ADR-044): registerSharedTools's browser-tool
// SSRF wiring (loop.go, "CRITICAL (code-review M2)" comment block, ~line
// 1681) must grant the gateway-origin exception on a CLONE of
// al.ssrfChecker (security.SSRFChecker.CloneWithGatewayOrigin), never by
// mutating the shared singleton in place via AllowGatewayOrigin. al.ssrfChecker
// is also consulted by provider base_url validation and skill-installer URL
// validation (pkg/gateway) — if the browser wiring ever regresses to mutate
// the singleton directly, every one of those other call sites would silently
// start accepting http://localhost:<gateway.port>/... too, a blanket-loopback
// widening the ADR explicitly rejected.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestBrowserSSRFWiring_SingletonNotMutated_CloneAccepts proves both halves
// of the M2 fix from a single, minimal AgentLoop construction. No browser
// process is launched: BrowserManager.ValidateURL (pkg/tools/browser/manager.go)
// is a pure scheme + SSRF check that never touches chromedp — the browser is
// only ever started lazily on first navigation (see manager.go's "lazy init"
// doc on NewBrowserManager) — so this stays cheap and deterministic, matching
// the existing TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager
// in this package, which relies on the same guarantee.
//
//  1. The gateway's own preview origin (http://localhost:<gateway.port>/...)
//     is BLOCKED by the shared al.ssrfChecker singleton — proving
//     registerSharedTools never called AllowGatewayOrigin on it directly.
//  2. The SAME origin is ACCEPTED by the per-agent browser manager's own
//     checker (reached via AgentLoop.BrowserManagerForAgent → ValidateURL) —
//     proving the clone actually carries the exception the browser tool
//     needs (US-10/S21: the built-in browser must be able to reach
//     serve_web's own /preview/ URL).
//
// cfg.Sandbox.SSRF.Enabled is explicitly turned on so al.ssrfChecker is
// non-nil: registerSharedTools only takes the CloneWithGatewayOrigin branch
// in that case (loop.go ~line 1692). When SSRF is disabled, al.ssrfChecker
// stays nil and the browser wiring takes a different, already-isolated
// branch (a brand-new security.NewSSRFChecker(nil) with the exception set
// directly on it) — that branch has no shared singleton to leak into, so it
// is not what M2 is about.
func TestBrowserSSRFWiring_SingletonNotMutated_CloneAccepts(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Sandbox.SSRF.Enabled = true
	cfg.Gateway.Port = 54321

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	require.NotNil(t, al.ssrfChecker, "cfg.Sandbox.SSRF.Enabled=true must build the shared singleton")

	gatewayOriginURL := "http://localhost:54321/preview/some-agent/some-token/"

	// --- Half 1: the shared singleton must still BLOCK the gateway origin.
	singletonErr := al.ssrfChecker.CheckURL(context.Background(), gatewayOriginURL)
	assert.Error(t, singletonErr,
		"M2 regression: al.ssrfChecker (the singleton shared with provider base_url and "+
			"skill-installer URL validation) must still BLOCK http://localhost:<gateway.port> — "+
			"if this passes, browser-tool registration mutated the shared checker in place")

	// --- Half 2: the browser tool's OWN checker (a clone) must ACCEPT it.
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, BrowserResolveOK, outcome,
		"registerSharedTools must have built a browser for the default agent's workspace")

	cloneErr := mgr.ValidateURL(context.Background(), gatewayOriginURL)
	assert.NoError(t, cloneErr,
		"the browser tool's own SSRF checker (built via CloneWithGatewayOrigin) must ACCEPT "+
			"the gateway's own preview origin — otherwise US-10/S21 (the built-in browser reaching "+
			"serve_web's own preview URL) is broken")
}

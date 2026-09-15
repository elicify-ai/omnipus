// loop_wire_test.go: tests for attach tools and deps onto agents

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager is the
// ADR-038 finding #2 regression guard, carried through the ADR-075 re-key:
// registerSharedTools MUST call Shutdown() on the PRIOR BrowserManager for a
// browsing key before installing a replacement for that SAME key (the
// hot-reload path, driven by ReloadProviderAndConfig on every Settings save).
// Before the fix, the old manager's Go reference was simply dropped and its
// Chromium subprocess (if the allocator had ever been started) leaked —
// Shutdown() is the only thing that cancels the chromedp allocator context.
//
// It drives the REAL registerSharedTools code path via a minimal AgentLoop (not
// a re-implementation of its browser block), so a future refactor of that block
// stays covered. It configures tools.browser.cdp_url to a
// syntactically-valid-but-unreachable loopback address: ensureStarted() takes
// chromedp.NewRemoteAllocator's lazy remote-CDP path in that case, which only
// stores the URL and returns a context/cancel pair; it does NOT dial anything
// until Allocate() is invoked by an in-flight chromedp.Run. So Session() below
// reliably flips the manager into "started" with zero dependency on a real
// Chromium binary or a reachable CDP endpoint.
func TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Tools.Browser.CDPURL = "ws://127.0.0.1:1/unreachable-by-design"

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	id := defaultAgent.ID

	firstMgr, outcome := al.BrowserManagerForAgent(context.Background(), id, "")
	require.Equal(t, BrowserResolveOK, outcome,
		"registerSharedTools must have built a browser for the default agent's workspace")
	require.NotNil(t, firstMgr)
	require.False(t, firstMgr.Started(), "a freshly registered manager must not be started until first use")

	// Trigger ensureStarted() via the one exported path (Session). The
	// subsequent tab-creation dial against the unreachable URL is expected to
	// fail and the error is deliberately ignored — the allocator having been
	// constructed is what is being verified, not that a tab could be opened.
	_, _ = firstMgr.Session(firstMgr.OperatorSessionID())
	require.True(t, firstMgr.Started(),
		"test setup: the manager must be 'started' for Shutdown()'s effect to be observable")

	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), provider, cfg))

	secondMgr, outcome := al.BrowserManagerForAgent(context.Background(), id, "")
	require.Equal(t, BrowserResolveOK, outcome)
	require.NotSame(t, firstMgr, secondMgr, "hot reload must install a NEW manager instance, not reuse the old one")

	require.False(t, firstMgr.Started(),
		"the FIRST manager must be Shutdown() (Started() must go false) before it is replaced — "+
			"otherwise its Chromium allocator (if ever launched) leaks on every hot reload")
}

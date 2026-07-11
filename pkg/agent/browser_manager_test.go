package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// TestBrowserManagerForAgent exercises the ADR-038 D4 per-agent accessor in
// isolation. A raw AgentLoop literal with just browserMgrs populated is
// enough, since BrowserManagerForAgent only touches al.mu (a zero-value
// sync.RWMutex is ready to use) and al.browserMgrs — this avoids the cost of
// a full NewAgentLoop (provider, registry, session stores, ...) for what is
// purely a map-lookup accessor.
//
// This is also a regression guard for the exact bug ADR-038 D4 fixes: before
// the per-agent map, a single al.browserMgr field meant only the LAST agent
// registerSharedTools processed ended up with a live (non-Shutdown()'d)
// manager. Two distinct managers surviving under two distinct agent IDs is
// the behavior that bug made impossible.
func TestBrowserManagerForAgent(t *testing.T) {
	cfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	mgrA, err := browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	mgrB, err := browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)

	al := &AgentLoop{
		browserMgrs: map[string]*browser.BrowserManager{
			"agentA": mgrA,
			"agentB": mgrB,
		},
	}

	got, ok := al.BrowserManagerForAgent("agentA")
	require.True(t, ok)
	require.Same(t, mgrA, got, "agentA's manager must be the exact instance registered for it")

	got, ok = al.BrowserManagerForAgent("agentB")
	require.True(t, ok)
	require.Same(t, mgrB, got, "agentB's manager must be independent of agentA's")

	_, ok = al.BrowserManagerForAgent("unknown-agent")
	require.False(t, ok, "an agent with no registered browser manager must report ok=false, not panic")
}

// TestBrowserManagerForAgent_NoManagersRegistered covers the pre-
// registerSharedTools state (an AgentLoop whose browserMgrs map is empty —
// e.g. browser tool registration failed for every agent, per the ErrorCF
// path in registerSharedTools).
func TestBrowserManagerForAgent_NoManagersRegistered(t *testing.T) {
	al := &AgentLoop{browserMgrs: make(map[string]*browser.BrowserManager)}
	_, ok := al.BrowserManagerForAgent("agentA")
	require.False(t, ok)
}

// TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager is the
// ADR-038 finding #2 regression guard: registerSharedTools MUST call
// Shutdown() on an agent's PRIOR BrowserManager before installing a
// replacement for the SAME agentID (the hot-reload path, driven by
// ReloadProviderAndConfig on every Settings save). Before the fix, the old
// manager's Go reference was simply dropped and its Chromium subprocess (if
// the allocator had ever been started) leaked — Shutdown() is the only thing
// that cancels the chromedp allocator context and kills it.
//
// This drives the REAL registerSharedTools code path via a minimal AgentLoop
// (not a re-implementation of its browser block), so a future refactor of
// that block stays covered. It configures tools.browser.cdp_url to a
// syntactically-valid-but-unreachable loopback address: BrowserManager's
// ensureStarted() takes chromedp.NewRemoteAllocator's lazy remote-CDP path
// in that case, which — per chromedp — only stores the URL and returns a
// context/cancel pair; it does NOT dial anything until Allocate() is
// actually invoked by an in-flight chromedp.Run. That means Session() below
// reliably flips the manager into "started" (BrowserManager.Started()) with
// zero dependency on a real Chromium binary or a reachable CDP endpoint —
// this test never spawns or requires a real browser process.
func TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Tools.Browser.CDPURL = "ws://127.0.0.1:1/unreachable-by-design"

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	id := defaultAgent.ID

	firstMgr, ok := al.BrowserManagerForAgent(id)
	require.True(t, ok, "registerSharedTools must have registered a browser manager for the default agent")
	require.NotNil(t, firstMgr)
	require.False(t, firstMgr.Started(), "a freshly registered manager must not be started until first use (lazy init)")

	// Trigger ensureStarted() via the one exported path (Session). The
	// subsequent tab-creation dial against the unreachable URL is expected
	// to fail and the error is deliberately ignored — the allocator having
	// been constructed (ensureStarted succeeding) is what we're verifying,
	// not that a tab could actually be opened.
	_, _ = firstMgr.Session(browser.DefaultSessionID)
	require.True(t, firstMgr.Started(),
		"test setup: the manager must be 'started' for Shutdown()'s effect to be observable")

	// Re-run registerSharedTools by driving the same reload path production
	// code uses (ReloadProviderAndConfig re-registers every agent's tools,
	// including the browser block).
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), provider, cfg))

	secondMgr, ok := al.BrowserManagerForAgent(id)
	require.True(t, ok)
	require.NotSame(t, firstMgr, secondMgr, "hot reload must install a NEW manager instance, not reuse the old one")

	require.False(t, firstMgr.Started(),
		"the FIRST manager must be Shutdown() (Started() must go false) before it is replaced — "+
			"otherwise its Chromium allocator (if ever launched) leaks on every hot reload")
}

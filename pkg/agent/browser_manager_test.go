package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

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

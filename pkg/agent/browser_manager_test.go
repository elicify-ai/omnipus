package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// errZeroKeyReachedFactory is returned by a test factory that must never be
// called, so the failure is a named error rather than a nil-nil return.
var errZeroKeyReachedFactory = errors.New("the manager factory was reached with a zero browsing key")

// browserTestKey mints a browsing key for a workspace id in this package's
// tests. It goes through the package's only public constructor so a key a test
// holds is subject to the same FR-037 validation a real one is.
func browserTestKey(t *testing.T, workspaceID string) browser.BrowsingKey {
	t.Helper()
	k, err := browser.ResolveBrowsingKeyForAgent(
		browserKeyProbeHome(t, workspaceID), "probe-agent", workspaceID,
	)
	require.NoError(t, err)
	return k
}

// browserKeyProbeHome writes one workspace file whose CoreTeam contains
// "probe-agent", so browserTestKey's resolution has something real to resolve
// against. Minting a key is deliberately not possible without a workspace.
func browserKeyProbeHome(t *testing.T, workspaceID string) string {
	t.Helper()
	home := t.TempDir()
	dir := home + "/workspaces"
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		dir+"/"+workspaceID+".json",
		[]byte(`{"id":"`+workspaceID+`","core_team":["probe-agent"]}`),
		0o600,
	))
	return home
}

// TestLoop_BrowserManagerForKey_OnePerKey is the FR-001 guard: exactly ONE
// BrowserManager — and therefore one Chrome, one profile directory and one
// cookie jar — per browsing key, however many times it is asked for.
//
// The failure it guards against is not a leak. It is two managers for one
// workspace, each with its own Chrome and its own logins, where an agent's
// tools drive one and the operator's live panel watches the other. That is
// ADR-072 §1.1's reported defect in its second form, and nothing about it looks
// wrong from either side.
func TestLoop_BrowserManagerForKey_OnePerKey(t *testing.T) {
	cfg, err := browser.DefaultConfig()
	require.NoError(t, err)

	built := 0
	al := &AgentLoop{
		browserMgrs: make(map[string]*browser.BrowserManager),
		browserFactory: func(browser.BrowsingKey) (*browser.BrowserManager, error) {
			built++
			return browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
		},
	}

	keyA := browserTestKey(t, "workspace-a")
	keyB := browserTestKey(t, "workspace-b")

	first, err := al.BrowserManagerForKey(context.Background(), keyA)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := al.BrowserManagerForKey(context.Background(), keyA)
	require.NoError(t, err)
	require.Same(t, first, second, "one key must resolve to ONE manager, not a fresh Chrome per call")
	require.Equal(t, 1, built, "the second lookup must not build a second manager")

	other, err := al.BrowserManagerForKey(context.Background(), keyB)
	require.NoError(t, err)
	require.NotSame(t, first, other, "two workspaces must NOT share a browser — that is one cookie jar for both")
	require.Equal(t, 2, built)
}

// TestLoop_BrowserManagerForKey_ZeroKeyIsNamedFailure: a zero key is the value
// ResolveBrowsingKey returns alongside ErrNoBrowsingContext. It must never
// resolve to a browser — a shared "" -keyed manager is exactly the merged
// browser FR-007 refuses to create.
func TestLoop_BrowserManagerForKey_ZeroKeyIsNamedFailure(t *testing.T) {
	al := &AgentLoop{
		browserMgrs: make(map[string]*browser.BrowserManager),
		browserFactory: func(browser.BrowsingKey) (*browser.BrowserManager, error) {
			t.Error("a zero browsing key must never reach the manager factory")
			return nil, errZeroKeyReachedFactory
		},
	}
	mgr, err := al.BrowserManagerForKey(context.Background(), browser.BrowsingKey{})
	require.Nil(t, mgr)
	require.ErrorIs(t, err, browser.ErrNoBrowsingContext)
}

// TestLoop_BrowserMgrsCommentIsCurrent is FR-002d. The standing comment on
// AgentLoop.browserMgrs described a map keyed by AGENT ID and cited ADR-038 D4.
// After the re-key that description is false, and it is the kind of false that
// costs a day: the next person to touch the reload prune reads "keyed by
// agentID", diffs the map against registry.ListAgentIDs(), matches nothing, and
// disposes every workspace's Chrome context on the first Settings save.
//
// A comment is not testable, so this asserts the two things that make it
// wrong-proof: the field's doc must name the browsing key, and must not claim
// the old per-agent keying.
func TestLoop_BrowserMgrsCommentIsCurrent(t *testing.T) {
	src, err := os.ReadFile("loop.go")
	require.NoError(t, err)
	text := string(src)

	start := strings.Index(text, "// browserMgrs holds one BrowserManager per")
	require.GreaterOrEqual(t, start, 0, "the browserMgrs doc comment has moved or been deleted")
	end := strings.Index(text[start:], "browserMgrs map[string]*browser.BrowserManager")
	require.Greater(t, end, 0, "the browserMgrs doc comment no longer precedes the field")
	doc := text[start : start+end]

	require.Contains(t, doc, "BROWSING KEY",
		"the doc must say the map is keyed by the browsing key, not by an agent id")
	require.Contains(t, doc, "ws:<workspaceID>",
		"the doc must show the actual key shape a reader will see in a log line")
	require.NotContains(t, doc, "one BrowserManager per agent",
		"the pre-ADR-072 claim survived the re-key — it is now false and actively misleading")
}

// TestBrowserManagerForAgent_DistinguishesNotRegisteredFromNoWorkspace is
// FR-008a. "Browser tools are not registered for this agent" and "this agent is
// not on a workspace team" are different operator problems with different
// remedies, and browser_inspect.go reported the former for BOTH — so an
// operator whose agent simply had no workspace was sent to check tool
// registration, which was fine.
func TestBrowserManagerForAgent_DistinguishesNotRegisteredFromNoWorkspace(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir()) // no workspaces on disk
	al := &AgentLoop{
		browserMgrs:             make(map[string]*browser.BrowserManager),
		browserRegisteredAgents: map[string]bool{"has-tools": true},
	}

	_, outcome := al.BrowserManagerForAgent(context.Background(), "no-tools-at-all", "")
	require.Equal(t, BrowserResolveNotRegistered, outcome)

	_, outcome = al.BrowserManagerForAgent(context.Background(), "has-tools", "")
	require.Equal(t, BrowserResolveNoWorkspace, outcome,
		"an agent WITH browser tools but no workspace must not be reported as unregistered")
}

// TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager is the
// ADR-038 finding #2 regression guard, carried through the ADR-072 re-key:
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

package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/require"
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
// ADR-075 §1.1's reported defect in its second form, and nothing about it looks
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
	text := readLoopSourcesForTest(t)

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
		"the pre-ADR-075 claim survived the re-key — it is now false and actively misleading")
}

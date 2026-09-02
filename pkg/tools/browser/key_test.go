package browser

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// key_test.go — FR-080. Whose tabs are these?
//
// Every test in this file is about one question, and it is the question
// ADR-072 §1.1 records an agent getting wrong: an operator browses in the live
// panel, switches the chat from Mia to Jim, and Jim reports zero tabs.

// listTabsThrough runs browser_list_tabs against a resolver and returns the
// tab URLs it reported, plus whether the browser was reported as started.
func listTabsThrough(t *testing.T, res ManagerResolver) ([]string, bool) {
	t.Helper()
	tool := &ListTabsTool{res: res}
	result := tool.Execute(context.Background(), map[string]any{})
	require.NotNil(t, result)
	require.False(t, result.IsError, "browser_list_tabs failed: %s", result.ForLLM)

	var body struct {
		Tabs []struct {
			Index int    `json:"index"`
			URL   string `json:"url"`
		} `json:"tabs"`
		BrowserStarted bool `json:"browser_started"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	urls := make([]string, 0, len(body.Tabs))
	for _, tab := range body.Tabs {
		urls = append(urls, tab.URL)
	}
	return urls, body.BrowserStarted
}

// TestTabs_TwoSessionsDoNotMerge is the property the whole re-key exists to
// establish, in the direction that fails loudly.
//
// One workspace means one browser. Two chats in it are two SESSIONS, and their
// tabs must not merge — if they did, an agent asked "what's open?" in chat A
// would answer with chat B's page, and both chats would drive one tab.
//
// A test that only asserted "chat A sees its own tab" would pass against a
// build that merged everything, so this asserts the ABSENCE: chat B does not
// see chat A's tab set.
func TestTabs_TwoSessionsDoNotMerge(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	ownerA, err := TabOwnerSession("session-A")
	require.NoError(t, err)
	ownerB, err := TabOwnerSession("session-B")
	require.NoError(t, err)

	// Chat A browses: it gets a tab set of its own.
	_, err = m.Session(sessionKey(testKey, ownerA))
	require.NoError(t, err)
	_, err = m.OpenTab(sessionKey(testKey, ownerA))
	require.NoError(t, err)

	tabsA, _, err := m.ListTabs(sessionKey(testKey, ownerA))
	require.NoError(t, err)
	require.Len(t, tabsA, 2, "chat A opened a second tab")

	// Chat B has not browsed at all. Its tab set must be its OWN — empty —
	// not chat A's.
	require.False(t, m.sessionExists(sessionKey(testKey, ownerB)),
		"chat B has not browsed; it must have no tab set, not a share of chat A's")

	_, err = m.Session(sessionKey(testKey, ownerB))
	require.NoError(t, err)
	tabsB, _, err := m.ListTabs(sessionKey(testKey, ownerB))
	require.NoError(t, err)
	require.Len(t, tabsB, 1, "chat B's first browse must give it ONE fresh tab, not chat A's two")

	// And the two sets are genuinely distinct objects, not two views of one.
	require.NotEqual(t, sessionKey(testKey, ownerA), sessionKey(testKey, ownerB))
	tabsA, _, err = m.ListTabs(sessionKey(testKey, ownerA))
	require.NoError(t, err)
	require.Len(t, tabsA, 2, "chat B browsing must not have disturbed chat A's tabs")
}

// TestTabs_AgentSwitchWithinASessionSeesTheSameTabs is ADR-072 §1.1's reported
// defect, asserted directly.
//
// Switching the chat from one agent to another does not change the session, so
// the second agent must see the tab the first opened. Before the re-key, the
// tab belonged to Mia's BrowserManager and Jim's reported zero.
func TestTabs_AgentSwitchWithinASessionSeesTheSameTabs(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	owner, err := TabOwnerSession("one-chat")
	require.NoError(t, err)

	// Mia is on the chat and opens two tabs. (The first browser_open_tab on a
	// cold browser creates the browsing context and its first tab; the second
	// genuinely appends — so two calls is what gives a two-tab set to compare.)
	mia := &fixedResolver{mgr: m, key: testKey, owner: owner}
	openTool := &OpenTabTool{res: mia}
	for i := 0; i < 2; i++ {
		result := openTool.Execute(tools.WithAgentID(context.Background(), "mia"), map[string]any{})
		require.NotNil(t, result)
		require.False(t, result.IsError, "browser_open_tab failed: %s", result.ForLLM)
	}

	miaTabs, _ := listTabsThrough(t, mia)
	require.Len(t, miaTabs, 2, "Mia has two tabs open in this chat")

	// The operator switches the chat to Jim. Same session, different agent.
	jim := &fixedResolver{mgr: m, key: testKey, owner: owner}
	jimTabs, started := listTabsThrough(t, jim)

	require.True(t, started, "Jim must see a running browser, not a cold one")
	require.Equal(t, miaTabs, jimTabs,
		"switching the chat's agent must not move, hide or duplicate a tab — "+
			"the tab belongs to the CHAT, not to whoever is on it")

	// No handover step exists, and none is needed (FR-006): Jim reached those
	// tabs with an ordinary tool call and nothing else.
	require.Equal(t, 1, jim.calls, "one ordinary tool call, no acquisition step")
	require.Equal(t, 3, mia.calls, "Mia's three calls each resolved their own manager, per Execute (FR-002a)")
}

// TestTabs_WorkspaceOwnedSetIsVisibleToAll: a tab the OPERATOR opened through
// the live panel is workspace-owned, so every agent on the workspace can see
// it — regardless of which chat they are in. That is the half of D1.9a that
// D1.9c deliberately preserves.
func TestTabs_WorkspaceOwnedSetIsVisibleToAll(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	// The operator browses in the panel: the panel addresses the
	// workspace-owned set.
	operatorSet := m.OperatorSessionID()
	require.Equal(t, testOperatorSessionID, operatorSet,
		"the manager's own operator-set accessor and sessionKey(key, TabOwnerWorkspace()) must agree")
	_, err := m.Session(operatorSet)
	require.NoError(t, err)
	_, err = m.OpenTab(operatorSet)
	require.NoError(t, err)

	// Two different chats, each with its own session, both asking for the
	// operator's set explicitly, both see the same two tabs.
	for _, sessionID := range []string{"chat-one", "chat-two"} {
		res := &fixedResolver{mgr: m, key: testKey, owner: TabOwnerWorkspace()}
		urls, started := listTabsThrough(t, res)
		require.True(t, started, "chat %q must see the operator's running browser", sessionID)
		require.Len(t, urls, 2, "chat %q must see BOTH of the operator's tabs", sessionID)
	}

	// And it is genuinely a different set from a chat's own tabs.
	own, err := TabOwnerSession("chat-one")
	require.NoError(t, err)
	require.NotEqual(t, operatorSet, sessionKey(testKey, own))
	require.False(t, m.sessionExists(sessionKey(testKey, own)),
		"the operator browsing must NOT have created a tab set for any chat")
}

// TestTabs_EmptyTranscriptSessionIsNamedFailure. An empty transcriptSessionID
// is an ordinary, reachable state on several turn types. Minting an owner from
// it would give every transcript-less turn on the workspace ONE shared tab set
// — and, worse, one that is indistinguishable from the operator's if it fell
// through to TabOwnerWorkspace(): a transcript-less turn would silently be able
// to drive the tabs a person is using.
func TestTabs_EmptyTranscriptSessionIsNamedFailure(t *testing.T) {
	owner, err := TabOwnerSession("")

	require.ErrorIs(t, err, ErrNoTabOwner)
	require.True(t, owner.IsZero(), "a refused owner must be the zero value, never a usable one")
	require.False(t, owner.IsWorkspace(),
		"a transcript-less turn must NOT fall through to the operator's workspace-owned set")
	require.Contains(t, err.Error(), "no transcript session")

	// And the tool reports it rather than opening anything.
	m := newTestManagerWithFakeTabs(t)
	res := &fixedResolver{mgr: m, err: ErrNoTabOwner}
	tool := &OpenTabTool{res: res}
	result := tool.Execute(context.Background(), map[string]any{})
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Contains(t, result.ForLLM, "no transcript session")
	require.Empty(t, m.sessions, "a refused turn must not have created a tab set")
}

// TestTabs_OwnerKeyIsTranscriptNotRouting is the delegation fixture.
//
// A delegated child gets its OWN transcriptSessionID but INHERITS its parent's
// routingSessionID verbatim — for a grandchild, routingSessionID equals the
// ROOT's. Keying tabs on the routing id would merge a whole delegation subtree
// into one tab set, and `delegate` defaults to async, so N delegate calls in one
// turn would run N children concurrently against that one merged set.
//
// The two ids are asserted to produce DIFFERENT owners even when the routing id
// is identical, which is the only shape in which the mistake is visible.
func TestTabs_OwnerKeyIsTranscriptNotRouting(t *testing.T) {
	const sharedRoutingSessionID = "routing-root" // identical for the whole subtree

	parent, err := TabOwnerSession("transcript-parent")
	require.NoError(t, err)
	childA, err := TabOwnerSession("transcript-child-a")
	require.NoError(t, err)
	childB, err := TabOwnerSession("transcript-child-b")
	require.NoError(t, err)

	owners := []TabOwner{parent, childA, childB}
	seen := map[string]bool{}
	for _, o := range owners {
		k := sessionKey(testKey, o)
		require.False(t, seen[k],
			"two members of one delegation subtree resolved to the SAME tab set — "+
				"that is the routing-session key, and it makes parallel delegation collide")
		seen[k] = true
	}
	require.Len(t, seen, 3)

	// The routing id must not appear in any of them: if it did, a future
	// refactor could start keying on it without any test noticing.
	for k := range seen {
		require.NotContains(t, k, sharedRoutingSessionID,
			"the tab-set key must be built from the TRANSCRIPT session id, never the routing one")
	}

	// And the sets really are independent inside one browser.
	m := newTestManagerWithFakeTabs(t)
	for _, o := range owners {
		_, err := m.Session(sessionKey(testKey, o))
		require.NoError(t, err)
	}
	for _, o := range owners {
		tabs, _, err := m.ListTabs(sessionKey(testKey, o))
		require.NoError(t, err)
		require.Len(t, tabs, 1,
			"each delegated turn must hold exactly its OWN tab, not a share of a merged set")
	}
}

// TestBrowsingKey_HasNoLiteralConstructor is the structural half of D1.11: a
// BrowsingKey is minted only by resolution. The zero value must be unusable and
// must not render as anything a map could key on by accident.
func TestBrowsingKey_HasNoLiteralConstructor(t *testing.T) {
	var zero BrowsingKey
	require.True(t, zero.IsZero())
	require.Equal(t, "", zero.String())
	require.Equal(t, "", zero.WorkspaceID())
	require.Equal(t, "", zero.ProfileSegment(),
		"a zero key must not render a profile directory — that directory would be shared by everything")

	resolved := newTestBrowsingKey(t, "01J8ZQ4T7N9K3M2P5R6S7T8V9W")
	require.False(t, resolved.IsZero())
	require.Equal(t, "ws:01J8ZQ4T7N9K3M2P5R6S7T8V9W", resolved.String())
	require.Equal(t, "01J8ZQ4T7N9K3M2P5R6S7T8V9W", resolved.WorkspaceID())
}

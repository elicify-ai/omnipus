package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// panel_tab_set_test.go — issue #671. Which tab is the operator actually
// watching?
//
// The live panel used to ask for OperatorSessionID() unconditionally. An
// agent's browser_* tools ask focusedTabSet, which diverts onto the operator's
// set ONLY when that set already has tabs. So whenever the operator's set was
// empty — a gateway whose home was empty at boot, or any gateway idle long
// enough for the reaper to close its unwatched warm tab — the agent browsed in
// the chat's own set while the panel looked at a workspace-owned tab it had
// just lazily created, parked on /browser-start. The navigate genuinely
// succeeded; the operator was simply shown a different tab, with no error
// anywhere.
//
// Every test here asserts the same property from a different starting state:
// PanelTabSetID and focusedTabSet must CONVERGE on one tab set.

// panelAndAgentTabSets resolves both halves for one chat: what the panel would
// drive, and what that chat's own tools would address.
func panelAndAgentTabSets(m *BrowserManager, chatSessionID string) (panel, agent string) {
	owner, err := TabOwnerSession(chatSessionID)
	if err != nil {
		panic("panel_tab_set_test: " + err.Error())
	}
	return m.PanelTabSetID(chatSessionID), sessionKey(m.key, m.focusedTabSet(owner))
}

// TestPanel_FollowsTheChatsOwnTabsWhenTheOperatorSetIsEmpty is #671 itself.
//
// The reported state, exactly: nothing in the operator's set, an agent that
// has browsed in its chat. The panel opened from that chat must land on the
// agent's tab — and must NOT bring a workspace-owned /browser-start tab into
// existence as a side effect of being asked.
func TestPanel_FollowsTheChatsOwnTabsWhenTheOperatorSetIsEmpty(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	const chat = "chat-671"
	chatOwner := mustTabOwner(chat)
	chatSet := sessionKey(testKey, chatOwner)

	// Precondition: the operator has browsed nothing. This is the state the
	// idle reaper returns every gateway to after tools.browser.idle_ttl.
	require.False(t, m.sessionExists(testOperatorSessionID),
		"precondition: the operator's set must be empty for this to be #671's scenario")

	// The agent browses in its chat. focusedTabSet has no operator set to
	// divert onto, so this lands in the chat's own set — which is exactly why
	// the panel could not see it.
	require.Equal(t, chatOwner, m.focusedTabSet(chatOwner),
		"with an empty operator set the agent must stay in its own tab set")
	_, err := m.Session(chatSet)
	require.NoError(t, err)
	agentTabs, _, err := m.ListTabs(chatSet)
	require.NoError(t, err)
	require.Len(t, agentTabs, 1, "the agent's browse gave this chat one tab")

	// The operator opens "Watch live" on that same chat.
	panelSet, agentSet := panelAndAgentTabSets(m, chat)

	require.Equal(t, chatSet, panelSet,
		"#671: the panel must drive the tab set the agent is actually browsing in, "+
			"not the operator's empty workspace-owned set")
	require.Equal(t, agentSet, panelSet,
		"panel and agent must resolve to ONE tab set — divergence is the silent wrong-tab bug")

	// And asking must not have manufactured the /browser-start tab #671
	// describes. Session() is what LiveViewRegistry.Attach calls, so this is
	// the lazily-created operator tab in its actual creation path.
	_, err = m.Session(panelSet)
	require.NoError(t, err)
	require.False(t, m.sessionExists(testOperatorSessionID),
		"opening the panel must not lazily create a workspace-owned tab parked on /browser-start")

	panelTabs, _, err := m.ListTabs(panelSet)
	require.NoError(t, err)
	require.Equal(t, agentTabs, panelTabs,
		"the panel must be looking at the agent's own tabs, byte for byte")
}

// TestPanel_ResolvesToTheOperatorWhenTheOperatorSetHasTabs is the other half of
// the convergence, and the reason the fix is not "always use the chat's set".
//
// With tabs in the operator's set, an agent whose chat has none is diverted
// onto the operator's (focusedTabSet's take-over default). The panel must
// follow it there — this is the headline "I browse, then ask the agent to take
// over" flow, and it must keep working unchanged.
func TestPanel_ResolvesToTheOperatorWhenTheOperatorSetHasTabs(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	const chat = "chat-operator-first"

	// The operator browses in the panel first.
	_, err := m.Session(testOperatorSessionID)
	require.NoError(t, err)

	panelSet, agentSet := panelAndAgentTabSets(m, chat)

	require.Equal(t, testOperatorSessionID, panelSet,
		"the operator's own tabs are what the panel drives when the chat has none of its own")
	require.Equal(t, agentSet, panelSet,
		"the agent diverts onto the operator's set here, so the panel must resolve there too")
	require.False(t, m.sessionExists(sessionKey(testKey, mustTabOwner(chat))),
		"resolving the panel must not create a tab set for a chat that has never browsed")
}

// TestPanel_ChatWithItsOwnTabsKeepsThemEvenWhenTheOperatorHasTabsToo.
//
// A chat that has already browsed owns its tabs. focusedTabSet is explicitly
// narrow — it never diverts an agent that has tabs of its own, "so an agent
// that has been browsing is never diverted mid-task" — and the panel must
// respect the same boundary, or watching chat A would show chat B's page.
func TestPanel_ChatWithItsOwnTabsKeepsThemEvenWhenTheOperatorHasTabsToo(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	const chat = "chat-both"
	chatSet := sessionKey(testKey, mustTabOwner(chat))

	_, err := m.Session(testOperatorSessionID)
	require.NoError(t, err)
	_, err = m.Session(chatSet)
	require.NoError(t, err)

	panelSet, agentSet := panelAndAgentTabSets(m, chat)

	require.Equal(t, chatSet, panelSet,
		"a chat that has browsed owns its tabs; the panel must show them, not the operator's")
	require.Equal(t, agentSet, panelSet, "panel and agent must still agree")
}

// TestPanel_NoChatContextResolvesToTheOperator pins the unchanged behaviour for
// every caller that legitimately has no chat to resolve against (the boot-time
// warm-up, a live-panel frame that carried no session id). An empty id is NOT
// an error here and must never become one — it is the operator, and the
// operator's set is the right answer for it.
func TestPanel_NoChatContextResolvesToTheOperator(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	require.Equal(t, testOperatorSessionID, m.PanelTabSetID(""),
		"no chat context means the operator's set, exactly as before")

	// Even with a chat that HAS tabs, an empty id must not reach into it.
	_, err := m.Session(sessionKey(testKey, mustTabOwner("some-chat")))
	require.NoError(t, err)
	require.Equal(t, testOperatorSessionID, m.PanelTabSetID(""),
		"an unnamed viewer must never be handed some other chat's tabs")
}

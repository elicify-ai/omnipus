package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFocusedTabSet_SessionWithNoTabsAddressesTheOperatorsSet is the UAT-15
// regression: the operator browses to a page, asks the agent to act on it, and
// the agent must land on THAT page.
//
// Before this, a session with no tabs of its own defaulted to its own (empty)
// set, so browser_type went to a blank tab while the panel showed the
// operator's page unchanged — and the agent reported success. Reproduced 4/4
// by a UAT tester driving the real UI.
func TestFocusedTabSet_SessionWithNoTabsAddressesTheOperatorsSet(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	session, err := TabOwnerSession("chat-with-no-tabs-of-its-own")
	require.NoError(t, err)

	// The operator has browsed: Session() on the workspace-owned key creates
	// that set with one tab, which is what the live panel is looking at.
	_, err = m.Session(sessionKey(m.key, TabOwnerWorkspace()))
	require.NoError(t, err)

	// hasTabsLocked needs m.mu; focusedTabSet takes it itself, so the
	// preconditions are read in their own critical section and released
	// before the call under test.
	m.mu.Lock()
	operatorHasTabs := m.hasTabsLocked(TabOwnerWorkspace())
	sessionHasTabs := m.hasTabsLocked(session)
	m.mu.Unlock()

	require.True(t, operatorHasTabs,
		"precondition: the fixture must give the operator's set a tab, "+
			"or this test proves nothing about preferring it")
	require.False(t, sessionHasTabs,
		"precondition: the asking session must have no tabs of its own")

	require.Equal(t, TabOwnerWorkspace(), m.focusedTabSet(session),
		"a session with no tabs of its own must address the OPERATOR's set — "+
			"otherwise the agent acts on a blank tab of its own while the panel "+
			"shows the operator's page unchanged, and reports success")
}

// TestFocusedTabSet_SessionWithItsOwnTabsIsNotDiverted is the other half, and
// it is what keeps the change narrow: an agent that has been browsing must
// never be silently moved onto the operator's page mid-task.
func TestFocusedTabSet_SessionWithItsOwnTabsIsNotDiverted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	session, err := TabOwnerSession("chat-that-has-been-browsing")
	require.NoError(t, err)

	// Both sets have a tab: the agent has been browsing, and so has the operator.
	_, err = m.Session(sessionKey(m.key, TabOwnerWorkspace()))
	require.NoError(t, err)
	_, err = m.Session(sessionKey(m.key, session))
	require.NoError(t, err)

	m.mu.Lock()
	sessionHasTabs := m.hasTabsLocked(session)
	m.mu.Unlock()
	require.True(t, sessionHasTabs, "precondition: the session owns a tab")

	require.Equal(t, session, m.focusedTabSet(session),
		"a session with its own tabs must keep addressing them")
}

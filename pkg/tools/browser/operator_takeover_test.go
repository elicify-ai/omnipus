// Omnipus — "the operator browses, then asks the agent to take over" (ADR-072
// D1.9b/D1.9c, FR-070/FR-071/FR-080)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// operator_takeover_test.go — the headline scenario, end to end.
//
// "The user starts browsing and then asks the agent to take over." Two halves,
// and both were broken by the same omission: the TabOwner a tool acted on was
// resolved once, from the turn's session id, and could never be the operator's
// workspace-owned set.
//
//  1. No production path handed an agent TabOwnerWorkspace(), so an agent
//     could not act on the operator's tabs at all — while browser_list_tabs
//     showed them, numbered, with prose saying every agent on the workspace
//     can drive them.
//  2. The live panel takes the human-control lock on
//     mgr.OperatorSessionID() == sessionKey(key, TabOwnerWorkspace()), and
//     every tool asked IsControlled(sessionKey(key, <the SESSION>)). The two
//     strings can never be equal, so the answer was "nobody is driving" every
//     single time.
//
// These tests are written against the REAL registered index space: the agent
// reads browser_list_tabs, picks the index it was shown, and the action must
// land on THAT tab.

// operatorTakeoverFixture builds a browser holding two tab sets: the chat
// session's own (2 tabs) and the operator's workspace-owned set (3 tabs).
//
// The counts are deliberately different so that an off-by-a-whole-set mistake
// cannot pass by coincidence.
func operatorTakeoverFixture(t *testing.T) *BrowserManager {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)

	// The operator browses first, through the live panel.
	operatorSet := m.OperatorSessionID()
	_, err := m.Session(operatorSet)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = m.OpenTab(operatorSet)
		require.NoError(t, err)
	}

	// The chat has been browsing too, in its own set.
	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	return m
}

// operatorTabIndexFromListing reads browser_list_tabs exactly as the model
// would and returns the index it was SHOWN for the operator's tab at
// operatorPos. Reading the index out of the listing rather than computing it
// here is the point: the constraint is that the listing must not name a tab
// the agent cannot then drive.
func operatorTabIndexFromListing(t *testing.T, m *BrowserManager, operatorPos int) int {
	t.Helper()
	list := &ListTabsTool{res: newFixedResolver(m)}
	out := decodeToolJSON(t, list.Execute(context.Background(), map[string]any{}))
	opTabs, ok := out["operator_tabs"].([]any)
	require.True(t, ok, "browser_list_tabs must report the operator's set: %v", out)
	require.Greater(t, len(opTabs), operatorPos)
	entry, ok := opTabs[operatorPos].(map[string]any)
	require.True(t, ok)
	idx, ok := entry["index"].(float64)
	require.True(t, ok, "every listed tab must carry the index the agent passes back: %v", entry)
	return int(idx)
}

// TestOperatorTakeover_AgentActsOnTheOperatorsTabByIndex is the user's
// scenario, verbatim: the operator's tab exists, the agent acts on it by the
// index it was shown, and the action lands on THAT tab.
func TestOperatorTakeover_AgentActsOnTheOperatorsTabByIndex(t *testing.T) {
	m := operatorTakeoverFixture(t)
	operatorSet := m.OperatorSessionID()

	// The agent's turn is an ORDINARY one: its resolver hands it its own
	// session's tab set, exactly as pkg/agent's resolver does in production.
	res := newFixedResolver(m)

	// It reads the listing and picks the operator's FIRST tab.
	idx := operatorTabIndexFromListing(t, m, 0)

	switchTab := &SwitchTabTool{res: res}
	result := switchTab.Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"index": float64(idx)},
	)
	require.NotNil(t, result)
	require.False(t, result.IsError,
		"an agent must be able to act on the tab browser_list_tabs showed it: %s", result.ForLLM)

	// It landed on the OPERATOR's tab...
	_, opTabs, opActive, err := m.ListTabsState(operatorSet)
	require.NoError(t, err)
	require.Len(t, opTabs, 3)
	assert.Equal(t, 0, opActive,
		"browser_switch_tab on the operator's first tab must make THAT tab active")

	// ...and not on a tab of the agent's own, which is the silent-wrong-action
	// this fix exists to remove.
	_, ownTabs, ownActive, err := m.ListTabsState(testSessionID)
	require.NoError(t, err)
	require.Len(t, ownTabs, 2, "the agent must not have opened a tab of its own instead")
	assert.Equal(t, 1, ownActive,
		"the agent's own active tab must be untouched — the action was about the operator's tab")

	// The result must SAY which set it acted on. A call that silently lands
	// somewhere else and reports plain success is the failure class this
	// project treats as its worst.
	body := decodeToolJSON(t, result)
	assert.Equal(t, "workspace_operator", body["tab_set"],
		"the result must name the tab set the call acted on")

	// Nothing transferred: the operator's set is still the operator's
	// (FR-070 — acquisition is implicit and has no representation).
	assert.Equal(t, operatorSet, m.OperatorSessionID())
	assert.True(t, m.sessionExists(operatorSet))
	assert.True(t, m.sessionExists(testSessionID),
		"the agent's own tab set must still exist; taking over does not discard it")

	// And the take-over holds for the calls that follow, which is what "take
	// over" means: browser_open_tab now opens IN the operator's browsing, not
	// invisibly back in the chat's own set.
	openTab := &OpenTabTool{res: res}
	openResult := openTab.Execute(tools.WithAgentID(context.Background(), "jim"), map[string]any{})
	require.NotNil(t, openResult)
	require.False(t, openResult.IsError, "%s", openResult.ForLLM)

	_, opTabs, _, err = m.ListTabsState(operatorSet)
	require.NoError(t, err)
	assert.Len(t, opTabs, 4,
		"after taking over, the agent's next action must continue on the operator's tabs")
	_, ownTabs, _, err = m.ListTabsState(testSessionID)
	require.NoError(t, err)
	assert.Len(t, ownTabs, 2,
		"the follow-up action must NOT have landed back in the agent's own set")

	// Switching back to its own tabs is symmetric, and is how the agent hands
	// the browsing back.
	backResult := switchTab.Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"index": float64(0)},
	)
	require.NotNil(t, backResult)
	require.False(t, backResult.IsError, "%s", backResult.ForLLM)
	backBody := decodeToolJSON(t, backResult)
	assert.Equal(t, "this_chat_session", backBody["tab_set"])
	_, _, ownActive, err = m.ListTabsState(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 0, ownActive)
}

// TestOperatorTakeover_ListingOnlyNamesDrivableTabs is the constraint on the
// listing: every index it shows must be an index a tool accepts.
//
// The old payload numbered the operator's tabs 0..m-1 in their own private
// space, overlapping the agent's own 0..n-1. An agent that picked one landed
// on a different tab, or on nothing.
func TestOperatorTakeover_ListingOnlyNamesDrivableTabs(t *testing.T) {
	m := operatorTakeoverFixture(t)

	list := &ListTabsTool{res: newFixedResolver(m)}
	out := decodeToolJSON(t, list.Execute(context.Background(), map[string]any{}))

	seen := map[int]bool{}
	for _, group := range []string{"tabs", "operator_tabs"} {
		arr, ok := out[group].([]any)
		require.True(t, ok, "%s must be an array: %v", group, out)
		for _, raw := range arr {
			entry, ok := raw.(map[string]any)
			require.True(t, ok, "every listed tab must be an object: %v", raw)
			rawIdx, ok := entry["index"].(float64)
			require.True(t, ok, "every listed tab must carry an index: %v", entry)
			idx := int(rawIdx)
			require.False(t, seen[idx],
				"index %d is listed twice across `tabs` and `operator_tabs` — the agent cannot "+
					"tell which tab it would land on", idx)
			seen[idx] = true
		}
	}
	require.Len(t, seen, 5, "two of the agent's tabs and three of the operator's: %v", out)
}

// TestOperatorTakeover_DefersWhileAHumanDrivesTheOperatorsTab is the control
// lock, on the tab the tool is about to touch.
//
// The live panel takes control on mgr.OperatorSessionID(). Before this fix
// every tool asked about the SESSION's key instead, so the answer was always
// "nobody is driving" — eleven tool descriptions promised the model they would
// stand down for a human and none could.
func TestOperatorTakeover_DefersWhileAHumanDrivesTheOperatorsTab(t *testing.T) {
	m := operatorTakeoverFixture(t)
	operatorSet := m.OperatorSessionID()
	res := newFixedResolver(m)

	idx := operatorTabIndexFromListing(t, m, 0)

	// A human takes the wheel on the operator's tabs, through the same seam
	// pkg/gateway/browser_ws.go uses.
	require.True(t, m.Live().TakeControl(operatorSet, "human-viewer"))

	switchTab := &SwitchTabTool{res: res}
	result := switchTab.Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"index": float64(idx)},
	)
	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, humanControlDeferralMarker,
		"a human is driving the operator's tab — the agent's action on THAT tab must defer")
	assert.NotContains(t, strings.ToLower(result.ForLLM), leaseDeferralMarker,
		"the human-control answer is STOP, not the lease's RETRY")

	// The lock must have been consulted BEFORE the lease, never after: a lease
	// taken here means the gates are ordered wrong (§14.2 rule 1).
	assert.False(t, leaseWasTaken(m, testKey, TabOwnerWorkspace()),
		"when a human holds control the lease must never be acquired")

	// Nothing moved.
	_, _, opActive, err := m.ListTabsState(operatorSet)
	require.NoError(t, err)
	assert.Equal(t, 2, opActive, "the deferred call must not have switched the operator's tab")

	// The lock is SCOPED to the operator's set, not global: the agent's own
	// tabs are still its own to drive while the human browses.
	own := switchTab.Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"index": float64(0)},
	)
	require.NotNil(t, own)
	assert.NotContains(t, own.ForLLM, humanControlDeferralMarker,
		"a human on the operator's tabs must not lock the agent out of its own chat's tabs")
	_, _, ownActive, err := m.ListTabsState(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 0, ownActive)

	// The gate must also hold for the tools that take NO index — the ones that
	// act on "wherever the browser already is". After a take-over that is the
	// operator's tab, and their control check has to follow it there. If it
	// keeps asking about the chat's own set, an agent that has taken over
	// drives straight through a human holding the wheel.
	m.Live().ReleaseControl(operatorSet, "human-viewer")
	idxForTakeover := operatorTabIndexFromListing(t, m, 0)
	takeover := switchTab.Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"index": float64(idxForTakeover)},
	)
	require.False(t, takeover.IsError, "%s", takeover.ForLLM)
	require.True(t, m.Live().TakeControl(operatorSet, "human-viewer"))

	openTab := &OpenTabTool{res: res}
	openResult := openTab.Execute(tools.WithAgentID(context.Background(), "jim"), map[string]any{})
	require.NotNil(t, openResult)
	assert.Contains(t, openResult.ForLLM, humanControlDeferralMarker,
		"an index-free tool must defer too once this chat is driving the operator's tabs")
	_, opTabsAfter, _, err := m.ListTabsState(operatorSet)
	require.NoError(t, err)
	assert.Len(t, opTabsAfter, 3, "the deferred call must not have opened anything")

	// Once the human lets go, the take-over proceeds.
	m.Live().ReleaseControl(operatorSet, "human-viewer")
	idx = operatorTabIndexFromListing(t, m, 0)
	after := switchTab.Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"index": float64(idx)},
	)
	require.NotNil(t, after)
	require.False(t, after.IsError, "%s", after.ForLLM)
	_, _, opActive, err = m.ListTabsState(operatorSet)
	require.NoError(t, err)
	assert.Equal(t, 0, opActive)
}

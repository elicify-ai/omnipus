package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// uat15_pinned_panel_convergence_test.go — the exact UAT-15 (agent half)
// sequence, walked in the order the E2E walks it, with the panel's tab set
// PINNED at attach the way pkg/gateway/browser_ws.go pins it.
//
// The hypothesis under test: because the panel resolves ONCE at attach while
// the agent re-resolves per tool call, the operator driving the tab in between
// could move the two apart — the panel showing one set while the agent's
// browser_type lands in another, which would look exactly like "the agent
// narrated an action it did not perform".
func TestUAT15_PinnedPanelAndAgentConvergeAcrossTheHandover(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	const chat = "chat-uat15"
	chatOwner := mustTabOwner(chat)

	// 1. openLivePanel: a brand-new chat, nothing browsed anywhere yet. This
	// is the resolution handleAttach pins for the life of the connection.
	pinned := m.PanelTabSetID(chat)

	// 2. LiveViewRegistry.Attach materialises whatever the panel resolved.
	_, err := m.Session(pinned)
	require.NoError(t, err)

	// 3. The operator drives that tab from the panel's omnibox (navigate to
	// the login form) and then hands the wheel back with Esc. Neither touches
	// tab-set ownership, so the only state that changed is "the set the panel
	// pinned now has a tab in it".
	operatorTabs, _, err := m.ListTabs(pinned)
	require.NoError(t, err)
	require.NotEmpty(t, operatorTabs, "the panel's own set must hold the tab the operator drove")

	// 4. The agent is asked, in chat, to type into that page. Its tools
	// re-resolve per call, with no knowledge of what the panel pinned.
	agentSet := sessionKey(m.key, m.focusedTabSet(chatOwner))

	require.Equal(t, pinned, agentSet,
		"the panel pinned %q at attach but the agent's browser_type would address %q — "+
			"the operator would watch an unchanged page while the tool reported success",
		pinned, agentSet)

	agentTabs, _, err := m.ListTabs(agentSet)
	require.NoError(t, err)
	require.Equal(t, operatorTabs, agentTabs,
		"panel and agent must be looking at the same tabs, byte for byte")
}

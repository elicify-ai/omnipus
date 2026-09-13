// live_standdown_release_gate_test.go — ADR-085 BROWSER-FR-029/FR-050 asserted
// where it is FELT: the agent's own tool gate.
//
// WHY THIS FILE EXISTS. ReleaseAllStoodDown already has coverage
// (TestReleaseAllStoodDown_ClearsEveryStoodDownTabSet), but that test asserts
// IsStoodDown — the registry's own bookkeeping read. The property the operator
// depends on is one step further out: after their next prompt returns the
// wheel, the agent's browser tools must actually RUN again. controlledResult is
// the single gate every one of those tools passes through, and it is the
// function that would keep deferring forever if a release cleared the
// interactive lock while leaving the FR-026a latch or the FR-047
// handover-pending state standing.
//
// ReleaseAllStoodDown is the exact call the gateway's prompt-release action
// makes (pkg/gateway/browser_ws.go::ReleaseBrowserWheelForPrompt), so this is
// the agent-side half of "the operator gets the browser back", asserted on the
// production path rather than on a stand-in.

package browser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReleaseAllStoodDown_TheAgentsToolGateStopsDeferring is BROWSER-FR-029 +
// FR-050 end to end across BOTH stand-down producers at once, because the
// prompt release must clear both or the agent is left half locked out.
//
// BDD (D-G): Given a human holds the operator's tabs AND the agent has handed
// the chat's own tab set over for a sign-in, When the operator sends a new
// prompt (there is no hand-back button — the prompt IS the hand-back), Then
// every browser tool stops deferring on both tab sets.
func TestReleaseAllStoodDown_TheAgentsToolGateStopsDeferring(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	ctx := context.Background()

	// Precondition: with nothing held, the gate lets the agent through. Without
	// this, a test that only ever observes nil after the release could pass on
	// a gate that never defers at all.
	require.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"test setup: an unheld browser must not defer")
	require.Nil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"test setup: an unheld operator tab set must not defer")

	// The two stand-downs, raised through the production calls: an agent
	// handover on the chat's own set, a human take on the operator's.
	transitioned, _ := mgr.Live().SetHandoverPending(testSessionID, "please sign in to the bank")
	require.True(t, transitioned, "test setup: the handover must be a real transition")
	require.True(t, mgr.Live().TakeControl(testOperatorSessionID, "human-viewer"),
		"test setup: the human take must succeed")

	deferredOwn := controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil)
	require.NotNil(t, deferredOwn,
		"a handed-over tab set must defer the agent's tools — otherwise the agent drives over the "+
			"operator mid-sign-in")
	require.False(t, deferredOwn.IsError, "a deferral is turn-coordination, not a tool failure")
	require.NotNil(t, deferredOwn.Deferred, "FR-012a: the deferral must be carried structurally")
	require.NotNil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"a human-held operator tab set must defer the agent's tools")

	// The operator's next prompt. This is the call ReleaseBrowserWheelForPrompt
	// makes on every browser the workspace owns.
	released := mgr.Live().ReleaseAllStoodDown()
	require.Len(t, released, 2,
		"FR-050: the prompt release must reach BOTH stood-down tab sets, not just the panel's; got %+v",
		released)

	assert.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"FR-029: after the operator's prompt the agent must be able to drive its own tab set again — "+
			"a release that cleared only lv.controller would leave this deferring for the rest of the "+
			"process's life, with nothing in the UI to say so")
	assert.Nil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"FR-050: the operator's own tab set must be drivable again too")
}

// TestReleaseAllStoodDown_LeavesOtherBrowsersAlone is the scoping half: the
// prompt release must not un-gate a hold in a DIFFERENT workspace's browser.
// Without it, the assertions above would pass equally on an implementation that
// simply cleared every lock in the process.
func TestReleaseAllStoodDown_LeavesOtherBrowsersAlone(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	other := newHandoverTestManager(t, true)
	other.key = newTestBrowsingKey(t, "some-other-workspace")
	ctx := context.Background()

	otherOwner, err := TabOwnerSession("some-other-chat")
	require.NoError(t, err)
	otherSet := sessionKey(other.key, otherOwner)
	require.True(t, other.Live().TakeControl(otherSet, "other-human"))
	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	require.Len(t, mgr.Live().ReleaseAllStoodDown(), 1,
		"the release must report only the sets THIS browser actually cleared")

	assert.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"this workspace's browser is free again")
	assert.NotNil(t, controlledResult(ctx, other, other.key, otherOwner, "browser_click", nil),
		"another workspace's held browser must stay held — one operator's prompt must never take the "+
			"wheel out of a different operator's hands")
}

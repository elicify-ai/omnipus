package browser

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// implicit_acquisition_test.go — FR-070 and FR-071.
//
// An agent acquires the operator's shared tab BY ACTING ON IT. There is no
// acquisition call, no twelfth tool, no policy entry and no wire field
// (FR-070). That simplicity has a price: the ADR-038 D6 control lock is the
// ONLY thing standing between "an agent drove the tab I had finished with" and
// "an agent drove the tab I am using right now".
//
// So FR-071 requires the BLOCKED direction to be asserted. The allowed
// direction is green on a build with no lock at all, which is why SC-025 takes
// a mutation receipt against these tests rather than trusting them on sight.

// countingLeaseManager wraps the lease table so a test can prove acquireWrite
// was NOT reached, rather than inferring it from an outcome.
//
// The seam is the table's own isHeld: if the control lock short-circuits first,
// no lease is ever taken, and the table stays empty for the whole call.
func leaseWasTaken(m *BrowserManager, key BrowsingKey, owner TabOwner) bool {
	return m.writeLeases().isHeld(sessionKey(key, owner))
}

// TestTabs_OperatorTabIsAcquiredByActing is FR-070's behavioural half.
//
// Acting on the operator's workspace-owned tab takes effect, and afterwards the
// tab is STILL workspace-owned: acting on it transfers nothing, announces
// nothing, and leaves nothing for a later call to observe. There is no
// acquisition to represent, so there is no acquisition state to get wrong.
func TestTabs_OperatorTabIsAcquiredByActing(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	owner := TabOwnerWorkspace()
	operatorSet := m.OperatorSessionID()

	// The operator opened a tab in the panel.
	_, err := m.Session(operatorSet)
	require.NoError(t, err)
	before, _, err := m.ListTabs(operatorSet)
	require.NoError(t, err)
	require.Len(t, before, 1)

	// An agent acts on it — no acquisition step of any kind.
	res := &fixedResolver{mgr: m, key: testKey, owner: owner}
	openTool := &OpenTabTool{res: res}
	result := openTool.Execute(tools.WithAgentID(context.Background(), "jim"), map[string]any{})
	require.NotNil(t, result)
	require.False(t, result.IsError, "acting on the operator's tab must simply work: %s", result.ForLLM)

	// It took effect...
	after, _, err := m.ListTabs(operatorSet)
	require.NoError(t, err)
	require.Len(t, after, 2, "the agent's action must have taken effect on the operator's tab set")

	// ...and the set is STILL the operator's, with no transfer field in the
	// body and no ownership state anywhere to have changed.
	require.Equal(t, operatorSet, m.OperatorSessionID(),
		"acting on the operator's tab must not move it to the acting agent or session")
	require.True(t, m.sessionExists(operatorSet))
	for _, word := range []string{"acquired", "transfer", "owner", "took_control", "handover"} {
		require.NotContains(t, strings.ToLower(result.ForLLM), word,
			"acquisition is implicit and has NO representation (FR-070); a %q key in the result "+
				"body is a surface, and surfaces get depended on", word)
	}
}

// TestImplicitAcquisition_BlockedWhileHumanControls is FR-071, in five cases.
//
// This is the test SC-025's receipt is taken against: with
// LiveViewRegistry.IsControlled forced to return false it MUST go red. If it
// stays green, the control lock is dead and every lease test in this package is
// vacuous, because the mitigation they all compose with is not running.
func TestImplicitAcquisition_BlockedWhileHumanControls(t *testing.T) {
	owner := TabOwnerWorkspace()
	operatorSet := sessionKey(testKey, owner)

	newFixture := func(t *testing.T) (*tools.ToolRegistry, *BrowserManager) {
		t.Helper()
		registry := tools.NewToolRegistry()
		mgr := registerToolsForTestAs(t, registry, controlTestCfg(t),
			security.NewSSRFChecker([]string{"127.0.0.1"}),
			func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
		t.Cleanup(mgr.Shutdown)
		mgr.cfg.LeaseWait = 20 * time.Millisecond
		return registry, mgr
	}

	// (a) The lock is held on the resolved key: the call defers, and NO lease
	//     is taken — the two gates are ordered, not merely both present.
	t.Run("a lock held on the resolved key blocks, and no lease is taken", func(t *testing.T) {
		registry, mgr := newFixture(t)
		require.True(t, mgr.Live().TakeControl(operatorSet, "human-viewer"))

		navigate, ok := registry.Get("browser_navigate")
		require.True(t, ok)
		result := navigate.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1/x"})
		require.NotNil(t, result)
		require.Contains(t, result.ForLLM, humanControlDeferralMarker,
			"a human holds the wheel — implicit acquisition MUST be blocked here, and this is the "+
				"only thing that blocks it")
		require.False(t, leaseWasTaken(mgr, testKey, owner),
			"when a human holds control the lease must NEVER be acquired (FR-022) — a lease taken "+
				"here means controlledResult ran after leaseWrite, or not at all")
	})

	// (b) The lock is consulted against the RESOLVED key. A lock held on a
	//     DIFFERENT key must not block, and a call whose resolved key IS the
	//     locked one must. This is what fails if controlledResult goes back to
	//     asking about one fixed session id.
	t.Run("the lock is consulted against the resolved key", func(t *testing.T) {
		registry, mgr := newFixture(t)

		otherOwner, err := TabOwnerSession("some-other-chat")
		require.NoError(t, err)
		require.True(t, mgr.Live().TakeControl(sessionKey(testKey, otherOwner), "human-viewer"))

		navigate, ok := registry.Get("browser_navigate")
		require.True(t, ok)
		result := navigate.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1/x"})
		require.NotNil(t, result)
		require.NotContains(t, result.ForLLM, humanControlDeferralMarker,
			"a lock on ANOTHER tab set must not block this call; blocking here means the lock is "+
				"being consulted globally rather than against the resolved key")

		// Now lock the set this call actually resolves — it must block.
		require.True(t, mgr.Live().TakeControl(operatorSet, "human-viewer"))
		result = navigate.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1/x"})
		require.NotNil(t, result)
		require.Contains(t, result.ForLLM, humanControlDeferralMarker)
	})

	// (c) Lock free: the call proceeds. Without this the suite would pass on a
	//     build that blocked everything.
	t.Run("an unlocked tab proceeds", func(t *testing.T) {
		registry, mgr := newFixture(t)
		require.False(t, mgr.Live().IsControlled(operatorSet))

		navigate, ok := registry.Get("browser_navigate")
		require.True(t, ok)
		result := navigate.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1/x"})
		require.NotNil(t, result)
		require.NotContains(t, result.ForLLM, humanControlDeferralMarker,
			"with nobody holding the wheel the call must proceed")
	})

	// (d) Lock held AND lease held elsewhere: the reason must be the CONTROL
	//     one, which is what proves the gate order rather than merely that both
	//     gates exist.
	t.Run("with both gates blocking, the human-control reason wins", func(t *testing.T) {
		registry, mgr := newFixture(t)
		require.True(t, mgr.Live().TakeControl(operatorSet, "human-viewer"))
		release, ok, _ := mgr.acquireWrite(context.Background(), testKey, owner, "another-turn")
		require.True(t, ok)
		defer release()

		navigate, found := registry.Get("browser_navigate")
		require.True(t, found)
		result := navigate.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1/x"})
		require.NotNil(t, result)
		require.Contains(t, result.ForLLM, humanControlDeferralMarker,
			"a human is present: the answer must be STOP, not the lease's RETRY")
		require.NotContains(t, result.ForLLM, leaseDeferralMarker,
			"the two deferrals mean opposite things; reporting the lease one here tells the model to "+
				"retry against a tab a person is driving")
	})

	// (e) The exemption's price, against the tool that actually pays it.
	//
	// THIS SUBTEST USED TO DRIVE browser_list_tabs, and it could not fail.
	// It was written when browser_handle_dialog was still D2's and unbuilt, so
	// it reached for "the closest live analogue" among the read-only exempt
	// tools. But browser_list_tabs enumerates a slice and returns; it has no
	// code path that could take a lease or move a tab set, so asserting that
	// it does neither was a statement about a tool that could not have done
	// either. The property was never in question for the stand-in, and was
	// never checked for the real subject.
	//
	// browser_handle_dialog now exists (tools_dialog.go) and it is the one
	// tool exempt from BOTH gates while still ACTING on the page: FR-035 makes
	// it the recovery verb, because the click that raised the dialog still
	// holds the lease and the human staring at the wedged tab has no button
	// either, so gating it behind the mechanisms the fault itself disables is
	// a deadlock rather than a safety property.
	//
	// That exemption is what has to be paid for here. A write-shaped tool that
	// answers while a human drives, and takes no lease doing it, is exactly
	// the shape that could become an implicit-acquisition path by accident —
	// and FR-070's whole design is that acting on the operator's tab transfers
	// nothing. Both gates are stacked against it deliberately: a human holds
	// control AND another turn holds the write lease, so a tool that consulted
	// either would defer instead of answering.
	t.Run("the doubly-exempt dialog verb answers past both gates and moves no ownership", func(t *testing.T) {
		registry, mgr := newFixture(t)
		fakeSession(t, mgr, operatorSet)
		seedDialogOnActiveTab(t, mgr, operatorSet, &PendingDialog{Type: "alert", Message: "are you sure?"})

		// Gate 1: a human is driving the operator's tab.
		require.True(t, mgr.Live().TakeControl(operatorSet, "human-viewer"))
		// Gate 2: another turn holds the write lease on that same set — this
		// is the wedge FR-035 describes, not a contrived one.
		release, leased, _ := mgr.acquireWrite(context.Background(), testKey, owner, "the-turn-that-wedged-it")
		require.True(t, leased, "test setup: the blocking lease must be held")
		defer release()

		before := mgr.OperatorSessionID()
		dialog, ok := registry.Get("browser_handle_dialog")
		require.True(t, ok, "browser_handle_dialog must be registered — it is the subject of this assertion")

		result := dialog.Execute(context.Background(), map[string]any{"accept": false})
		require.NotNil(t, result)

		// It ANSWERS. Without this the three assertions below would be
		// satisfied by a tool that deferred and did nothing at all.
		require.NotContains(t, result.ForLLM, humanControlDeferralMarker,
			"browser_handle_dialog deferred to the human holding the live view. It is the verb that "+
				"unwedges the tab, and the human has no button either (FR-035) — deferring here "+
				"leaves the tab stuck for both of them")
		require.NotContains(t, result.ForLLM, leaseDeferralMarker,
			"browser_handle_dialog deferred to the write lease. The lease is held by the very call "+
				"the dialog is blocking, so waiting for it is a deadlock by construction")

		// NOTE ON WHAT IS *NOT* ASSERTED HERE, because getting it wrong is
		// easy and silent: leaseWasTaken() cannot be used in this subtest.
		// The setup above holds the lease itself, so isHeld() is true whether
		// or not the tool touched it, and the assertion would be reporting the
		// fixture rather than the tool. The lease exemption is proved instead
		// by the deferral marker being ABSENT while another turn holds the
		// lease and mgr.cfg.LeaseWait is 20ms: a tool that tried to take it
		// would block for that budget and say so.
		require.Equal(t, before, mgr.OperatorSessionID(),
			"the operator's tab set moved. Acting on it transfers nothing (FR-070) — and a "+
				"write-shaped tool that answers while a human drives is the likeliest thing to "+
				"become an implicit-acquisition path by accident")
	})
}

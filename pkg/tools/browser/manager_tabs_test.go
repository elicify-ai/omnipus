// manager_tabs_test.go: tests for tabs - open, close, switch, list, and their bookkeeping, including creating tabs, adopting Chrome-spawned targets, reconciling the tab set, and the tab-open memory gate.

package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from manager.go tests 2026-09-15 ---

// TestSwitchTab_FocusEmulatesTheTabItSwitchesTo is the core regression. Before
// the fix the switched-to tab was only brought to front, so focusTreatment
// reports "unknown" (bringToFront with no focus emulation) and this fails.
func TestSwitchTab_FocusEmulatesTheTabItSwitchesTo(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	wantCtx := m.sessions[testSessionID].tabs[0].ctx
	m.mu.Unlock()

	before := len(rec.calls())
	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	fg := rec.calls()
	require.Len(t, fg, before+1,
		"the switched-to tab must get the FULL foreground treatment (bringToFront AND focus "+
			"emulation) — bringToFront alone leaves the tab the encoder re-binds to in a "+
			"different rendering regime than the one capture start established")
	assert.Same(t, wantCtx, fg[len(fg)-1], "the treatment must land on the tab switched TO")
}

// TestSwitchTab_ReleasesFocusEmulationOnTheTabItLeaves — the measured half.
// Without this the previous tab stays convinced it is foreground and keeps
// compositing at ~30 fps in the background, for every tab the agent ever
// visits.
func TestSwitchTab_ReleasesFocusEmulationOnTheTabItLeaves(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	leavingCtx := se.tabs[se.activeIdx].ctx
	m.mu.Unlock()

	beforeBlur := len(rec.blurCalls())
	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	blurred := rec.blurCalls()
	require.Len(t, blurred, beforeBlur+1,
		"the tab being left must have its focus emulation cleared — measured 25–35 rAF/s of "+
			"pointless background compositing when it is not")
	assert.Same(t, leavingCtx, blurred[len(blurred)-1],
		"the release must land on the tab being left, never on the new one")
}

// TestSwitchTab_ToTheSameTabDoesNotReleaseItsOwnFocus — switching to the tab
// that is already active must not blur the tab it just foregrounded. Getting
// this wrong would leave the ACTIVE tab un-emulated: the exact defect, inverted.
func TestSwitchTab_ToTheSameTabDoesNotReleaseItsOwnFocus(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // active index is now 1
	require.NoError(t, err)

	beforeBlur := len(rec.blurCalls())
	_, err = m.SwitchTab(testSessionID, 1) // switch to the ALREADY-active tab
	require.NoError(t, err)

	assert.Len(t, rec.blurCalls(), beforeBlur,
		"a no-op switch must not release the focus of the tab that stays active")
}

// TestOpenTab_FocusEmulatesTheNewTabAndReleasesThePrevious — browser_open_tab
// moves the active tab exactly as a switch does, and fires the same
// tabs-changed → recapture chain. Before the fix OpenTab issued no CDP focus
// call whatsoever, so the encoder re-bound to a tab Chrome had never been told
// about.
func TestOpenTab_FocusEmulatesTheNewTabAndReleasesThePrevious(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	previousCtx := m.sessions[testSessionID].tabs[0].ctx
	m.mu.Unlock()

	beforeFg, beforeBlur := len(rec.calls()), len(rec.blurCalls())
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	fg, blurred := rec.calls(), rec.blurCalls()
	require.Len(t, fg, beforeFg+1, "the newly-opened tab must get the foreground treatment")
	require.Len(t, blurred, beforeBlur+1, "the tab it displaced must have its focus emulation released")

	newCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	assert.Same(t, newCtx, fg[len(fg)-1],
		"the foreground treatment must land on the tab Session() now resolves — the one the "+
			"encoder's chrome.tabs.query will re-bind to")
	assert.Same(t, previousCtx, blurred[len(blurred)-1], "the release must land on the displaced tab")
}

// TestReleaseTabFocusInChrome_SkipsDeadContexts — a tab whose context already
// died (closed, browser crash) must not be dispatched to chromedp: in
// production that is a guaranteed PageTimeout stall for a tab that cannot be
// focused either way. Mirrors the same guarantee activateTabInChrome has.
func TestReleaseTabFocusInChrome_SkipsDeadContexts(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	before := len(rec.blurCalls())
	m.releaseTabFocusInChrome(dead, testSessionID)
	m.releaseTabFocusInChrome(nil, testSessionID)
	assert.Len(t, rec.blurCalls(), before,
		"a canceled or nil tab context must be skipped, not dispatched to CDP")
}

// TestCloseTab_ActivatesTheTabThatBecomesActive closes the third path that
// moved activeIdx without telling Chrome (review F9 follow-up, 2026-08-13).
// SwitchTab and OpenTab both activate; CloseTab settled activeIdx and fired
// notifyTabsChanged — which triggers the WebRTC recapture — leaving the
// encoder's chrome.tabs.query({active:true}) target to whatever Chrome
// happened to pick when the target closed. That is the same silent
// capture-follows-the-wrong-tab failure activateTabInChrome exists to
// prevent (see its doc comment, root-caused live 2026-08-03).
func TestCloseTab_ActivatesTheTabThatBecomesActive(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// Close the ACTIVE tab (index 2, opened last): index 1 becomes active.
	m.mu.Lock()
	require.Len(t, m.sessions[testSessionID].tabs, 3)
	require.Equal(t, 2, m.sessions[testSessionID].activeIdx)
	wantCtx := m.sessions[testSessionID].tabs[1].ctx
	m.mu.Unlock()

	before := len(rec.calls())
	_, activeIdx, err := m.CloseTab(testSessionID, 2)
	require.NoError(t, err)
	require.Equal(t, 1, activeIdx)

	calls := rec.calls()
	require.Len(t, calls, before+1,
		"closing the active tab must tell Chrome which tab is active NOW — otherwise the "+
			"recapture that notifyTabsChanged fires resolves its capture target from Chrome's "+
			"own post-close guess, which nothing here ever verified agrees with activeIdx")
	assert.Same(t, wantCtx, calls[len(calls)-1],
		"the activation must land on the tab that BECAME active, not the closed one")
}

// TestCreateTab_CallsStartPageNavigation closes the gap the tests above cannot:
// they exercise navigateNewTabToStartPage DIRECTLY, so they still pass if the
// call site inside createTab is deleted — which is precisely how the start page
// shipped inert the first time (unit tests green, brand-new tab still
// about:blank on live UAT v39).
//
// createTab's real body performs a CDP attach that cannot run without Chrome,
// so rather than add a second production seam purely for this test, this
// asserts on the SOURCE: createTab must contain the call. A source assertion is
// weak by nature, but it fails loudly on the one edit that caused the outage —
// removing the wiring — which no behavioral test in this file can see.
//
// The scan reads the whole manager*.go family, not manager.go by name: createTab
// lives in whichever sibling holds the tab job after the split, and a by-name
// scan would keep passing while guarding nothing.
func TestCreateTab_CallsStartPageNavigation(t *testing.T) {
	body := readManagerSourcesForTest(t)

	fromCreateTab := sliceFromMarkerForTest(t, body, "func (m *BrowserManager) createTab(")
	end := strings.Index(fromCreateTab, "\nfunc ")
	require.Positive(t, end, "createTab must be followed by another function")

	require.Contains(t, fromCreateTab[:end], "navigateNewTabToStartPage(",
		"createTab must call navigateNewTabToStartPage — without it every new tab opens "+
			"about:blank and the start page is inert in production (UAT v39 regression)")
}

// --- test 8 (FR-013) --------------------------------------------------------

// TestListTabsState_ThreeDistinctStates builds each of the three states
// directly on a manager and asserts they are pairwise distinguishable — both
// in ListTabsState's own return and in the payload the model actually reads.
//
// The oracle is §10.2's dataset table, not the implementation:
//
//	no `sessions` entry            -> TabStateNoContext, empty tabs
//	browser live, 2 tabs           -> TabStateOpen,      2 tabs
//	browser live, len(se.tabs)==0  -> TabStateEmpty,     empty tabs
func TestListTabsState_ThreeDistinctStates(t *testing.T) {
	// --- no_context: nothing has ever browsed under this key+owner.
	mNone := newTestManagerWithFakeTabs(t)
	state, tabs, _, err := mNone.ListTabsState(testSessionID)
	require.NoError(t, err, "an absent browsing context is a STATE, never an error")
	assert.Equal(t, TabStateNoContext, state,
		"a manager with no sessions entry must report no_context — reporting an empty tab set here "+
			"is the exact lie FR-013 exists to remove")
	assert.Empty(t, tabs)

	// --- open: a live context with two tabs.
	mOpen := newTestManagerWithFakeTabs(t)
	_, err = mOpen.Session(testSessionID) // creates the context with its first tab
	require.NoError(t, err)
	_, err = mOpen.OpenTab(testSessionID)
	require.NoError(t, err)
	state, tabs, activeIdx, err := mOpen.ListTabsState(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, TabStateOpen, state)
	assert.Len(t, tabs, 2, "§10.2: browser live with 2 tabs")
	assert.GreaterOrEqual(t, activeIdx, 0)

	// --- empty: a live context whose tab set is momentarily zero. Reachable
	// in production through CloseTab's last-tab path when the replacement tab
	// fails to open.
	mEmpty := newTestManagerWithFakeTabs(t)
	_, err = mEmpty.Session(testSessionID)
	require.NoError(t, err)
	mEmpty.mu.Lock()
	mEmpty.sessions[testSessionID].tabs = nil
	mEmpty.mu.Unlock()
	state, tabs, _, err = mEmpty.ListTabsState(testSessionID)
	require.NoError(t, err, "an empty tab set is a STATE, never an error")
	assert.Equal(t, TabStateEmpty, state,
		"a live context with zero tabs must be distinguishable from no context at all")
	assert.Empty(t, tabs)

	// --- the states are pairwise distinct as MODEL-VISIBLE payloads, which is
	// the property that actually matters: three different situations must not
	// render to two different answers.
	payloads := map[string]string{}
	for name, m := range map[string]*BrowserManager{
		"no_context": mNone, "open": mOpen, "empty": mEmpty,
	} {
		tool := &ListTabsTool{res: newFixedResolver(m)}
		res := tool.Execute(context.Background(), map[string]any{})
		require.False(t, res.IsError, "%s: %s", name, res.ForLLM)
		payloads[name] = res.ForLLM
	}
	assert.NotEqual(t, payloads["no_context"], payloads["empty"],
		"no_context and empty must not render identically — that identity IS the reported defect")
	assert.NotEqual(t, payloads["no_context"], payloads["open"])
	assert.NotEqual(t, payloads["empty"], payloads["open"])

	// --- the state set is CLOSED at exactly three, with no "denied" member
	// (ADR D1.12). Enumerated from the package's own source so a fourth
	// constant added later fails here rather than shipping unnoticed.
	assert.ElementsMatch(t,
		[]string{"no_context", "open", "empty"},
		declaredTabStateValues(t),
		"TabState must be exactly {no_context, open, empty}; D1.12 withdrew \"denied\" as unreachable, "+
			"because a policy-denied agent never receives the tool at all (see test 11)")
}

// TestListTabs_DelegatesAndNeverReturnsSilentEmpty pins the §5 non-behaviour:
// once ListTabsState exists, ListTabs must not carry its own "missing context
// looks like an empty success" branch. It delegates, so the two agree by
// construction rather than by two copies of the same logic staying in step.
func TestListTabs_DelegatesAndNeverReturnsSilentEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *BrowserManager
	}{
		{"no_context", func(t *testing.T) *BrowserManager { return newTestManagerWithFakeTabs(t) }},
		{"open", func(t *testing.T) *BrowserManager {
			m := newTestManagerWithFakeTabs(t)
			_, err := m.Session(testSessionID)
			require.NoError(t, err)
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			wantState, wantTabs, wantIdx, wantErr := m.ListTabsState(testSessionID)
			gotTabs, gotIdx, gotErr := m.ListTabs(testSessionID)
			assert.Equal(t, wantErr, gotErr)
			assert.Equal(t, wantTabs, gotTabs)
			assert.Equal(t, wantIdx, gotIdx)
			assert.NotEmpty(t, string(wantState))
		})
	}

	// The structural half: the literal `return nil, 0, nil` — the silent
	// empty-success shape — must not exist anywhere in the manager*.go family
	// any more. The scan reads the whole family, not manager.go by name, so a
	// ListTabs that moves between siblings cannot smuggle the shape back in.
	assert.NotContains(t, readManagerSourcesForTest(t), "return nil, 0, nil",
		"the manager*.go family must not return the silent nil,0,nil empty-success shape once ListTabsState exists (§5)")
}

// --- FR-080's payload half --------------------------------------------------

// TestListTabs_PayloadLabelsSessionAndWorkspaceSets asserts the payload says
// WHOSE tabs it is reporting: this chat session's own set, and — separately
// labelled — the workspace-owned set the operator opened.
//
// The negative half is the one that matters. A build that merged the two sets
// into one array, or that reported only the session's and called it "the
// tabs", would pass any assertion that only counts tabs.
func TestListTabs_PayloadLabelsSessionAndWorkspaceSets(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	// The operator opens a tab through the live panel: the workspace-owned set.
	operatorSet := sessionKey(testKey, TabOwnerWorkspace())
	_, err := m.Session(operatorSet)
	require.NoError(t, err)

	// This chat session opens two of its own.
	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	tool := &ListTabsTool{res: newFixedResolver(m)}
	out := decodeToolJSON(t, tool.Execute(context.Background(), map[string]any{}))

	assert.Equal(t, "this_chat_session", out["tabs_owner"],
		"the payload must name whose set `tabs` is")
	sessionTabs, ok := out["tabs"].([]any)
	require.True(t, ok, "tabs must be an array: %v", out)
	assert.Len(t, sessionTabs, 2, "`tabs` is THIS session's set — 2 tabs, not the operator's 1 as well")

	operatorTabs, ok := out["operator_tabs"].([]any)
	require.True(t, ok, "the operator's workspace-owned set must be reported separately: %v", out)
	assert.Len(t, operatorTabs, 1)
	assert.Equal(t, string(TabStateOpen), out["operator_tabs_state"])
	assert.Contains(t, out, "tab_ownership", "the payload must say in words which set is which")

	// A second chat session on the SAME browser sees its own (absent) set and
	// the SAME workspace-owned set — never the first session's tabs.
	otherOwner, err := TabOwnerSession("01OTHERSESSION")
	require.NoError(t, err)
	otherTool := &ListTabsTool{res: &fixedResolver{mgr: m, key: testKey, owner: otherOwner}}
	other := decodeToolJSON(t, otherTool.Execute(context.Background(), map[string]any{}))
	assert.Equal(t, string(TabStateNoContext), other["state"],
		"a session that has never browsed sees no_context — not the other session's tabs")
	assert.Empty(t, other["tabs"])
	otherOperatorTabs, ok := other["operator_tabs"].([]any)
	require.True(t, ok)
	assert.Len(t, otherOperatorTabs, 1,
		"the workspace-owned set is visible to every session on the workspace, labelled as the operator's")

	// A turn that IS the operator gets one set, not the same set under two
	// names — an invented second tab set would be its own lie.
	opTool := &ListTabsTool{res: newOperatorResolver(m)}
	op := decodeToolJSON(t, opTool.Execute(context.Background(), map[string]any{}))
	assert.Equal(t, "workspace_operator", op["tabs_owner"])
	assert.NotContains(t, op, "operator_tabs",
		"an operator turn must not have its own set echoed back as a second, separate one")
}

// --- test 11 (FR-014) -------------------------------------------------------

// TestListTabs_DeniedAgentNeverReachesTool asserts the ABSENCE that ADR D1.12
// rules on: a policy-denied agent is never shown browser_list_tabs, so
// Execute is never entered and there is no payload to shape.
//
// FilterToolsByPolicy `continue`s past a deny verdict rather than substituting
// a refusal tool, so the assertion is on the tool DEFINITIONS the model sees.
// There is deliberately no ModelMessage assertion: nothing runs, so nothing
// speaks. This is why TabState has no "denied" member.
func TestListTabs_DeniedAgentNeverReachesTool(t *testing.T) {
	registry := tools.NewToolRegistry()
	m := newTestManagerWithFakeTabs(t)
	require.NoError(t, RegisterTools(registry, newFixedResolver(m), true, t.TempDir(), true))

	all := registry.GetAll()

	// Sanity: with an allow policy the tool IS present. Without this the
	// absence assertion below could pass because nothing was ever registered.
	allowed, _ := tools.FilterToolsByPolicy(all, "custom", &tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"browser_list_tabs": config.ToolPolicyAllow},
	})
	require.True(t, containsToolNamed(allowed, "browser_list_tabs"),
		"precondition: an allowed agent must actually see browser_list_tabs")

	denied, deniedPolicies := tools.FilterToolsByPolicy(all, "custom", &tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"browser_list_tabs": config.ToolPolicyDeny},
	})
	assert.NotContains(t, deniedPolicies, "browser_list_tabs",
		"a denied tool must not even appear in the resolved policy map")
	assert.False(t, containsToolNamed(denied, "browser_list_tabs"),
		"a policy-denied agent must never receive the browser_list_tabs definition — it answers from "+
			"the tool's absence, which is why there is no \"denied\" TabState (ADR D1.12)")
}

// A normal tab switch focuses the selected target, measures it, then sends
// one qualified recapture. The encoder binds the explicit target ID.
func TestSwitchTab_OrdinarySwitchReassertsForegroundBeforeTheControlFrame(t *testing.T) {
	m, _, _, ledger, order := newAttachedLiveManager(t)

	_, err := m.SwitchTab(testSessionID, 0) // a real move
	require.NoError(t, err)

	require.Eventually(t, func() bool { return ledger.recaptures() == 1 }, 3*time.Second, 5*time.Millisecond,
		"an ordinary tab switch owes the encoder exactly one recapture")
	require.Eventually(t, func() bool { return len(order.snapshot()) == 3 }, 3*time.Second, 5*time.Millisecond,
		"the selected target must be focused and measured before recapture")

	assert.Equal(t, []string{"focus", "measure", "control:recapture"}, order.snapshot(),
		"the encoder must receive measured geometry after the selected target is focused")
}

// One user action, one encoder rebuild. With a viewport already applied, the
// ordinary switch used to recapture TWICE: onTabsChanged's immediate,
// geometry-less Recapture(), and then the post-re-apply RecaptureAt a few
// hundred ms later. Two full encoder rebuilds and two PLI bursts per tab
// click, worst exactly where it hurts most. The first of the two could never
// have been the right one anyway — it re-binds the stream before the new
// target has been given the panel's size and per-target sharpness.
func TestSwitchTab_WithAViewportAppliedRecapturesOnceWithTheVerifiedSize(t *testing.T) {
	m, lv, _, ledger, _ := newAttachedLiveManager(t)

	lv.mu.Lock()
	lv.lastRequestedW, lv.lastRequestedH, lv.lastRequestedScale = 633, 686, 2
	previous := lv.runCDP
	lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case layoutMetricsAction:
			*a.w, *a.h = 633, 686
		case viewportFrameGeometryAction:
			*a.width, *a.height, *a.scale = 633, 686, 2
		case documentPaintAction, chromedp.ActionFunc:
			return previous(ctx, timeout, actions...)
		}
		return nil
	}
	lv.mu.Unlock()

	_, err := m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return ledger.recaptures() >= 1 }, 3*time.Second, 5*time.Millisecond,
		"the picture must follow the tab")
	time.Sleep(300 * time.Millisecond) // long enough for a second rebuild to show up

	assert.Equal(t, 1, ledger.recaptures(),
		"one tab click must cost exactly one encoder rebuild, not one before the re-apply and one after")
	w, h := ledger.lastDims()
	assert.Equal(t, 633, w, "and it must carry the CDP-verified size the new tab actually reached")
	assert.Equal(t, 686, h)
}

// Coalescing is only honest if the surviving pass uses the LATEST caller's
// geometry. A pass that replayed the geometry captured when the worker
// started would hand the encoder the size of a tab the user has already left
// — the same class of stale-measurement bug as F1's cache write, one layer
// down.
func TestRecaptureForTabChangeAt_CoalescedPassUsesTheLatestGeometry(t *testing.T) {
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(new(int32), nil))

	release := make(chan struct{})
	var once sync.Once
	cs.mu.Lock()
	cs.foregroundAssertFn = func(context.Context) bool {
		once.Do(func() { <-release }) // hold the worker inside pass 1
		return true
	}
	cs.mu.Unlock()
	ledger := &ingestLedger{}
	cs.BindIngest(func(action string, _ *string, w, h int, _ int) error {
		ledger.mu.Lock()
		ledger.actions = append(ledger.actions, action)
		ledger.dims = append(ledger.dims, [2]int{w, h})
		ledger.mu.Unlock()
		return nil
	}, func() {})

	cs.RecaptureForTabChangeAt(633, 686) // starts the worker; it blocks
	cs.RecaptureForTabChangeAt(640, 480) // coalesces — this is where the user ended up
	close(release)

	require.Eventually(t, func() bool { return ledger.recaptures() == 2 }, 3*time.Second, 5*time.Millisecond,
		"the in-flight pass plus one coalesced re-run")
	w, h := ledger.lastDims()
	assert.Equal(t, 640, w, "the surviving pass must carry the geometry of the tab the user actually ended on")
	assert.Equal(t, 480, h)
}

// FR-016 applies to legacy callers too: shutdown retires their pending start.
func TestOpenTabLocalStartupShutdownRetiresLaunch(t *testing.T) {
	m, entered, release, disposed := blockedLocalStartupManager(t)
	done := make(chan error, 1)
	go func() { _, err := m.OpenTab(testSessionID); done <- err }()
	launch := <-entered
	m.Shutdown()
	canceled := false
	select {
	case <-launch.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	release()
	err := <-done
	require.True(t, canceled, "shutdown must cancel a legacy tab-open startup")
	require.True(t, errors.Is(err, errBrowserSessionChanged), "retired startup error: %v", err)
	assertLocalStartupDrained(t, m, disposed)
	require.False(t, m.Started(), "startup must not resurrect the stopped manager")
	require.Equal(t, 0, m.TotalOpenTabs())
}

func TestOpenTab_FailedLoad_DoesNotStrandTheTabOnTheTarget(t *testing.T) {
	skipIfNoBrowser(t)

	srv := stallingTestServer(t)
	cfg := testBrowserCfg(t)
	cfg.PageTimeout = 3 * time.Second

	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := registerToolsForTest(t, registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	openTool := mustGetTool(t, registry, "browser_open_tab")
	result := openTool.Execute(context.Background(), map[string]any{"url": srv.URL + "/stall"})
	require.NotNil(t, result)
	require.True(t, result.IsError, "a stalled load must report an error; got: %s", result.ForLLM)

	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	readCtx, cancel := context.WithTimeout(tabCtx, 10*time.Second)
	defer cancel()

	var location string
	require.NoError(t, chromedp.Run(readCtx, chromedp.Location(&location)))
	require.NotContains(t, location, "/stall",
		"the new tab was left parked on the failed target")
}

// TestDeHeadlessUA verifies the User-Agent de-Headless rewrite used by
// applyStealth removes the biggest automation giveaway (the "HeadlessChrome"
// / "Headless" token) while leaving a normal UA untouched.
func TestDeHeadlessUA(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "chrome-headless-shell UA",
			in:   "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/125.0.0.0 Safari/537.36",
			want: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		},
		{
			name: "already-clean Chrome UA is unchanged",
			in:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
			want: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deHeadlessUA(tc.in); got != tc.want {
				t.Fatalf("deHeadlessUA(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got := deHeadlessUA(tc.in); containsHeadless(got) {
				t.Errorf("deHeadlessUA output still contains a Headless token: %q", got)
			}
		})
	}
}

// TestSwitchTab_ActivatesNewTabInChrome is THE regression test for the
// three-way desync. Without SwitchTab's activateTabInChrome call it fails:
// zero activations are recorded, which is exactly the state that let the
// encoder's chrome.tabs.query keep resolving the previous tab.
func TestSwitchTab_ActivatesNewTabInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// Resolve the context of the tab we are about to switch to, so the
	// assertion below proves the RIGHT tab was activated — not merely that
	// some activation happened.
	m.mu.Lock()
	wantCtx := m.sessions[testSessionID].tabs[1].ctx
	m.mu.Unlock()

	before := len(rec.calls())
	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	calls := rec.calls()
	require.Greater(t, len(calls), before,
		"SwitchTab must activate the newly-active tab in Chrome; without it the WebRTC "+
			"encoder's chrome.tabs.query({active:true}) keeps resolving the PREVIOUS tab "+
			"and the stream silently never moves (live-measured 2026-08-03)")
	assert.Same(t, wantCtx, calls[len(calls)-1],
		"the activation must target the tab that was just switched TO")
}

// TestSwitchTab_ActivatesBeforeNotifyingTabsChanged pins the ORDERING that
// makes the fix work. The tabs-changed callback is what triggers the WebRTC
// recapture; if activation ran after it, the recapture would still race a
// stale active-tab notion in Chrome and could re-bind to the old tab — the
// original bug, merely narrowed to a race window.
func TestSwitchTab_ActivatesBeforeNotifyingTabsChanged(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	var mu sync.Mutex
	var order []string
	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) != "foreground" {
			return nil // the release-of-focus half; ordering here is about activation
		}
		mu.Lock()
		order = append(order, "activate")
		mu.Unlock()
		return nil
	}
	m.SetTabsChangedFunc(func(string, []Tab, int) {
		mu.Lock()
		order = append(order, "tabsChanged")
		mu.Unlock()
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	mu.Lock()
	order = nil // ignore setup-time callbacks; only the switch matters
	mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"activate", "tabsChanged"}, order,
		"Chrome must already agree about the active tab BEFORE the tabs-changed "+
			"callback fires the WebRTC recapture, or the recapture races a stale "+
			"active-tab notion and can re-bind to the old tab")
}

// TestSwitchTab_ActivationFailureIsNonFatal — activation is best-effort. The
// switch is already recorded in se.activeIdx, so every server-side consumer
// (Session(), tool calls, the JPEG path) still follows the new tab correctly;
// only the WebRTC capture's own tab resolution degrades. A failure here must
// never turn a successful switch into an error the user sees.
func TestSwitchTab_ActivationFailureIsNonFatal(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)
	rec.err = errors.New("bringToFront exploded")

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	tab, err := m.SwitchTab(testSessionID, 1)
	require.NoError(t, err, "a failed tab activation must not fail the switch itself")
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	// The authoritative server-side state must still reflect the switch.
	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

// TestSwitchTab_DoesNotActivateOnLookupFailure — an out-of-range or unknown
// switch must not touch Chrome at all. Activating on a rejected switch would
// steal focus toward a tab the caller never successfully selected.
func TestSwitchTab_DoesNotActivateOnLookupFailure(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	before := len(rec.calls())

	_, err = m.SwitchTab(testSessionID, 99)
	require.Error(t, err)
	_, err = m.SwitchTab("never-opened", 0)
	require.Error(t, err)

	assert.Len(t, rec.calls(), before,
		"a rejected switch must never activate a tab in Chrome")
}

// TestSwitchTab_ActivatesUnderNoManagerLock guards the ADR-038 rule every CDP
// call in this file follows. activateTabInChrome issues a real, blocking
// chromedp.Run in production; holding m.mu across it would deadlock any
// concurrent manager call. Calling back into the manager from inside the
// activation hook is the same trick TestSetTabsChangedFunc uses — if the lock
// were held, this test would hang rather than fail.
func TestSwitchTab_ActivatesUnderNoManagerLock(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	var reentered atomic.Bool
	m.tabFocusFn = func(context.Context, ...chromedp.Action) error {
		// Would deadlock if SwitchTab held m.mu across the activation.
		if _, _, err := m.ListTabs(testSessionID); err == nil {
			reentered.Store(true)
		}
		return nil
	}

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.SwitchTab(testSessionID, 1)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SwitchTab deadlocked — activateTabInChrome must run with NO BrowserManager lock held (ADR-038)")
	}
	assert.True(t, reentered.Load(), "the activation hook should have been able to re-enter the manager")
}

// TestSwitchTab_SkipsActivationForDeadContext — a tab whose context already
// died (browser crash, tab closed out from under us) must not be handed to
// chromedp: in production that is a guaranteed PageTimeout stall for a tab
// that cannot be brought to front anyway.
func TestSwitchTab_SkipsActivationForDeadContext(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	before := len(rec.calls())
	m.activateTabInChrome(dead, testSessionID, 0)
	assert.Len(t, rec.calls(), before, "a canceled tab context must be skipped, not dispatched to CDP")

	// A nil context must be equally inert.
	m.activateTabInChrome(nil, testSessionID, 0)
	assert.Len(t, rec.calls(), before, "a nil tab context must be skipped")
}

// TestRunTabFocusCDP_NonChromedpContextDoesNotAllocate is the production
// half of the dead/nil skip: a LIVE context that is not a chromedp target
// must not reach chromedp.Run. Run treats a non-chromedp context as
// "start a new browser", which is how TestCloseTab_CancelsTheClosedTabsContext
// launched a second Chrome on CI, hit "No usable sandbox", and panicked
// in chromedp's Allocate cleanup. The recorded-activation seam is
// deliberately NOT installed here — that seam is what hid the defect
// from every SwitchTab unit test.
func TestRunTabFocusCDP_NonChromedpContextDoesNotAllocate(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg}
	err = m.runTabFocusCDP(context.Background(), page.BringToFront())
	require.NoError(t, err, "a non-chromedp context must be a no-op, not a new Chrome")
}

// TestRunTabFocusCDP_UnattachedChromedpContextDoesNotAllocate covers the
// actual fake-tab factory shape: chromedp.NewContext(context.Background()).
// FromContext is non-nil (default ExecAllocator) but Target is nil until
// the first Run — and that first Run is what launched Chrome on CI and
// panicked Allocate. The recorded-activation seam is deliberately NOT
// installed here, matching TestRunTabFocusCDP_NonChromedpContextDoesNotAllocate.
func TestRunTabFocusCDP_UnattachedChromedpContextDoesNotAllocate(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg}
	ctx, cancel := chromedp.NewContext(context.Background())
	t.Cleanup(cancel)
	c := chromedp.FromContext(ctx)
	require.NotNil(t, c, "NewContext must install a chromedp context")
	require.Nil(t, c.Target, "a never-Run NewContext must have no Target — that is the fake-tab shape")
	err = m.runTabFocusCDP(ctx, page.BringToFront())
	require.NoError(t, err, "an unattached chromedp context must be a no-op, not a new Chrome")
}

// TestSwitchTab_ActivationPrecedesTabsChangedNotification is the end-to-end
// ordering guarantee. The recapture is triggered BY the tabs-changed callback,
// so Chrome must already agree about the active tab before that callback runs
// — otherwise the recapture resolves the old tab and the stream silently never
// moves, which is exactly the shipped bug.
func TestSwitchTab_ActivationPrecedesTabsChangedNotification(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	rec := &chainRecorder{}

	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) == "foreground" {
			rec.add("chrome-activated")
		}
		return nil
	}
	m.SetTabsChangedFunc(func(_ string, _ []Tab, activeIdx int) {
		// Stands in for the real LiveViewRegistry.handleTabsChanged, which is
		// what ultimately calls CaptureSession.Recapture.
		rec.add("tabs-changed")
		rec.add("recapture-would-fire")
		_ = activeIdx
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	rec.mu.Lock()
	rec.events = nil // discard setup noise
	rec.mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	got := rec.snapshot()
	require.Equal(t, []string{"chrome-activated", "tabs-changed", "recapture-would-fire"}, got,
		"Chrome must be told the active tab moved BEFORE the tabs-changed callback fires the "+
			"recapture; reversing this re-opens the silent wrong-tab capture")
}

// TestSwitchTab_EverySwitchActivatesAndNotifies — a rapid sequence of switches
// must produce one activation per switch. A missed activation anywhere in the
// sequence leaves the capture pinned to a stale tab for every subsequent
// switch, which is how the live session ended up three-way desynced (tab strip,
// URL bar, and pixels all disagreeing).
func TestSwitchTab_EverySwitchActivatesAndNotifies(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	rec := &chainRecorder{}

	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) == "foreground" {
			rec.add("activate")
		}
		return nil
	}
	m.SetTabsChangedFunc(func(string, []Tab, int) { rec.add("notify") })

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = m.OpenTab(testSessionID)
		require.NoError(t, err)
	}

	rec.mu.Lock()
	rec.events = nil
	rec.mu.Unlock()

	order := []int{2, 0, 1, 0, 2}
	for _, idx := range order {
		_, err := m.SwitchTab(testSessionID, idx)
		require.NoError(t, err)
	}

	assert.Equal(t, len(order), rec.count("activate"),
		"every switch must activate its target tab in Chrome — a skipped activation pins "+
			"the capture to a stale tab for all later switches")
	assert.Equal(t, len(order), rec.count("notify"),
		"every switch must still notify the tabs-changed subscribers")
}

// TestSwitchTab_ActivatesTabMatchingResolvedSession ties the activation to the
// SAME context Session() hands to the capture path. If these ever diverge, the
// panel activates one tab while the capture binds another — the desync in a
// different disguise.
func TestSwitchTab_ActivatesTabMatchingResolvedSession(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	// Capture the activated context via a slice rather than assigning a
	// context.Context into a captured variable — the latter trips fatcontext
	// ("nested context in function literal") and, more substantively, storing a
	// context for later comparison is exactly the pattern that lint discourages.
	var mu sync.Mutex
	activatedCtxs := make([]context.Context, 0, 1)
	m.tabFocusFn = func(tabCtx context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) != "foreground" {
			return nil
		}
		mu.Lock()
		activatedCtxs = append(activatedCtxs, tabCtx)
		mu.Unlock()
		return nil
	}

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// Discard setup activations: OpenTab foregrounds the tab it opens too
	// (review finding F9), so only the switch below is under test here.
	mu.Lock()
	activatedCtxs = activatedCtxs[:0]
	mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	// Session() is the oracle the live/capture paths use to resolve "the
	// active tab" (ADR-041 D1) — the activation must have targeted that exact
	// context.
	resolved, err := m.Session(testSessionID)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, activatedCtxs, 1, "exactly one activation expected")
	assert.Same(t, resolved, activatedCtxs[0],
		"the tab activated in Chrome must be the same context Session() resolves, or the "+
			"panel and the capture disagree about which tab is live")
}

// TestSwitchTab_TabsChangedReceivesNewActiveIndex guards the payload the live
// registry relies on to decide whether the active tab actually changed
// (onTabsChanged's activeTabChanged signal, which gates the recapture).
func TestSwitchTab_TabsChangedReceivesNewActiveIndex(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	var mu sync.Mutex
	var gotIdx []int
	var gotActiveFlags []bool
	m.SetTabsChangedFunc(func(_ string, tabs []Tab, activeIdx int) {
		mu.Lock()
		defer mu.Unlock()
		gotIdx = append(gotIdx, activeIdx)
		if activeIdx >= 0 && activeIdx < len(tabs) {
			gotActiveFlags = append(gotActiveFlags, tabs[activeIdx].Active)
		}
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	mu.Lock()
	gotIdx, gotActiveFlags = nil, nil
	mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []int{1}, gotIdx, "the tabs-changed callback must carry the NEW active index")
	require.Equal(t, []bool{true}, gotActiveFlags,
		"the snapshot's active flag must agree with the reported active index")
}

// TestSwitchTab_SlowActivationDoesNotBlockOtherSessions — activation issues a
// real, blocking CDP call in production. A slow or hung bringToFront on one
// browsing context must not stall unrelated manager work, or one wedged tab
// takes the whole browser subsystem down with it.
func TestSwitchTab_SlowActivationDoesNotBlockOtherSessions(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// Installed AFTER setup, deliberately: OpenTab drives the same focus seam
	// as SwitchTab (review finding F9 — opening a tab moves the active tab
	// too), so a hook that blocks forever would hang the setup instead of the
	// switch this test is about.
	m.tabFocusFn = func(context.Context, ...chromedp.Action) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // simulate a hung CDP call
		return nil
	}

	switched := make(chan struct{})
	go func() {
		defer close(switched)
		_, _ = m.SwitchTab(testSessionID, 1)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("activation hook was never reached")
	}

	// While that switch is stuck inside activation, unrelated manager reads
	// must still complete promptly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = m.ListTabs(testSessionID)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("a hung tab activation blocked an unrelated manager call — activation must not " +
			"hold the BrowserManager lock (ADR-038)")
	}

	close(release)
	<-switched
}

// TestSwitchTab_SameIndexStillTriggersExactlyOneRecapture is THE regression
// test for this fix. Switching to the index that is ALREADY active is the
// user's recovery action when Chrome has drifted away from our model, and it
// must move the picture. Before the fix the count stays at 0: SwitchTab did
// its half (Page.bringToFront corrects Chrome) and nothing ever asked the
// encoder to re-bind, so the video sat on the old tab forever.
func TestSwitchTab_SameIndexStillTriggersExactlyOneRecapture(t *testing.T) {
	m, ledger := newThreeTabManagerWithCapture(t)

	_, err := m.SwitchTab(testSessionID, 2) // already active
	require.NoError(t, err)

	eventuallyCount(t, ledger, 1,
		"switching to the already-active tab must still request exactly one recapture — "+
			"it is the only way a user can recover from Chrome and the model disagreeing")
}

// TestSwitchTab_DifferentIndexDoesNotDoubleFireRecapture is the guard on the
// other side. The normal path is already covered by onTabsChanged, so
// SwitchTab must NOT add a second recapture there — two recaptures per switch
// means two encoder re-binds and two PLI bursts for one user action.
func TestSwitchTab_DifferentIndexDoesNotDoubleFireRecapture(t *testing.T) {
	m, ledger := newThreeTabManagerWithCapture(t)

	_, err := m.SwitchTab(testSessionID, 0) // a real move
	require.NoError(t, err)

	eventuallyCount(t, ledger, 1,
		"a switch that really moves the model must produce exactly one recapture — "+
			"onTabsChanged already owns that case, so SwitchTab must not fire a second")
}

// TestSwitchTab_MixedSequenceProducesOneRecapturePerSwitch walks the exact
// shape the live failure had: real moves interleaved with same-index
// re-selections. Every one of them must move the picture, and none may fire
// twice — regardless of which half of the system (SwitchTab itself, or
// onTabsChanged) happens to own that particular switch.
//
// Each switch is settled before the next is issued. That is deliberate, not
// timing hygiene for its own sake: RecaptureForTabChange COALESCES calls that
// overlap in time (see its doc comment — two same-index switches in the same
// instant genuinely need only one re-bind), so firing the whole sequence back
// to back would make the expected total depend on scheduler luck. Settling
// each step keeps the assertion "one per switch" exact.
func TestSwitchTab_MixedSequenceProducesOneRecapturePerSwitch(t *testing.T) {
	m, ledger := newThreeTabManagerWithCapture(t)

	order := []int{2, 0, 0, 1, 1, 1, 2} // starts on tab 2: same, move, same, move, same, same, move
	for step, idx := range order {
		_, err := m.SwitchTab(testSessionID, idx)
		require.NoError(t, err)
		eventuallyCount(t, ledger, step+1,
			fmt.Sprintf("switch #%d (to tab %d) owes exactly one recapture", step+1, idx))
	}
}

// TestSwitchTab_SameIndexRecaptureIsANoOpWithoutCaptureSession pins that the
// new call is safe on the overwhelmingly common path where nobody is watching
// the browser panel at all.
func TestSwitchTab_SameIndexRecaptureIsANoOpWithoutCaptureSession(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, serr := m.SwitchTab(testSessionID, 0)
		require.NoError(t, serr)
	})
	require.Nil(t, m.CaptureSession(), "no capture session should have been created as a side effect")
}

// --- (b) the tab-change-specific recapture entry point ---

// TestRecaptureForTabChange_ReassertsForegroundBeforeControlFrame pins the
// ORDER that makes the re-assert worth anything. The encoder re-resolves its
// capture target when it receives the control frame, so a re-assert that
// landed AFTER the frame would be pure cost with no effect.
func TestRecaptureForTabChange_ReassertsForegroundBeforeControlFrame(t *testing.T) {
	relay := &fakeRelay{}
	var encoderCalls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&encoderCalls, nil))

	rec := &chainRecorder{}
	cs.mu.Lock()
	cs.foregroundAssertFn = func(context.Context) bool {
		rec.add("foreground-assert")
		return true
	}
	cs.mu.Unlock()
	cs.BindIngest(func(action string, _ *string, _, _ int, _ int) error {
		rec.add("control:" + action)
		return nil
	}, func() {})

	cs.RecaptureForTabChange()

	require.Eventually(t, func() bool { return len(rec.snapshot()) == 2 }, 2*time.Second, 5*time.Millisecond,
		"RecaptureForTabChange must both re-assert the foreground tab and send the control frame")
	assert.Equal(t, []string{"foreground-assert", "control:recapture"}, rec.snapshot(),
		"Chrome must be told which tab is foreground BEFORE the encoder is told to re-query it — "+
			"the reverse order re-binds to whatever Chrome still believed")
}

// TestRecaptureForTabChange_CoalescesConcurrentCalls pins that a burst of tab
// changes cannot spawn a goroutine (and a CDP round trip) each. Coalescing is
// only safe because the worker re-resolves the CURRENT active tab on every
// pass, so the last change still wins.
func TestRecaptureForTabChange_CoalescesConcurrentCalls(t *testing.T) {
	relay := &fakeRelay{}
	var encoderCalls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&encoderCalls, nil))

	release := make(chan struct{})
	var asserts int32
	var firstOnce sync.Once
	cs.mu.Lock()
	cs.foregroundAssertFn = func(context.Context) bool {
		n := atomic.AddInt32(&asserts, 1)
		if n == 1 {
			firstOnce.Do(func() { <-release }) // hold the worker inside pass 1
		}
		return true
	}
	cs.mu.Unlock()
	ledger := &recaptureLedger{}
	ledger.bind(cs)

	cs.RecaptureForTabChange() // starts the worker, which blocks in the assert
	require.Eventually(t, func() bool { return atomic.LoadInt32(&asserts) == 1 }, 2*time.Second, 5*time.Millisecond)

	for i := 0; i < 25; i++ {
		cs.RecaptureForTabChange() // all coalesce into ONE pending re-run
	}
	close(release)

	require.Eventually(t, func() bool { return ledger.count() == 2 }, 2*time.Second, 5*time.Millisecond,
		"25 coalesced calls plus the in-flight one must settle at exactly two passes")
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 2, ledger.count(), "coalescing must not leak an extra pass")
	assert.Equal(t, int32(2), atomic.LoadInt32(&asserts), "one foreground re-assert per pass, not per call")
}

// TestRecaptureForTabChange_IsANoOpAfterStop guards against a tab change
// racing teardown and resurrecting CDP/ingest traffic for a dead session.
func TestRecaptureForTabChange_IsANoOpAfterStop(t *testing.T) {
	relay := &fakeRelay{}
	var encoderCalls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&encoderCalls, nil))
	ledger := &recaptureLedger{}
	ledger.bind(cs)

	cs.Stop()
	cs.RecaptureForTabChange()

	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 0, ledger.count(), "a stopped session must not request a recapture")
}

// --- (c) the paths that moved activeIdx without telling Chrome ---

// TestAdoptTarget_ActivatesAdoptedTabInChrome. Adoption makes the new tab
// active (ADR-041 D2) and then fires the tabs-changed callback that drives the
// WebRTC recapture — so if Chrome is never told, the encoder's
// chrome.tabs.query({active:true}) answer and ours agree only by luck.
func TestAdoptTarget_ActivatesAdoptedTabInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	rec.mu.Lock()
	rec.ctxs, rec.treatment = nil, nil // discard first-tab setup
	rec.mu.Unlock()

	result, err := m.adoptTarget(testSessionID, target.ID("adopted-1"))
	require.NoError(t, err)
	require.NotNil(t, result.Adopted, "setup expects the adoption to succeed")

	activations := rec.calls()
	require.Len(t, activations, 1, "adopting a tab must tell Chrome that tab is now active")

	adoptedCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, activations[0] == adoptedCtx,
		"the activated context must be the ADOPTED tab's — the same one Session() now resolves")
	assert.Len(t, rec.blurCalls(), 1,
		"the tab the adoption moved away from must have its focus emulation released, as on every other path")
}

// TestAdoptTarget_RetriesAfterTransientFailure is THE regression test for the
// stranded tab. Before the retry, the first failure was terminal: adoptTarget
// deletes its pendingAdopt entry on the error path and Target.targetCreated
// never fires again for that target, so the tab was invisible to the model for
// the life of the browsing context — which is precisely how Chrome and the
// model end up with different active tabs.
func TestAdoptTarget_RetriesAfterTransientFailure(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	flaky, attempts := flakyTabFactory(2) // fail twice, succeed on the third
	m.mu.Lock()
	m.createTabFn = flaky
	m.adoptRetryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	m.mu.Unlock()

	m.adoptTargetWithRetry(testSessionID, target.ID("flaky-1"))

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "the tab must be adopted once the transient failure clears")
	assert.Equal(t, int32(3), atomic.LoadInt32(attempts), "two failures then one success")
}

// TestAdoptTarget_RetryIsBounded — a target that is genuinely gone must not be
// retried forever. An unbounded retry would be a goroutine (and CDP round
// trip) leak per advert-opened tab, on the same saturated transport that
// caused the failure.
func TestAdoptTarget_RetryIsBounded(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	alwaysFails, attempts := flakyTabFactory(1 << 30)
	backoff := []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	m.mu.Lock()
	m.createTabFn = alwaysFails
	m.adoptRetryBackoff = backoff
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.adoptTargetWithRetry(testSessionID, target.ID("gone-1"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("adoptTargetWithRetry never returned — the retry is unbounded")
	}

	assert.Equal(t, int32(len(backoff)+1), atomic.LoadInt32(attempts),
		"exactly one initial attempt plus one per backoff step")
	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "nothing should have been adopted")
}

// TestHandleTargetEvent_UsesRetryingAdoption proves the retry is wired into
// the path that actually receives Chrome's one-and-only targetCreated event.
// Testing adoptTargetWithRetry directly would pass even if handleTargetEvent
// still called the bare, one-shot adoptTarget — which is the bug.
func TestHandleTargetEvent_UsesRetryingAdoption(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 1)

	m.mu.Lock()
	openerID := m.sessions[testSessionID].tabs[0].targetID
	flaky, _ := flakyTabFactory(1) // one transient failure, then success
	m.createTabFn = flaky
	m.adoptRetryBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	m.mu.Unlock()

	m.handleTargetEvent(testSessionID, &target.EventTargetCreated{
		TargetInfo: &target.Info{
			TargetID: target.ID("popup-1"),
			Type:     "page",
			OpenerID: openerID,
		},
	})

	require.Eventually(t, func() bool {
		got, _, lerr := m.ListTabs(testSessionID)
		return lerr == nil && len(got) == 2
	}, 3*time.Second, 5*time.Millisecond,
		"a target whose first adoption attempt failed must still end up adopted — "+
			"Target.targetCreated fires exactly once, so nothing else will ever try again")
}

// TestOpenTab_RealChromium_SecondTabSharesSameBrowser is the browserCtx
// lifetime fix's second real-Chromium regression guard: OpenTab must add a
// SECOND tab to the SAME running browser, not try to launch a second
// Chromium process. Before the fix, OpenTab's "append an additional tab"
// branch reused m.allocCtx for the 2nd+ tab — chromedp treats a fresh
// context off the raw allocator as "launch a brand new browser" (see
// createTab's and sessionEntry.browserCtx's doc comments in manager.go), and
// with the managed-mode fixed debug port already held by the first browser,
// that second launch failed outright with "chrome failed to start".
//
// Both tabs must be independently usable (navigate + read distinguishing
// content) — proving they are two live tabs of ONE browser, not one tab
// replacing/hiding the other.
func TestOpenTab_RealChromium_SecondTabSharesSameBrowser(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	tab1, err := mgr.OpenTab(testSessionID)
	require.NoError(t, err,
		"OpenTab must open a second tab in the SAME running browser, not try to launch a second Chromium")
	assert.Equal(t, 1, tab1.Index)
	assert.True(t, tab1.Active)

	tabs, activeIdx, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2, "OpenTab must result in exactly 2 tabs in the SAME browsing context")
	assert.Equal(t, 1, activeIdx)

	// Tab 1 (now active) must be independently usable — navigate + read the
	// resulting title.
	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.False(t, sameChromedpContext(tab0Ctx, tab1Ctx), "tab 0 and tab 1 must be distinct chromedp contexts")
	var title string
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL), chromedp.Title(&title)),
		"tab 1 must be able to navigate — it is a real, independently-usable tab in the running browser")
	assert.Equal(t, "Contact", title)

	// Switch back to tab 0 and confirm IT is STILL independently usable too
	// (proves both tabs stayed alive in the same browser this whole time,
	// rather than tab 1's creation having torn down and replaced tab 0).
	_, err = mgr.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	tab0CtxAgain, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, sameChromedpContext(tab0Ctx, tab0CtxAgain), "Session must follow SwitchTab back to the original tab 0 context")
	var heading string
	require.NoError(t, chromedp.Run(tab0CtxAgain, chromedp.Text("h1", &heading, chromedp.ByQuery)))
	assert.Equal(t, "Contact", heading)
}

// TestCloseTab_RealChromium_ClosingTab0KeepsBrowserAndSurvivorAlive is the
// browserCtx lifetime fix's core real-Chromium regression guard: closing tab
// 0 out of 2+ open tabs must NOT tear down the browser or the other tab.
//
// Before the fix, tab 0 (the first tab created for a browsing context) was
// itself the chromedp context chromedp binds the running *Browser to (its
// "c.first" target) — canceling THAT context is what chromedp.Cancel's own
// doc comment calls "graceful[ly] clos[ing]" the whole browser. So closing
// tab 0 specifically (as opposed to any other tab) used to kill the ENTIRE
// Chromium process, taking every sibling tab down with it. The browserCtx
// design fixes this by making EVERY user tab — tab 0 included — a
// non-"first" child of a dedicated, owner-only browser-owning context, so no
// single tab's close can ever take the browser down.
func TestCloseTab_RealChromium_ClosingTab0KeepsBrowserAndSurvivorAlive(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.NotNil(t, navRes)
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	_, err := mgr.OpenTab(testSessionID)
	require.NoError(t, err, "opening a second tab must succeed")

	tabsBefore, _, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabsBefore, 2, "sanity: two tabs open before closing tab 0")

	// Close tab 0 specifically — NOT the active/last tab; tab 1 survives.
	closedTabs, activeIdx, err := mgr.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing tab 0 must succeed and must NOT kill the browser")
	require.Len(t, closedTabs, 1, "one tab remains after closing tab 0 out of 2")
	assert.Equal(t, 0, activeIdx, "the surviving tab slides into index 0 and becomes active")

	// The surviving tab (the one that WAS tab 1, now shifted to index 0)
	// must still be independently usable — navigate and read text via raw
	// chromedp — proving the browser (and this tab) are genuinely still
	// alive, not orphaned/dead.
	survivorCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	var title string
	require.NoError(
		t,
		chromedp.Run(survivorCtx, chromedp.Navigate(srv.URL+"/booked"), chromedp.Title(&title)),
		"the surviving tab must still be able to navigate after tab 0 was closed — the browser must not have died",
	)
	assert.Equal(t, "Booked", title)
	var heading string
	require.NoError(t, chromedp.Run(survivorCtx, chromedp.Text("#sched", &heading, chromedp.ByQuery)))
	assert.Equal(t, "Scheduling", heading)

	// And via the actual browser_get_text TOOL path too (not just raw
	// chromedp against the manually-resolved ctx) — proves the manager's
	// testSessionID plumbing still works end-to-end post-close.
	getText := mustGetTool(t, registry, "browser_get_text")
	getTextRes := getText.Execute(ctx, map[string]any{"selector": "#sched"})
	require.NotNil(t, getTextRes)
	require.False(t, getTextRes.IsError, "browser_get_text must still work on the survivor; got: %s", getTextRes.ForLLM)
	data := decodeJSON(t, getTextRes.ForLLM)
	assert.Equal(t, "Scheduling", data["text"])
}

// TestSwitchTab_RealChromium_SessionFollowsActiveTab confirms Session(default)
// follows whichever tab is active across a real SwitchTab call, and that
// each tab retains its OWN independent navigation state — the two tabs are
// genuinely separate, live pages in the same browser, not one tab's state
// leaking into the other.
func TestSwitchTab_RealChromium_SessionFollowsActiveTab(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	_, err = mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL+"/booked")))

	require.False(t, sameChromedpContext(tab0Ctx, tab1Ctx), "tab 0 and tab 1 must be distinct chromedp contexts")

	_, err = mgr.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	followedCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, sameChromedpContext(tab0Ctx, followedCtx), "Session(default) must follow SwitchTab back to tab 0")
	var title string
	require.NoError(t, chromedp.Run(followedCtx, chromedp.Title(&title)))
	assert.Equal(t, "Contact", title, "tab 0 must still show its OWN page (Contact), not tab 1's (Booked)")

	_, err = mgr.SwitchTab(testSessionID, 1)
	require.NoError(t, err)
	followedCtx2, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, sameChromedpContext(tab1Ctx, followedCtx2), "Session(default) must follow SwitchTab to tab 1")
	var title2 string
	require.NoError(t, chromedp.Run(followedCtx2, chromedp.Title(&title2)))
	assert.Equal(t, "Booked", title2, "tab 1 must retain ITS own navigation state independent of tab 0")
}

// TestCloseTab_RealChromium_ActiveTabClose_LiveViewFollowsSurvivorNoFalseDeath
// is the real-Chromium regression guard for the live-UAT fix ("closing the
// ACTIVE tab fires a false 'session ended' banner and leaves the live view
// stuck on stale content", confirmed 2/2 by two independent live testers
// WITH A VIEWER ATTACHED). It attaches the real ADR-038 live-view engine
// (mgr.Live().Attach — the same path pkg/gateway/browser_ws.go drives) to
// the active tab, closes it, and proves against REAL Chromium tab-context
// resolution that (a) no false "session ended" status ever reaches the
// viewer, and (b) the live view's tabCtx is genuinely re-bound to the
// surviving tab's REAL chromedp context (ADR-061: video is carried
// exclusively by WebRTC now, so there is no screencast frame left to prove
// this via — the live view's own tabCtx/listenCtx bookkeeping, compared
// against mgr.Session()'s real post-close resolution, is the mechanism that
// actually needs to be right).
func TestCloseTab_RealChromium_ActiveTabClose_LiveViewFollowsSurvivorNoFalseDeath(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	tab1, err := mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	require.Equal(t, 1, tab1.Index)
	require.True(t, tab1.Active)

	var statusMu sync.Mutex
	var statusMsgs []string
	onStatus := func(msg string) {
		statusMu.Lock()
		statusMsgs = append(statusMsgs, msg)
		statusMu.Unlock()
	}

	controlledByOther, err := mgr.Live().Attach(testSessionID, "viewer1", onStatus, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)
	t.Cleanup(func() { mgr.Live().Detach(testSessionID, "viewer1") })

	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	lv := mgr.Live().view(testSessionID)
	lv.mu.Lock()
	require.True(t, sameChromedpContext(tab1Ctx, lv.tabCtx),
		"sanity: the live view must be bound to tab 1 (the active tab) before the close")
	lv.mu.Unlock()

	// Close the ACTIVE tab (index 1) — the exact live-UAT repro. Tab 0
	// survives and becomes active.
	closedTabs, activeIdx, err := mgr.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, closedTabs, 1)
	require.Equal(t, 0, activeIdx)

	survivorCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)

	// The live view must rebind to the REAL surviving tab context — not stay
	// stuck on the closed tab, and not go dead (nil listenCtx).
	require.Eventually(t, func() bool {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		return lv.listenCtx != nil && sameChromedpContext(survivorCtx, lv.tabCtx)
	}, 5*time.Second, 10*time.Millisecond,
		"the live view must rebind to the surviving tab's real chromedp context after the active tab "+
			"closes — it must not stay bound to the closed tab or go dead")

	statusMu.Lock()
	got := append([]string(nil), statusMsgs...)
	statusMu.Unlock()
	assert.Empty(t, got,
		"closing the ACTIVE tab (browser and survivor alive) must never emit a false 'session ended' "+
			"status to an attached viewer: %v", got)
}

// TestCloseTab_RealChromium_TargetGenuinelyClosedInChrome answers, WITH
// EVIDENCE rather than by reading chromedp's source, the exact question the
// operator raised: does BrowserManager.CloseTab's `closing.cancel()` actually
// tell CHROME to close the target (Target.closeTarget over CDP), or does it
// merely detach our own chromedp client and leave the page resident in the
// browser?
//
// This project has been burned before by assuming a mechanism instead of
// measuring it (ADR-061's JPEG screencast, the focus-emulation episode) — an
// `<img>` swapped fast enough looks like video, and a chromedp context that
// stops responding to OUR calls looks exactly like a closed tab from inside
// this package's own bookkeeping (se.tabs), whether or not Chrome's process
// still has the page open. The only way to tell the difference is to ask
// Chrome directly, which is what this test does: after every CloseTab call it
// enumerates Chrome's REAL "page" targets via Target.getTargets (the same CDP
// call ReconcileTabs uses) and asserts the closed tab's TargetID is actually
// gone — not merely absent from mgr.ListTabs.
//
// Exercises BOTH CloseTab code paths that call closing.cancel():
//  1. Closing one of SEVERAL open tabs (the len(se.tabs) > 1 branch).
//  2. Closing the LAST remaining tab, which is replaced via createFirstTab
//     reusing the same browserCtx (ADR-041 D3 "never leaves zero tabs") — this
//     additionally proves the replacement is a genuinely NEW real Chrome
//     target, and that the real target COUNT stays at exactly 1 (no leaked
//     ghost target sitting alongside the replacement, no zero-tab gap).
func TestCloseTab_RealChromium_TargetGenuinelyClosedInChrome(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	// --- Set up two real tabs and record Chrome's OWN target IDs for both. ---
	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))
	tab0ID := chromeTargetIDOf(t, tab0Ctx)

	_, err = mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL+"/booked")))
	tab1ID := chromeTargetIDOf(t, tab1Ctx)
	require.NotEqual(t, tab0ID, tab1ID, "sanity: two distinct tabs must have two distinct real CDP TargetIDs")

	// Sanity against ground truth BEFORE closing anything: Chrome must
	// already show both real targets, and only those two.
	before := realChromePageTargetIDs(t, tab1Ctx)
	require.True(t, before[tab0ID], "sanity: Chrome must show tab0's real target before any close")
	require.True(t, before[tab1ID], "sanity: Chrome must show tab1's real target before any close")
	// NOTE: no assertion on the TOTAL target count. chromedp.Targets lists
	// every page target in the whole browser, and in ADR-043 shared-Chrome
	// mode that includes other browsing contexts (other agents' sessions, a
	// concurrently-running test's tabs). Measured here: 4 targets present
	// where this test had created 2. The question this test exists to answer
	// is about IDENTITY -- is THIS closed tab's target gone from Chrome --
	// so every assertion below is keyed on the specific TargetIDs this test
	// created, never on how many targets the shared browser happens to hold.

	// --- Case 1: close tab 0 out of 2 (the len(se.tabs) > 1 branch). ---
	closedTabs, activeIdx, err := mgr.CloseTab(testSessionID, 0)
	require.NoError(t, err)
	require.Len(t, closedTabs, 1, "one tab remains after closing tab 0 out of 2")
	require.Equal(t, 0, activeIdx)

	survivorCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	survivorID := chromeTargetIDOf(t, survivorCtx)
	assert.Equal(t, tab1ID, survivorID, "the surviving tab must still be Chrome's original tab1 target")

	// CloseTarget is in-flight when cancel() returns — listing immediately
	// flaked on CI (2026-08-16 #615: tab0 still present, count still 4).
	// Wait on the identity condition, never a fixed delay. Do NOT assert
	// the total target count: the comment above already records that a
	// shared Chrome can hold extra page targets, so a count that stays
	// put is not evidence the closed tab leaked.
	require.True(t, waitUntilChromeTargetGone(t, survivorCtx, tab0ID, 2*time.Second),
		"MEASURED: tab0's real CDP target (%s) must be GONE from Chrome's own target list after CloseTab — "+
			"if this is still present, CloseTab only detached our client and leaked the tab in Chrome", tab0ID)
	afterFirstClose := realChromePageTargetIDs(t, survivorCtx)
	assert.True(t, afterFirstClose[tab1ID], "the surviving tab's real target must still be present")

	// --- Case 2: close the LAST remaining tab (createFirstTab replacement). ---
	closedTabs2, activeIdx2, err := mgr.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed and produce a replacement (ADR-041 D3)")
	require.Len(t, closedTabs2, 1, "ADR-041 D3: closing the last tab must never leave zero tabs")
	require.Equal(t, 0, activeIdx2)

	replacementCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	replacementID := chromeTargetIDOf(t, replacementCtx)
	assert.NotEqual(t, tab1ID, replacementID,
		"the last-tab replacement must be a genuinely NEW real Chrome target, not a relabeled survivor")

	require.True(t, waitUntilChromeTargetGone(t, replacementCtx, tab1ID, 2*time.Second),
		"MEASURED: tab1's real CDP target (%s) must be GONE from Chrome's own target list after closing the "+
			"last tab — if still present, the last-tab-replacement path leaked it", tab1ID)
	afterSecondClose := realChromePageTargetIDs(t, replacementCtx)
	assert.True(t, afterSecondClose[replacementID], "the replacement tab's real target must be present")
	assert.False(t, afterSecondClose[tab0ID],
		"tab0's target must still be gone after the second close — it must not reappear")
}

// --- Tab-set add/switch/close/neighbor-activation (ADR-041 D1/D3) ---

func TestOpenTab_AppendsAndActivates(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tab, err := m.OpenTab(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
	assert.False(t, tabs[0].Active)
	assert.True(t, tabs[1].Active)
}

func TestOpenTab_MemoryPressure_Refused(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(2)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err, "the second tab opens while there is memory headroom")

	_, err = m.OpenTab(testSessionID)
	require.Error(t, err, "the third tab must be refused once the machine is under memory pressure")
	assert.ErrorIs(t, err, errMemoryPressureTabOpen)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "a refused OpenTab must not leave a partially-added tab")
}

// FR-082: on a host whose memory cannot be measured at all, the FIRST tab
// opens and the SECOND is refused. A floor of zero would remove browsing
// entirely from gVisor and GKE Sandbox, which this project supports; a floor
// of two is unpriced. Both halves are asserted, because a gate that refuses
// the first tab and a gate that admits without limit both pass a test that
// only checks one of them.
func TestOpenTab_UnmeasurableHost_FirstTabOpensSecondIsRefused(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = unmeasurableHost()

	_, err := m.Session(testSessionID)
	require.NoError(t, err, "the FIRST tab must open on an unmeasurable host")

	_, err = m.OpenTab(testSessionID)
	require.Error(t, err, "the SECOND tab must be refused on an unmeasurable host")
	assert.ErrorIs(t, err, errMemoryPressureTabOpen)
}

func TestSwitchTab_ChangesActiveIndex(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	tab, err := m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
	assert.False(t, tabs[0].Active)
	assert.False(t, tabs[2].Active)
}

func TestSwitchTab_OutOfRange_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, err = m.SwitchTab(testSessionID, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")

	_, err = m.SwitchTab(testSessionID, -1)
	require.Error(t, err)
}

func TestSwitchTab_UnknownSession_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.SwitchTab("never-opened", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active session")
}

func TestCloseTab_NonActiveTab_KeepsActiveIndexStable(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 2, active
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	// Active tab (was index 2) shifted down to index 1 after removing index 0.
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

func TestCloseTab_ActiveTab_ActivatesSlidInNeighbour(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 2
	require.NoError(t, err)

	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	// Closing the active tab (index 1): the tab that slides into index 1
	// (formerly index 2) becomes active.
	tabs, activeIdx, err := m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

func TestCloseTab_ActiveLastTab_FallsBackToNewLastTab(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1, active
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)
}

func TestCloseTab_OutOfRange_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, _, err = m.CloseTab(testSessionID, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestCloseTab_LastRemainingTab_NeverLeavesZeroTabs(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed by opening a fresh replacement, not error")
	require.Len(t, tabs, 1, "the browsing context must never be left with zero tabs")
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)

	// The browsing context must still be usable afterward.
	ctx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)
}

func TestCloseTab_CancelsTheClosedTabsContext(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}
	fn, canceled := fakeTabFactory()
	m.createTabFn = fn

	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	require.EqualValues(t, 0, atomic.LoadInt32(canceled))

	_, _, err = m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(canceled), "closing a tab must cancel its chromedp context")
}

// --- ADR-041 fix F3: passive Target.targetCreated listener re-arm ---
//
// chromedp.ListenTarget's registration is scoped to the ctx it was given, so
// closing the tab whose ctx currently holds the ADR-041 D2 passive listener
// silently ends new-tab detection forever unless something re-installs it on
// whichever tab becomes the new tab 0. installTargetListenerLocked does this
// re-arm, tracked via sessionEntry.listenerTarget. These tests can't fire a
// real CDP Target.targetCreated event in this pod (no Chromium binary), so
// they verify the re-arm BOOKKEEPING directly: se.listenerTarget must track
// se.tabs[0].targetID after any operation that changes which tab occupies
// slot 0.

// TestCloseTab_NonLastBranch_RearmsListenerOnNewTab0 covers CloseTab's
// non-last branch: closing tab 0 out of >= 2 tabs slides another tab into
// slot 0, and the listener must be re-armed onto IT.
func TestCloseTab_NonLastBranch_RearmsListenerOnNewTab0(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1
	require.NoError(t, err)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	oldListenerTarget := se.listenerTarget
	oldTab0TargetID := se.tabs[0].targetID
	newTab0TargetIDBeforeClose := se.tabs[1].targetID // what will slide into slot 0
	m.mu.Unlock()

	require.Equal(t, oldTab0TargetID, oldListenerTarget, "the listener must start out armed on tab 0")

	// Close tab 0 — the ONLY non-last branch that changes which tab occupies
	// slot 0 without going through registerFreshSessionLocked.
	_, _, err = m.CloseTab(testSessionID, 0)
	require.NoError(t, err)

	m.mu.Lock()
	se = m.sessions[testSessionID]
	newListenerTarget := se.listenerTarget
	newTab0TargetID := se.tabs[0].targetID
	m.mu.Unlock()

	assert.Equal(t, newTab0TargetIDBeforeClose, newTab0TargetID, "sanity: the surviving tab slid into slot 0")
	assert.Equal(t, newTab0TargetID, newListenerTarget,
		"the listener must be re-armed onto the NEW tab 0 after closing the old one")
	assert.NotEqual(t, oldListenerTarget, newListenerTarget,
		"the listener bookkeeping must actually change — re-arming onto the same (now-closed) target would "+
			"silently leave new-tab detection dead")
}

// TestCloseTab_LastRemainingTab_RearmsListenerOnReplacement covers the
// last-tab-replacement path (registerFreshSessionLocked, via createFirstTab):
// closing the LAST tab tears down the whole sessionEntry and builds a brand
// new one around a fresh blank tab — the listener must be armed on THAT tab,
// not left pointing at the torn-down one.
func TestCloseTab_LastRemainingTab_RearmsListenerOnReplacement(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	oldListenerTarget := m.sessions[testSessionID].listenerTarget
	oldTab0TargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()
	require.Equal(t, oldTab0TargetID, oldListenerTarget)

	tabs, _, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed via the fresh-replacement path")
	require.Len(t, tabs, 1)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	newListenerTarget := se.listenerTarget
	newTab0TargetID := se.tabs[0].targetID
	m.mu.Unlock()

	assert.Equal(t, newTab0TargetID, newListenerTarget,
		"the listener must be armed on the replacement tab's target, not the torn-down one")
	assert.NotEqual(t, oldListenerTarget, newListenerTarget,
		"the replacement tab is a genuinely new CDP target — the listener bookkeeping must have moved on")
}

// TestCloseTab_LastTabReplacement_ConcurrentWithOpenTab_NoLeak is F1's
// second regression guard: CloseTab's last-tab-replacement branch used to
// build its own bare sessionEntry and blindly overwrite m.sessions[sessionID]
// too, exactly like OpenTab did — a concurrent OpenTab racing the replacement
// could suffer the identical leak. Drives that race directly and applies the
// same "created == tracked + canceled" no-orphan invariant as the test above.
func TestCloseTab_LastTabReplacement_ConcurrentWithOpenTab_NoLeak(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}

	fn, canceled := fakeTabFactory()
	var created int32
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		atomic.AddInt32(&created, 1)
		return fn(allocCtx, targetID)
	}

	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(&created))

	var wg sync.WaitGroup
	var closeErr, openErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, closeErr = m.CloseTab(testSessionID, 0) // triggers last-tab-replacement
	}()
	go func() {
		defer wg.Done()
		_, openErr = m.OpenTab(testSessionID)
	}()
	wg.Wait()

	require.NoError(t, closeErr)
	require.NoError(t, openErr)

	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	require.True(t, ok)
	survivingTabs := len(se.tabs)
	total := m.totalTabCountLocked()
	m.mu.Unlock()

	assert.Equal(t, survivingTabs, total, "totalTabCountLocked must match the actually-reachable tab count")
	assert.GreaterOrEqual(t, survivingTabs, 1, "the browsing context must never end up with zero tabs")

	createdN := atomic.LoadInt32(&created)
	canceledN := atomic.LoadInt32(canceled)
	assert.EqualValues(t, createdN, int32(survivingTabs)+canceledN,
		"every physically-created tab (the original + whatever the race created) must be EITHER "+
			"tracked or canceled — never orphaned")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, survivingTabs)
}

// --- ADR-041 D2: adoption + the FR-060 memory gate on adoption ---

func TestAdoptTarget_AppendsAndActivatesNewTab(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	result, err := m.adoptTarget(testSessionID, target.ID("opened-by-window-open"))
	require.NoError(t, err)
	require.NotNil(t, result.Adopted)
	assert.False(t, result.Unadopted)
	assert.Equal(t, 1, result.Adopted.Index)
	assert.True(t, result.Adopted.Active)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
}

func TestAdoptTarget_AlreadyTracked_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tid := target.ID("dup-target")
	result1, err := m.adoptTarget(testSessionID, tid)
	require.NoError(t, err)
	require.NotNil(t, result1.Adopted)

	result2, err := m.adoptTarget(testSessionID, tid)
	require.NoError(t, err)
	assert.Nil(t, result2.Adopted, "adopting an already-tracked target must be a silent no-op")
	assert.False(t, result2.Unadopted, "already-tracked is a true no-op, not a reportable Unadopted case")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "must not double-adopt the same target")
}

func TestAdoptTarget_MemoryPressure_ReportsUnadopted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(1)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	result, err := m.adoptTarget(testSessionID, target.ID("runaway-window-open"))
	require.NoError(t, err, "capped adoption is not an error — a runaway window.open loop must not error the caller")
	assert.Nil(t, result.Adopted)
	require.True(
		t,
		result.Unadopted,
		"ADR-041 fix F2: a detected-but-refused target must be reported, not silently dropped",
	)
	assert.Equal(t, tabAdoptReasonMemoryPressure, result.Reason)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "an adoption the memory gate refuses must be dropped, not appended")
}

func TestAdoptTarget_NoBrowsingContext_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	// No Session() call yet — nothing to adopt into.
	result, err := m.adoptTarget(testSessionID, target.ID("orphan"))
	require.NoError(t, err)
	assert.Nil(t, result.Adopted)
	assert.False(t, result.Unadopted)
}

// TestAdoptTarget_ConcurrentAdoptionOfSameTarget_AllCallersSeeTheSameOutcome
// is the concurrency-safety guard for adoptTarget's pendingAdoptEntry design
// (see its doc comment in manager.go). It supersedes an earlier version of
// this test (renamed from *_OnlyOneWins) whose "exactly one caller sees
// Adopted" assertion encoded the OLD, since-fixed contract: a "losing"
// concurrent caller used to get told nothing happened (a blind no-op) even
// though the winner went on to succeed. That is exactly the gap that made
// browser_click silently report plain success on a target="_blank" click —
// its own ReconcileTabs call routinely LOST this same race to the async
// passive listener (a real CDP target-created event can be dispatched before
// the click's own CDP round trip even returns), so it saw "already pending"
// and reported nothing. The fix: a losing caller now WAITS for the winner's
// actual result and returns THAT, so every caller asking about the same
// target — winner and waiters alike — ends up with the SAME, correct answer.
// The one invariant that must still hold unconditionally: exactly ONE
// physical tab gets created for the target, never a duplicate, no matter how
// many concurrent callers ask about it.
//
// The n-1 "losing" callers are deliberately synchronized to land WHILE the
// winner's adoption is in flight — not left to an unsynchronized `go`
// burst against createTabFn's near-instant fake, which flaked under CI's
// contended (-p 4, shared 8-core) full-suite runs: heavy external
// scheduling pressure could let some of the n-1 goroutines receive no CPU
// time at all until AFTER the fake createTab (a few instructions, no real
// I/O) had already returned and the winner had fully finished, appending
// the tab to se.tabs and unlocking. Those late arrivals then hit
// adoptTarget's OWN top-of-function "already ours" fast path — a true,
// deliberate no-op (see TestAdoptTarget_AlreadyTracked_IsNoop) for a
// caller that finds a target already tracked, indistinguishable from a
// legitimate sequential re-adoption of a target this same test cannot
// (and must not) also report as freshly Adopted. That is a test
// synchronization gap, not a pendingAdoptEntry bug: the fix here is to
// block the winner inside createTab (via createTabFn) until every losing
// caller is confirmed in flight, so all n-1 deterministically observe the
// registered pendingAdoptEntry and take the WAIT branch this test exists
// to exercise, regardless of scheduler fairness.
func TestAdoptTarget_ConcurrentAdoptionOfSameTarget_AllCallersSeeTheSameOutcome(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tid := target.ID("raced-target")
	const n = 8

	baseFn := m.createTabFn
	registered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// adoptTarget's pendingAdopt gate (manager.go) guarantees only ONE
	// caller ever reaches createTabFn for a given targetID — every other
	// concurrent caller either becomes a waiter on the registered entry or
	// (the case under test) must be given the chance to become one. So
	// gating on targetID == tid here only ever fires for the winner.
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		if targetID == tid {
			once.Do(func() { close(registered) })
			<-release
		}
		return baseFn(allocCtx, targetID)
	}

	var wg sync.WaitGroup
	results := make([]tabAdoptResult, n)
	errs := make([]error, n)

	// Launch the winner alone first so it deterministically registers the
	// pendingAdoptEntry (manager.go) and then blocks inside createTabFn —
	// removing any reliance on scheduler luck to land the other n-1 calls
	// while the (otherwise near-instant) fake adoption is still in flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], errs[0] = m.adoptTarget(testSessionID, tid)
	}()
	select {
	case <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("winner never reached createTabFn / registered the pendingAdoptEntry")
	}

	// The winner is now confirmedly blocked mid-adoption with its entry
	// still registered in m.pendingAdopt. Launch the other n-1 callers —
	// no matter how long the scheduler takes to actually run each one, the
	// entry stays put (the winner cannot proceed until release is closed
	// below), so every one of them is guaranteed to find it once it does
	// run and take the WAIT branch.
	for i := 1; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = m.adoptTarget(testSessionID, tid)
		}(i)
	}
	// A generous, fixed safety margin (orders of magnitude larger than a
	// handful of uncontended mutex acquisitions ever need, even under heavy
	// external CPU pressure) for the n-1 goroutines above to actually run
	// far enough to observe the still-registered entry before the winner is
	// released. There is no signal to wait on instead: reaching the
	// internal "already pending, wait on entry.done" select is exactly the
	// unexported implementation detail this black-box test must not reach
	// into to observe directly.
	time.Sleep(50 * time.Millisecond)
	close(release)

	wg.Wait()

	adoptedCount := 0
	for i := range n {
		require.NoError(t, errs[i])
		if results[i].Adopted != nil {
			adoptedCount++
			assert.Equal(t, 1, results[i].Adopted.Index,
				"every caller that sees Adopted must see the SAME adopted tab, not a duplicate")
		}
	}
	assert.Equal(t, n, adoptedCount,
		"ALL concurrent callers asking about the same target must see it was adopted — a losing racer waits "+
			"for and returns the winner's actual outcome instead of a blind no-op (pendingAdoptEntry)")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "the target must be adopted EXACTLY ONCE — no duplicate tab despite 8 concurrent callers")
}

// --- ADR-041 D2: ReconcileTabs ---

func TestReconcileTabs_AdoptsNewlyDetectedTarget(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	rootCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotNil(t, rootCtx)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{
				TargetID: target.ID("new-blank-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://cal.com/booking",
			},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	require.True(t, outcome.Adopted)
	require.NotNil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)
	assert.Equal(t, 1, outcome.NewActive.Index)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
}

func TestReconcileTabs_IgnoresUnrelatedAndAlreadyTrackedTargets(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			// No OpenerID at all — a top-level target, not opened by us.
			{TargetID: target.ID("unrelated-top-level"), Type: "page", OpenerID: ""},
			// Opened by something outside this browsing context entirely.
			{TargetID: target.ID("unrelated-child"), Type: "page", OpenerID: target.ID("some-other-tab")},
			// A non-page target (e.g. a service worker) must be ignored.
			{TargetID: target.ID("a-worker"), Type: "service_worker", OpenerID: rootTargetID},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "nothing here was opened by our own tab set — nothing should be adopted")
}

func TestReconcileTabs_NoBrowsingContext_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)
}

func TestReconcileTabs_ListTargetsError_IsPropagated(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	wantErr := fmt.Errorf("simulated CDP transport failure")
	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return nil, wantErr
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
}

// TestReconcileTabs_MaxTabsCap_ReportsUnadopted drives ReconcileTabs through
// a tab set the memory gate refuses to grow, and asserts the outcome reports the
// stranded tab — ADR-041 fix F2's regression guard, the exact failure class
// the ADR was written to prevent: a click opens a new tab that can't be
// adopted, and the pre-fix code silently reported nothing at all instead of
// telling the agent a tab was stranded. applyReconcileOutcome (tools.go) is
// what turns this into browser_click's
// tab_opened_but_not_adopted/reason/note result fields; this test proves the
// manager-level signal it consumes is actually populated.
func TestReconcileTabs_MemoryPressure_ReportsUnadopted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(1) // the root tab already exhausts the headroom
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{
				TargetID: target.ID("stranded-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://cal.com/booking",
			},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	require.True(t, outcome.Unadopted, "a genuinely new tab was detected but could not be adopted — must be reported")
	assert.Equal(t, tabAdoptReasonMemoryPressure, outcome.Reason)

	// The result map browser_click actually returns to the agent.
	result := map[string]any{"success": true}
	applyReconcileOutcome(result, outcome)
	assert.Equal(t, true, result["tab_opened_but_not_adopted"])
	assert.Equal(t, string(tabAdoptReasonMemoryPressure), result["reason"])
	// FR-063: the reason the model branches on must be the memory code, and
	// the note must name a remedy without naming a limit or a config key.
	assert.Equal(t, "memory_pressure", result["reason"])
	assert.Contains(t, result["note"], "browser_close_tab")
	reconcileNote, isString := result["note"].(string)
	require.True(t, isString, "the note must be a string the model can read")
	assert.NotContains(t, strings.ToLower(reconcileNote), deletedTabCapConfigKey)
	assert.NotEmpty(t, result["note"], "the agent needs a human-readable explanation, not just a machine reason code")
	assert.Nil(t, result["opened_new_tab"], "an unadopted tab must not ALSO be reported as opened_new_tab")
}

// TestReconcileTabs_OneClickTwoNewTargets_OneAdoptedOneStranded is the
// second-fix-wave regression guard for the exact bug UAT caught: a single
// click that spawns TWO new CDP targets in one go, where the first is
// adopted (filling the memory headroom) and the second is then refused. Before
// this fix, ReconcileOutcome aggregated both signals correctly at the
// manager level, but applyReconcileOutcome's if/else-if (tools.go) reported
// only the FIRST-matched signal (Adopted) to the agent and silently dropped
// the second target's Unadopted/Reason — the agent was told a tab opened but
// never told a second one was stranded. Asserts BOTH the ReconcileOutcome
// itself AND the applyReconcileOutcome result map carry both signals.
func TestReconcileTabs_OneClickTwoNewTargets_OneAdoptedOneStranded(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	// Root tab (1) + exactly one more (2) fits under the headroom; a third does
	// not. This is the same oracle the deleted two-tab cap expressed, now
	// expressed against live memory (FR-060).
	m.memoryPressureFn = refuseTabsAtOrAbove(2)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	// One click handler that opened two target="_blank" links: the first
	// fills the remaining headroom, the second is then refused.
	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{
				TargetID: target.ID("adoptable-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://example.com/a",
			},
			{
				TargetID: target.ID("stranded-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://example.com/b",
			},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)

	// Both signals must survive the aggregation — neither clears the other.
	// (The adopted tab's URL isn't asserted here: fakeTabFactory, the test
	// seam used by newTestManagerWithFakeTabs, never populates title/url —
	// only the real createTab does, via refreshTabMeta's chromedp calls —
	// mirroring TestReconcileTabs_AdoptsNewlyDetectedTarget's identical
	// omission above.)
	require.True(t, outcome.Adopted, "the first new target must be reported as adopted")
	require.NotNil(t, outcome.NewActive)
	assert.Equal(t, 1, outcome.NewActive.Index)
	require.True(t, outcome.Unadopted, "the second new target must be reported as stranded, not silently dropped")
	assert.Equal(t, tabAdoptReasonMemoryPressure, outcome.Reason)
	assert.Equal(t, 1, outcome.UnadoptedCount)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "exactly one of the two new targets is adopted — the memory gate stops the second")

	// The result map browser_click actually returns to the agent must carry
	// BOTH keys — this is the exact bug: an if/else-if here used to report
	// only one of the two.
	result := map[string]any{"success": true}
	applyReconcileOutcome(result, outcome)
	assert.Equal(t, true, result["opened_new_tab"], "the adopted tab must still be reported")
	assert.Equal(t, true, result["tab_opened_but_not_adopted"], "the stranded tab must ALSO be reported, not dropped")
	assert.Equal(t, string(tabAdoptReasonMemoryPressure), result["reason"])
	assert.NotEmpty(t, result["note"], "the agent needs a human-readable explanation of both outcomes")
}

// --- ADR-041 D4: tabs-changed callback wiring ---

func TestSetTabsChangedFunc_FiresOnOpenSwitchClose(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	var mu sync.Mutex
	var calls []int // recorded activeIdx per call
	m.SetTabsChangedFunc(func(sessionID string, tabs []Tab, activeIdx int) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, activeIdx)
	})

	_, err := m.Session(testSessionID) // fires once (new tab)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // fires once (activeIdx=1)
	require.NoError(t, err)
	_, err = m.SwitchTab(testSessionID, 0) // fires once (activeIdx=0)
	require.NoError(t, err)
	_, _, err = m.CloseTab(testSessionID, 1) // fires once
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 4)
	assert.Equal(t, []int{0, 1, 0, 0}, calls)
}

func TestSetTabsChangedFunc_NeverInvokedWithLockHeld(t *testing.T) {
	// Regression guard for the ADR-038 "no callback under the manager lock"
	// rule: the callback must be able to call BACK into the manager (e.g.
	// ListTabs) without deadlocking.
	m := newTestManagerWithFakeTabs(t)
	done := make(chan struct{})
	m.SetTabsChangedFunc(func(sessionID string, tabs []Tab, activeIdx int) {
		// If notifyTabsChanged were called with m.mu held, this would
		// deadlock and the test would time out (require.Eventually below).
		_, _, err := m.ListTabs(sessionID)
		assert.NoError(t, err)
		close(done)
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	select {
	case <-done:
	default:
		t.Fatal("tabs-changed callback never completed — possible deadlock calling back into the manager")
	}
}

// TestOpenTab_IsTheControlGateEscapeHatch is D-G's requirement, asserted from
// the production path: with a human holding the wheel, browser_open_tab must
// NOT come back as a deferral — it must reach the browser and fail on the
// unreachable CDP endpoint, exactly like an ungated tool.
//
// Why it is worth its own test rather than a row in a table. The agent is
// told, in three separate places (browser_handover's tool description, its
// success message and its FR-052 refusal message), that opening a new tab is
// the way to keep working while the operator holds the wheel. Before D-G's
// carve-out landed, browser_open_tab went through controlledResult like every
// other write verb and returned a deferral — the instruction was unfollowable,
// and nothing failed to say so. This test is what makes that promise
// falsifiable.
//
// BDD: Given a human viewer currently controls the live browser session,
// When browser_open_tab's Execute is called,
// Then the result is NOT a control-gate deferral.
func TestOpenTab_IsTheControlGateEscapeHatch(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"),
		"test setup: taking control must succeed on an uncontrolled session")
	require.True(t, mgr.Live().IsStoodDown(testSessionID),
		"test setup: the take must actually have stood the tab set down, or this test proves nothing")

	result := mustGetTool(t, registry, "browser_open_tab").Execute(ctx, map[string]any{})
	require.NotNil(t, result)
	assert.NotContains(t, result.ForLLM, "human is currently controlling",
		"D-G: browser_open_tab must not defer while the wheel is held; got: %s", result.ForLLM)
	assert.NotContains(t, result.ForLLM, `"deferred":true`,
		"D-G: browser_open_tab must not defer while the wheel is held; got: %s", result.ForLLM)
	assert.Nil(t, result.Deferred,
		"D-G: the structural deferral signal the turn engine's ledger reads must be absent too; got: %+v",
		result.Deferred)
	assert.True(t, result.IsError,
		"browser_open_tab must have gone on to attempt real execution and failed on the unreachable "+
			"CDP endpoint — a non-error, non-deferral result would mean it short-circuited somewhere "+
			"else and this test proves nothing; got: %s", result.ForLLM)
}

// TestOpenTab_IsExcludedFromTheEngineShortCircuitSet is the other half of
// D-G's carve-out. ControlGatedToolNames() is consumed once, at construction,
// by pkg/agent/browser_deferral.go to build the FR-016 set the TURN ENGINE
// short-circuits before dispatch after three deferrals in one turn. A held
// wheel produces exactly those three deferrals, so leaving browser_open_tab in
// that set would slam the escape hatch shut from the engine side on precisely
// the turn the agent needs it — with the gate itself never consulted.
func TestOpenTab_IsExcludedFromTheEngineShortCircuitSet(t *testing.T) {
	names := ControlGatedToolNames()
	require.NotEmpty(t, names, "an empty roster would pass the exclusion assertion vacuously")
	assert.NotContains(t, names, "browser_open_tab",
		"D-G: the engine's short-circuit set must not contain the one tool the gate lets through")
	// Differentiation: a sibling write verb that is NOT the escape hatch must
	// still be in the set, so this cannot pass by the roster being broken.
	assert.Contains(t, names, "browser_switch_tab",
		"the roster must still carry the gated tab verbs — an empty/broken roster would make the "+
			"exclusion above meaningless")
}

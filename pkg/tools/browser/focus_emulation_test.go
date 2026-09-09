package browser

// focus_emulation_test.go — review finding F9 (2026-08-13): focus emulation
// must be part of the treatment on EVERY path that changes which tab Chrome is
// compositing for, and must be released from the tab being left.
//
// The gap: Emulation.setFocusEmulationEnabled(true) was applied in exactly one
// place, CaptureSession.bringAgentTabToFront (capture start, and its one warm
// re-assert). The tab-switch path — BrowserManager.activateTabInChrome — did
// Page.bringToFront and nothing else, and browser_open_tab told Chrome nothing
// at all. Since a tab change fires onTabsChanged → CaptureSession.Recapture,
// the encoder re-bound to a tab under a DIFFERENT rendering regime than the
// one capture start had established, one browser_switch_tab after start. The
// tab being left, meanwhile, kept its emulation forever.
//
// What was measured, so the next reader does not over- or under-claim (this
// project's own Chrome, headless, rAF ticks under a full-viewport animation):
//
//   - Releasing the OLD tab is a real, reproducible win: a tab switched away
//     from kept running at 25–35 rAF/s while still emulated, and dropped to
//     0 rAF/s the moment emulation was cleared (4/4 paired trials). Nothing
//     captures or displays a background tab, so that is pure waste.
//   - Emulating the NEW tab was NOT measurably faster in that environment: a
//     brought-to-front tab ran at 60 rAF/s with and without it (6/6 trials).
//     So these tests pin CONSISTENCY — every foregrounded tab gets identical
//     treatment — not a claimed framerate gain on the switched-TO tab. The
//     framerate claim in capture_session.go's original comment did not
//     reproduce and must not be leaned on.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestFocusTreatmentActions pins the two action sequences themselves, so a
// future edit that drops the emulation half from foregroundTabActions (the
// original defect) fails here even if every call site is still wired up.
func TestFocusTreatmentActions(t *testing.T) {
	assert.Equal(t, "foreground", focusTreatment(foregroundTabActions()),
		"foregroundTabActions must bring the tab to front AND enable focus emulation")
	assert.Equal(t, "background", focusTreatment(backgroundTabActions()),
		"backgroundTabActions must disable focus emulation and must NOT bring anything to front")
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

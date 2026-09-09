package browser

// live_deadlock_test.go — regression coverage for the ADR-038 live-view
// deadlock postmortem (UAT finding, 2026-07-11) and the ADR-041 tab-switch
// false-death-broadcast fixes, both re-targeted at the ADR-061 mechanism
// (JPEG CDP screencast removed — WebRTC, ADR-047, is now the only
// live-browser video path; this package's LiveView is session/control-lock/
// tab-strip bookkeeping only).
//
// ORIGINAL BUG (kept for context — the property below still guards it):
// while a live-view screencast was active, a heavy page caused
// browser_screenshot to time out, and every subsequent browser tool call
// then hung indefinitely. Root cause: LiveView.attach() held lv.mu (via
// defer lv.mu.Unlock()) across a blocking page.StartScreencast() chromedp.Run
// call. attach() (and rebindWatch, its tab-switch counterpart) no longer make
// any CDP call at all — see live.go's attach()/rebindWatch doc comments for
// why that closes the whole deadlock class structurally, not just by
// bounding the call with a timeout. What remains load-bearing, and what this
// file still guards, is the ADR-041 property that a tab switch must never
// cause the death-watch to fire a FALSE "session ended unexpectedly"
// broadcast — that matters just as much for a WebRTC-only session today,
// since watchForUnexpectedDeath is also what stops an orphaned WebRTC
// CaptureSession on a genuine browser death.
//
// Tests removed as part of the JPEG-screencast removal (ADR-061): the ack-
// worker coalescing tests (queueAck/runAckWorker no longer exist), the
// mutex-not-held-across-a-CDP-call test (attach() makes no CDP call to hold
// the mutex across), the rebind-start-failure test (rebindWatch cannot fail
// — there is no CDP call to fail), and the two ADR-041 second-fix-wave
// interleaving tests (Finding A "orphaned screencast on last-viewer-detach
// during teardown" and Finding B "concurrent rebind drops its target") —
// both races existed only because the old rebind released lv.mu across a
// CDP round trip; rebindWatch now runs start-to-finish under one lv.mu
// acquisition, so the interleaving windows those tests drove are
// structurally unreachable.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveView_RebindWatch_NoFalseDeathBroadcast is the regression guard for
// ADR-041 Finding F1, re-targeted at rebindWatch: a tab switch must install
// the new watch epoch BEFORE canceling the old one, so the OLD epoch's
// background watchForUnexpectedDeath goroutine (started by attach()) always
// observes lv.listenCtx already pointing at the NEW epoch by the time it
// wakes, and therefore never mistakes an ordinary switch for a genuine death.
func TestLiveView_RebindWatch_NoFalseDeathBroadcast(t *testing.T) {
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}

	var statusMu sync.Mutex
	var statusMessages []string
	onStatus := func(msg string) {
		statusMu.Lock()
		statusMessages = append(statusMessages, msg)
		statusMu.Unlock()
	}

	lv := &LiveView{
		mgr:          mgr,
		sessionID:    "s1",
		viewers:      make(map[string]struct{}),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
		tabsSinks:    make(map[string]TabsSink),
		runCDP:       runCDPWithTimeout,
	}

	oldTabCtx, oldCancel := chromedp.NewContext(context.Background())
	defer oldCancel()

	controlledByOther, err := lv.attach(oldTabCtx, "viewer1", onStatus, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)

	lv.mu.Lock()
	oldListenCtx := lv.listenCtx
	lv.mu.Unlock()
	require.NotNil(t, oldListenCtx, "attach() must have installed an active watch epoch")

	newTabCtx, newCancel := chromedp.NewContext(context.Background())
	defer newCancel()

	lv.rebindWatch(newTabCtx)

	// Give the REAL background watcher goroutine (started by attach() for
	// the old epoch) every opportunity to misfire before asserting it
	// didn't — rebindWatch installs the new epoch under lv.mu before ever
	// canceling the old one, so this is a genuine happens-before guarantee,
	// not an unlikely-to-lose race.
	time.Sleep(100 * time.Millisecond)

	statusMu.Lock()
	got := append([]string(nil), statusMessages...)
	statusMu.Unlock()
	assert.Empty(t, got, "an ordinary tab switch must never emit a false "+
		"'session ended unexpectedly' broadcast to attached viewers: %v", got)

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.NotNil(t, lv.listenCtx, "rebindWatch must install a new epoch after a successful switch, "+
		"not leave the live view dead")
	assert.True(t, lv.listenCtx != oldListenCtx, "the new epoch's listenCtx must be a fresh context, not the old one")
	assert.True(t, sameChromedpContext(newTabCtx, lv.tabCtx), "lv.tabCtx must now point at the newly active tab")
}

// ---------------------------------------------------------------------------
// Live-UAT fix, 2026-07-12 — "closing the ACTIVE tab fires a false 'browser
// session ended unexpectedly' banner and leaves the live view frozen on the
// CLOSED tab's stale content", confirmed 2/2 by two independent live testers
// via the live panel WITH A VIEWER ATTACHED. Root cause: BrowserManager.
// CloseTab cancels the closed tab's own chromedp context BEFORE calling
// notifyTabsChanged; since LiveView.listenCtx is a CHILD of the active tab's
// context, that cancellation synchronously kills listenCtx too — so by the
// time onTabsChanged/rebindWatch run, the epoch already looks "dead"
// (isActiveLocked()==false) even though the browser and the surviving tab
// are perfectly alive, deterministically skipping the rebind onTabsChanged
// owed; watchForUnexpectedDeath's background goroutine, racing in on that
// same dead listenCtx, then fires a FALSE death broadcast. The fix:
// hasEpochLocked (weaker than isActiveLocked — "an epoch is installed, dead
// or alive") drives the rebind decision, and watchForUnexpectedDeath
// consults BrowserManager.browserAlive before ever broadcasting — see both
// functions' doc comments in live.go.
// ---------------------------------------------------------------------------

// TestLiveView_CloseActiveTab_NoFalseDeathAndRebindsToSurvivor is the
// regression guard for the live-UAT close-active-tab fix. It attaches a
// viewer with a StatusSink, opens a second tab, closes the ACTIVE tab, and
// asserts (a) the StatusSink never receives a "session ended"/error status,
// and (b) the LiveView's tabCtx converges on the surviving tab rather than
// staying stuck on the closed tab or going dead. Drives the fix through the
// REAL BrowserManager.CloseTab path (not a hand-built LiveView) — via
// newTestManagerWithFakeTabs's createTabFn seam (tabs_test.go) — with a real
// *LiveViewRegistry wired via newLiveViewRegistry, exercising the ACTUAL
// tab-context cancellation CloseTab performs (closing.cancel() in
// manager.go), which is the load-bearing trigger for this bug.
func TestLiveView_CloseActiveTab_NoFalseDeathAndRebindsToSurvivor(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	reg := newLiveViewRegistry(m)

	_, err := m.Session(testSessionID) // tab 0
	require.NoError(t, err)
	tab1, err := m.OpenTab(testSessionID) // tab 1, becomes active
	require.NoError(t, err)
	require.Equal(t, 1, tab1.Index)
	require.True(t, tab1.Active)

	survivorCtxBeforeClose, err := m.Session(testSessionID)
	require.NoError(t, err)

	var statusMu sync.Mutex
	var statusMsgs []string
	onStatus := func(msg string) {
		statusMu.Lock()
		statusMsgs = append(statusMsgs, msg)
		statusMu.Unlock()
	}

	controlledByOther, err := reg.Attach(testSessionID, "viewer1", onStatus, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)

	lv := reg.view(testSessionID)
	lv.mu.Lock()
	require.Equal(
		t,
		survivorCtxBeforeClose,
		lv.tabCtx,
		"sanity: the live view must be bound to tab 1 (the active tab) before the close",
	)
	lv.mu.Unlock()

	// Close the ACTIVE tab (index 1) — the exact live-UAT repro. Tab 0 slides
	// in as the new active tab.
	closedTabs, newActiveIdx, err := m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, closedTabs, 1)
	require.Equal(t, 0, newActiveIdx)

	survivorCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotEqual(
		t,
		survivorCtxBeforeClose,
		survivorCtx,
		"sanity: the survivor is a genuinely different tab context than the closed one",
	)

	// (b) The live view must rebind to the surviving tab — not stay stuck on
	// the closed tab, and not go dead (nil listenCtx).
	require.Eventually(t, func() bool {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		return lv.listenCtx != nil && lv.tabCtx == survivorCtx
	}, 2*time.Second, 5*time.Millisecond,
		"the live view must rebind to the surviving tab after the active tab closes — "+
			"it must not stay frozen on the closed tab's stale content or go dead")

	// (a) No false "session ended" status must EVER reach the attached
	// viewer. By the time the Eventually above succeeded, both the
	// (possibly racing) watcher and the rebind have had every opportunity to
	// run — this is not a best-effort "probably didn't race" check.
	statusMu.Lock()
	got := append([]string(nil), statusMsgs...)
	statusMu.Unlock()
	assert.Empty(t, got,
		"closing the ACTIVE tab (with the browser and the surviving tab alive) must never emit a "+
			"false 'session ended' status to an attached viewer: %v", got)
}

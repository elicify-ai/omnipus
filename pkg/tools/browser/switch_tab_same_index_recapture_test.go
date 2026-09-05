package browser

// switch_tab_same_index_recapture_test.go — coverage for the SECOND half of
// the tab-switch defect class, and for the three paths that moved the model's
// active tab without ever telling Chrome.
//
// The coverage gap that let this ship: live_test.go's
// TestLiveView_OnTabsChanged_ActiveTabSwitch_TriggersCaptureSessionRecapture
// pins the case where the MODEL moved — LiveView.onTabsChanged sees a
// different active-tab context and fires the recapture. Nothing pinned the
// mirror case, where the model did NOT move but Chrome's own idea of the
// active tab had drifted away from it (measured cause: a page-opened tab
// whose adoption failed, so Chrome activated a tab our model never learned
// about). In that state the user clicks the tab strip entry that is ALREADY
// active, SwitchTab genuinely corrects Chrome via Page.bringToFront, returns
// success — and the picture never follows, because onTabsChanged's
// activeTabChanged check is false and nobody asks for a recapture.
//
// These tests assert on the RECAPTURE REQUEST reaching the encoder, because
// that is the only observable the real defect had: no error, no console
// message, no failed call. Just a picture that stayed on the wrong tab.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recaptureLedger counts recapture control frames as the encoder would see
// them — i.e. at the ingest send boundary, the single point BOTH recapture
// entry points (Recapture/RecaptureAt via requestControl, and
// RecaptureForTabChange through them) funnel into. Counting here rather than
// at either method means a test cannot accidentally miss a recapture that
// arrived by the other route.
type recaptureLedger struct {
	mu sync.Mutex
	n  int
}

func (r *recaptureLedger) bind(cs *CaptureSession) {
	cs.BindIngest(func(action string, _ *string, _, _ int, _ int) error {
		if action == "recapture" {
			r.mu.Lock()
			r.n++
			r.mu.Unlock()
		}
		return nil
	}, func() {})
}

func (r *recaptureLedger) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func (r *recaptureLedger) reset() {
	r.mu.Lock()
	r.n = 0
	r.mu.Unlock()
}

// attachTestCaptureSession gives m a real CaptureSession backed by a fake
// relay and a fake encoder starter. mgr is deliberately nil inside the
// session (NewCaptureSessionWithDeps's first argument), so the production
// foreground re-assert short-circuits instead of driving chromedp against a
// never-dialed fake tab context — tests that care about the re-assert install
// foregroundAssertFn explicitly instead.
func attachTestCaptureSession(t *testing.T, m *BrowserManager) (*CaptureSession, *recaptureLedger) {
	t.Helper()
	relay := &fakeRelay{}
	var encoderCalls int32
	cs, err := m.EnsureCaptureSession(func() (*CaptureSession, error) {
		return NewCaptureSessionWithDeps(nil, "agent-1", relay, fakeEncoderStarter(&encoderCalls, nil), nil)
	})
	require.NoError(t, err)
	ledger := &recaptureLedger{}
	ledger.bind(cs)
	return cs, ledger
}

// installFakeLiveRecaptureBridge stands in for the real
// LiveViewRegistry.handleTabsChanged -> LiveView.onTabsChanged path, which is
// what fires the recapture on the NORMAL (model moved) switch. It reproduces
// onTabsChanged's activeTabChanged rule faithfully — resolve the active tab
// via mgr.Session, compare against the last one seen, and only then recapture,
// with the first call establishing the baseline rather than counting as a
// change.
//
// Faithfulness matters here: the whole point of these tests is "exactly ONE
// recapture per switch, whichever half of the system produces it". A bridge
// that recaptured unconditionally would hide a double-fire, and one that never
// recaptured would make the new code look necessary when it is not.
func installFakeLiveRecaptureBridge(m *BrowserManager, cs *CaptureSession) {
	tracker := &activeCtxTracker{}
	m.SetTabsChangedFunc(func(sessionID string, _ []Tab, _ int) {
		newCtx, err := m.Session(sessionID)
		if err != nil {
			return
		}
		if tracker.moved(newCtx) {
			cs.Recapture()
		}
	})
}

// activeCtxTracker mirrors LiveView.lastKnownActiveCtx: it remembers the
// active tab context it was last shown and reports whether the newest one is
// different. The very first call only establishes the baseline (it never
// reports a move), exactly as onTabsChanged's own nil guard does.
type activeCtxTracker struct {
	mu   sync.Mutex
	last context.Context
}

func (a *activeCtxTracker) moved(cur context.Context) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	moved := a.last != nil && a.last != cur
	a.last = cur
	return moved
}

// newThreeTabManagerWithCapture builds a 3-tab browsing context (active = tab
// 2) with a capture session and the live bridge wired, and returns a ledger
// already reset past the setup traffic.
func newThreeTabManagerWithCapture(t *testing.T) (*BrowserManager, *recaptureLedger) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	cs, ledger := attachTestCaptureSession(t, m)
	installFakeLiveRecaptureBridge(m, cs)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = m.OpenTab(testSessionID)
		require.NoError(t, err)
	}
	_, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Equal(t, 2, activeIdx, "setup expects the last-opened tab to be active")

	ledger.reset()
	return m, ledger
}

// eventuallyCount waits for the ledger to reach want, then holds still long
// enough to catch a LATE extra recapture (RecaptureForTabChange runs on its
// own goroutine, so "exactly one" has to be checked over a window, not at an
// instant).
func eventuallyCount(t *testing.T, ledger *recaptureLedger, want int, msg string) {
	t.Helper()
	require.Eventually(t, func() bool { return ledger.count() == want }, 2*time.Second, 5*time.Millisecond, msg)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, want, ledger.count(), msg+" (a late duplicate arrived)")
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

// TestRecaptureAt_DoesNotReassertForeground is the measured trade-off, written
// down as a test. RecaptureAt is the VIEWPORT-RESIZE path and the SPA drives it
// at drag frequency; a CDP round trip there is exactly the starvation the
// hosted 2-CPU box already suffers from. This guard fails if someone later
// "unifies" the two entry points by moving the re-assert down into RecaptureAt.
func TestRecaptureAt_DoesNotReassertForeground(t *testing.T) {
	relay := &fakeRelay{}
	var encoderCalls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&encoderCalls, nil))

	var asserts int32
	cs.mu.Lock()
	cs.foregroundAssertFn = func(context.Context) bool {
		atomic.AddInt32(&asserts, 1)
		return true
	}
	cs.mu.Unlock()
	ledger := &recaptureLedger{}
	ledger.bind(cs)

	for i := 0; i < 20; i++ {
		cs.RecaptureAt(1280, 720)
	}

	assert.Equal(t, 20, ledger.count(), "every resize recapture must still reach the encoder")
	assert.Equal(t, int32(0), atomic.LoadInt32(&asserts),
		"the resize path must stay free of CDP round trips — see RecaptureForTabChange's doc comment")
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

// TestCreateFirstTab_ActivatesFirstTabInChrome. The first-tab path is reached
// on cold start AND on CloseTab's last-tab replacement — the latter being the
// case that matters, because the tab the user was watching has just been
// destroyed and Chrome's active-tab answer at that instant is a fallback of
// its own choosing.
func TestCreateFirstTab_ActivatesFirstTabInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)

	ctx, err := m.Session(testSessionID) // creates the browsing context's first tab
	require.NoError(t, err)

	activations := rec.calls()
	require.Len(t, activations, 1, "creating a browsing context's first tab must tell Chrome it is active")
	assert.True(t, activations[0] == ctx,
		"the activated context must be the tab Session() resolves")
}

// TestCloseLastTab_ActivatesReplacementInChrome is the same guarantee through
// the path that actually motivates it.
func TestCloseLastTab_ActivatesReplacementInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	rec.mu.Lock()
	rec.ctxs, rec.treatment = nil, nil
	rec.mu.Unlock()

	tabs, activeIdx, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err)
	require.Len(t, tabs, 1, "closing the last tab must leave a replacement (ADR-041 D3)")
	require.Equal(t, 0, activeIdx)

	replacementCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	activations := rec.calls()
	require.NotEmpty(t, activations, "the replacement tab must be activated in Chrome")
	assert.True(t, activations[len(activations)-1] == replacementCtx,
		"the LAST activation must land on the replacement tab, not the destroyed one")
}

// --- (d) a failed adoption must not be permanent ---

// flakyTabFactory fails the first failures calls, then behaves like
// fakeTabFactory. Models the measured failure: a CDP attach that times out
// under transport saturation and then succeeds once the transport drains.
func flakyTabFactory(failures int32) (func(context.Context, target.ID) (*tabEntry, error), *int32) {
	attempts := new(int32)
	healthy, _ := fakeTabFactory()
	fn := func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		if atomic.AddInt32(attempts, 1) <= failures {
			return nil, context.DeadlineExceeded
		}
		return healthy(allocCtx, targetID)
	}
	return fn, attempts
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

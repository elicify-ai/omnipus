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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
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

// newThreeTabManagerWithCapture builds a 3-tab browsing context (active = tab
// 2) with a capture session and the live bridge wired, and returns a ledger
// already reset past the setup traffic.
func newThreeTabManagerWithCapture(t *testing.T) (*BrowserManager, *recaptureLedger) {
	t.Helper()
	m, view, cs, ingest, order := newAttachedLiveManager(t)
	_ = view
	_ = ingest
	_ = order
	ledger := &recaptureLedger{}
	_, _, err := cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil }, func(ctx context.Context, _ CaptureFrameState, current func() bool) error {
		if ctx.Err() != nil || !current() {
			return context.Canceled
		}
		ledger.mu.Lock()
		ledger.n++
		ledger.mu.Unlock()
		return nil
	}, func() {})
	require.NoError(t, err)
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

// TestCloseLastTab_ActivatesReplacementInChrome is the same guarantee through
// the path that actually motivates it.
func TestCloseLastTab_ActivatesReplacementInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
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
	assert.True(t, chromedp.FromContext(activations[len(activations)-1]) == chromedp.FromContext(replacementCtx),
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

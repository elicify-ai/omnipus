// live_viewport_resize_test.go — the 2026-08-16 resize fix wave, every case
// measured against a real headless Chrome 152 before it was written:
//
//   - the device-scale override is renderer-bound and must never be able to
//     fail a window resize that has already landed (the operator's "could not
//     resize the browser viewport" toast);
//   - the read-back must WAIT for the renderer to relay out instead of reading
//     once and believing whatever it got;
//   - the chrome-delta compensation must correct a shortfall only, never an
//     overshoot (that arithmetic collapsed a 633px request into a 1px window);
//   - when the video frame and the remembered tab size disagree, the TAB is
//     asked which is right, and whichever one is wrong gets fixed;
//   - a new tab must inherit the panel's viewport and sharpness, because
//     Chrome's scale override is per tab, not per window;
//   - refilling an invalidated cache must restore the scale, not leave it at
//     zero forever.

package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// isScaleAction reports whether a CDP action is the cosmetic device-scale
// override (set or clear), as opposed to the load-bearing window resize.
func isScaleAction(a chromedp.Action) bool {
	switch a.(type) {
	case *emulation.SetDeviceMetricsOverrideParams, *emulation.ClearDeviceMetricsOverrideParams:
		return true
	}
	return false
}

// When the video frame's shape disagrees with the remembered tab size, the tab
// is asked. Here it backs the CACHE — the operator's 2026-08-15 install, where
// a cached 633x686 met a 1600x1018 capture and the cache was RIGHT (the encoder
// was letterboxing a stream pinned at a size the tab no longer had).
//
// Two things must follow: clicks keep mapping through the tab's real size, and
// a fresh capture is requested so the PICTURE is fixed too — not just the
// arithmetic behind it, which is all the previous mitigation did.
func TestViewportBasis_TabBacksTheCache_KeepsMappingAndRequestsRecapture(t *testing.T) {
	relay := &fakeRelay{}
	mgr := &BrowserManager{started: true}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-basis", relay, fakeEncoderStarter(new(int32), nil), nil)
	require.NoError(t, err)
	mgr.captures = map[string]*CaptureSession{"s1": cs}

	var probes int
	lv := &LiveView{
		sessionID: "s1",
		mgr:       mgr,
		viewers:   make(map[string]struct{}),
		runCDP: func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				probes++
				*lm.w, *lm.h = 633, 686 // the tab backs the cache
			}
			return nil
		},
	}
	lv.cssViewportW, lv.cssViewportH = 633, 686
	lv.cssViewportScale = 2

	const capW, capH = 1600.0, 1018.0
	basisW, basisH := lv.viewportBasisForCapture(context.Background(), capW, capH, 633, 686)

	require.Equal(t, 633.0, basisW, "the tab confirmed the cache — clicks must keep mapping through it")
	require.Equal(t, 686.0, basisH)
	require.Equal(t, 1, probes, "asking the tab costs exactly one round trip")
	require.Equal(t, 1, relay.recaptureCount(),
		"a capture that does not depict the tab must be re-taken, not merely mapped around")

	// The probe verdict is memoized, so a stream of input events does not put a
	// round trip in front of every mouse move, and the recapture is rate-limited
	// so a persisting mismatch cannot loop the video.
	for i := 0; i < 20; i++ {
		lv.viewportBasisForCapture(context.Background(), capW, capH, 633, 686)
	}
	require.Equal(t, 1, probes, "the probe verdict must be reused, not re-asked per input event")
	require.Equal(t, 1, relay.recaptureCount(), "the recapture request must be rate-limited")

	// The warning latches per capture geometry, not once per LiveView: a
	// mismatch that recurs at a NEW geometry must be reported again rather than
	// looking like one historical event somebody already dealt with.
	lv.mu.Lock()
	firstKey := lv.basisWarnedKey
	lv.mu.Unlock()
	lv.viewportBasisForCapture(context.Background(), 1920.0, 1200.0, 633, 686)
	lv.mu.Lock()
	secondKey := lv.basisWarnedKey
	lv.mu.Unlock()
	require.NotEqual(t, firstKey, secondKey, "a new capture geometry must be able to warn again")
}

// The other direction: the tab backs the CAPTURE, so the remembered size is the
// stale one and must be refreshed from the tab rather than mapped through.
func TestViewportBasis_TabBacksTheCapture_RefreshesTheStaleCache(t *testing.T) {
	lv := &LiveView{
		sessionID: "s1",
		viewers:   make(map[string]struct{}),
		runCDP: func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				*lm.w, *lm.h = 633, 686 // the tab backs the capture's shape
			}
			return nil
		},
	}
	lv.cssViewportW, lv.cssViewportH = 633, 543 // stale
	lv.cssViewportScale = 2

	basisW, basisH := lv.viewportBasisForCapture(context.Background(), 1266, 1372, 633, 543)
	require.Equal(t, 633.0, basisW)
	require.Equal(t, 686.0, basisH, "the tab's own answer becomes the basis")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 686, lv.cssViewportH, "and the stale cache is corrected, not just worked around")
}

// Chrome's deviceScaleFactor override is per TARGET, not per window (measured
// 2026-08-16: tab A reports devicePixelRatio 2 while a tab opened afterwards in
// the same window reports 1, with identical innerWidth/innerHeight). So without
// re-applying, every newly-opened tab renders at 1x while the encoder is still
// capturing it at 2x — a visibly soft picture on every single tab open.
func TestOnTabsChanged_ReAppliesTheViewportToTheNewlyActiveTab(t *testing.T) {
	tabOld, cancelOld := context.WithCancel(context.Background())
	t.Cleanup(cancelOld)
	tabNew, cancelNew := context.WithCancel(context.WithValue(context.Background(), viewportTargetTestKey{}, "new"))
	t.Cleanup(cancelNew)

	mgr := &BrowserManager{
		started: true,
		sessions: map[string]*sessionEntry{
			"s1": {
				tabs:      []*tabEntry{{ctx: tabNew, cancel: cancelNew, targetID: "reapply-new"}},
				activeIdx: 0,
			},
		},
	}
	relay := &fakeRelay{}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-reapply", relay, fakeEncoderStarter(new(int32), nil), nil)
	require.NoError(t, err)
	mgr.captures = map[string]*CaptureSession{"s1": cs}

	type applied struct {
		bounds []windowBoundsAction
		scales int
		ctxs   []context.Context
	}
	var got applied
	done := make(chan struct{})
	lv := &LiveView{
		mgr:                mgr,
		sessionID:          "s1",
		viewers:            make(map[string]struct{}),
		statusSinks:        make(map[string]StatusSink),
		controlSinks:       make(map[string]ControlSink),
		tabsSinks:          make(map[string]TabsSink),
		lastKnownActiveCtx: tabOld,
		lastRequestedW:     633,
		lastRequestedH:     686,
		lastRequestedScale: 2,
	}
	lv.cssViewportW, lv.cssViewportH = 633, 686 // describes the tab we are LEAVING
	lv.runCDP = func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			got.bounds = append(got.bounds, a)
			got.ctxs = append(got.ctxs, ctx)
		case viewportFrameGeometryAction:
			*a.width, *a.height, *a.scale = 633, 686, 2
		case layoutMetricsAction:
			*a.w, *a.h = 633, 686
			select {
			case <-done:
			default:
				close(done)
			}
		}
		if isScaleAction(actions[0]) {
			got.scales++
		}
		return nil
	}

	lv.onTabsChanged(nil, 0)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the newly-active tab was never resized — it keeps the old tab's size and 1x sharpness")
	}
	require.Eventually(t, func() bool {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		return !lv.viewportReapplyInFlight
	}, 5*time.Second, 10*time.Millisecond, "the re-apply never finished")

	require.NotEmpty(t, got.bounds, "the panel's viewport must be re-applied to the new tab")
	require.Equal(t, 633, got.bounds[0].width)
	require.Equal(t, 686, got.bounds[0].height)
	require.Positive(t, got.scales,
		"the sharpness override is per tab, so it must be re-applied too or the new tab renders at 1x")
	require.Equal(t, "new", got.ctxs[0].Value(viewportTargetTestKey{}),
		"the resize must target the tab the user switched TO, not the one they left")
}

// After any cache invalidation the scale used to stay at zero forever, because
// the refill can read the layout viewport but Page.getLayoutMetrics cannot
// report a scale. That silently disabled the capture-derived fallback for the
// rest of the session, with nothing anywhere saying so.
func TestRescaleCacheRefillRestoresTheAppliedScale(t *testing.T) {
	lv := &LiveView{
		sessionID:        "s1",
		viewers:          make(map[string]struct{}),
		lastAppliedScale: 2,
		runCDP: func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				*lm.w, *lm.h = 640, 360
			}
			return nil
		},
	}

	_, _, ok := lv.rescaleToCSSViewport(context.Background(), 10, 10, 320, 180)
	require.True(t, ok)

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 640, lv.cssViewportW)
	require.Equal(t, 2.0, lv.cssViewportScale,
		"the scale still in force on the tab must come back with the dimensions, not be lost")
}

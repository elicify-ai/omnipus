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
	"fmt"
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

// A resize that HAS ALREADY HAPPENED must not be reported to the user as a
// failure because the renderer was slow to answer a sharpness request.
//
// Measured 2026-08-15 with the renderer blocked for 7s: getWindowForTarget
// 53ms, setWindowBounds 75ms — the window was resized 128ms in — and
// setDeviceMetricsOverride 6825ms. Bundled under one 5s budget that was a
// DeadlineExceeded, a full retry, up to 10s of stalling, and the operator's
// "could not resize the browser viewport" toast for a resize that had
// succeeded. It also returned before the read-back, so the cached viewport
// kept describing the PRE-resize tab and mis-aimed every later click.
func TestSetViewport_SlowScaleOverrideDoesNotFailAResizeThatLanded(t *testing.T) {
	var boundsCalls []windowBoundsAction
	var scaleCalls int
	runCDP := func(_ context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			require.Len(t, actions, 1,
				"the window resize must be issued alone, so a renderer-bound call cannot spend its budget")
			boundsCalls = append(boundsCalls, a)
			return nil
		case layoutMetricsAction:
			*a.w, *a.h = 615, 744
			return nil
		}
		if isScaleAction(actions[0]) {
			scaleCalls++
			require.Equal(t, viewportScaleTimeout, timeout,
				"the scale override must be spent against its OWN budget")
			return context.DeadlineExceeded
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 615, 744, 2)

	require.NoError(t, err,
		"a timed-out sharpness setting must never surface as a failed resize — the window is already the right size")
	require.True(t, applied)
	require.Len(t, boundsCalls, 1,
		"the resize landed on the first attempt; a slow scale override must not trigger the resize retry")
	require.Equal(t, 1, scaleCalls, "the scale override is attempted once and then given up on")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 615, lv.cssViewportW,
		"the sequence must continue to the read-back, so the cache describes the tab AFTER the resize")
	require.Equal(t, 744, lv.cssViewportH)
	require.Zero(t, lv.cssViewportScale,
		"the override did not land, so the scale is genuinely unknown and must not be recorded as if it had")
}

// The "resolution collapse" class: a read-back LARGER than the request.
//
// `width + (width - actual)` assumed the tab always comes back SHORT. Against a
// stale 2560-wide read for a 633-wide request it computes -1294, which
// clampViewportDim floors at 1 — a one-pixel-wide browser window. An overshoot
// needs no correction at all: the tab is already at least as big as asked.
func TestSetViewport_ReadBackLargerThanRequestIsNotCompensated(t *testing.T) {
	var boundsCalls []windowBoundsAction
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			boundsCalls = append(boundsCalls, a)
		case layoutMetricsAction:
			*a.w, *a.h = 2560, 1440 // the launch geometry, still in force
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 633, 686, 1)
	require.NoError(t, err)
	require.True(t, applied)

	require.Len(t, boundsCalls, 1,
		"an overshoot must not be 'corrected' — there is nothing to correct")
	require.Equal(t, 633, boundsCalls[0].width)
	require.Equal(t, 686, boundsCalls[0].height)
	for _, b := range boundsCalls {
		require.Greater(t, b.width, 1, "a compensation must never ask for a one-pixel-wide window")
		require.Greater(t, b.height, 1, "a compensation must never ask for a one-pixel-tall window")
	}

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 2560, lv.cssViewportW, "the cache must record the tab as it really is")
	require.Equal(t, 1440, lv.cssViewportH)
}

// Browser.setWindowBounds returns as soon as the browser process accepts the
// bounds; the renderer relays out 40-120ms later (idle) or ~350ms later (busy).
// A single read taken immediately therefore records the PRE-resize size about
// as often as the real one — and then compensates against a phantom shortfall,
// and maps every click through a number that was never true.
func TestSetViewport_ReadBackWaitsForTheTabToCatchUp(t *testing.T) {
	var boundsCalls int
	var reads int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			boundsCalls++
		case layoutMetricsAction:
			reads++
			if reads <= 3 {
				*a.w, *a.h = 1280, 720 // the tab has not relaid out yet
				return nil
			}
			*a.w, *a.h = 615, 744 // settled
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 615, 744, 1)
	require.NoError(t, err)
	require.True(t, applied)

	require.Greater(t, reads, 1, "the read-back must poll, not read once")
	require.Equal(t, 1, boundsCalls,
		"the resize did land — waiting for it must not be mistaken for a shortfall and 'compensated'")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 615, lv.cssViewportW, "the SETTLED read is what gets cached, never the early one")
	require.Equal(t, 744, lv.cssViewportH)
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
	mgr.capture = cs

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
	tabNew, cancelNew := context.WithCancel(context.Background())
	t.Cleanup(cancelNew)

	mgr := &BrowserManager{
		started: true,
		sessions: map[string]*sessionEntry{
			"s1": {
				tabs:      []*tabEntry{{ctx: tabNew, cancel: cancelNew}},
				activeIdx: 0,
			},
		},
	}
	relay := &fakeRelay{}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-reapply", relay, fakeEncoderStarter(new(int32), nil), nil)
	require.NoError(t, err)
	mgr.capture = cs

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
	require.Same(t, tabNew, got.ctxs[0],
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

// Belt-and-braces on the settle poll's contract: a poll that reads successfully
// but never reaches the requested size is NOT a failure — the tab really is
// that size, and recording it is what keeps clicks aimed while the panel
// renders smaller than asked. Only a poll that never read anything at all may
// invalidate.
func TestSettleCSSViewport_RecordsTheTruthWhenItCannotReachTheTarget(t *testing.T) {
	lv := &LiveView{
		sessionID: "s1",
		runCDP: func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				*lm.w, *lm.h = 603, 300
			}
			return nil
		},
	}
	w, h, err := lv.settleCSSViewport(context.Background(), 603, 900)
	require.NoError(t, err, "an unreachable target is not a read failure")
	require.Equal(t, int64(603), w)
	require.Equal(t, int64(300), h)

	lv.runCDP = func(context.Context, time.Duration, ...chromedp.Action) error {
		return fmt.Errorf("transport wedged")
	}
	_, _, err = lv.settleCSSViewport(context.Background(), 603, 900)
	require.Error(t, err, "a poll that never read anything must report failure so the cache is invalidated")
}

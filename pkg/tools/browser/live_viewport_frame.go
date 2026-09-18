package browser

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

type windowContentsSizeAction struct{ width, height int }

func (a windowContentsSizeAction) Do(ctx context.Context) error {
	id, _, err := browser.GetWindowForTarget().Do(ctx)
	if err != nil {
		return err
	}
	return browser.SetContentsSize(id).WithWidth(int64(a.width)).WithHeight(int64(a.height)).Do(ctx)
}

// Inner dimensions include scrollbars; capture's CSS client dimensions do not.
type viewportContentGeometryAction struct{ width, height, clientWidth, clientHeight *int }

func (a viewportContentGeometryAction) Do(ctx context.Context) error {
	var size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := chromedp.Evaluate(`({width:window.innerWidth,height:window.innerHeight})`, &size).Do(ctx); err != nil {
		return err
	}
	w, h, err := readCSSLayoutViewport(ctx)
	if err != nil {
		return err
	}
	*a.width, *a.height = size.Width, size.Height
	*a.clientWidth, *a.clientHeight = int(w), int(h)
	return nil
}

func (lv *LiveView) viewportMatchesRequest(ctx context.Context, measured CaptureFrameState, width, height int) (bool, error) {
	matches := func(w, h int) bool {
		return viewportDeltaPx(width, int64(w)) <= viewportDriftTolerancePx && viewportDeltaPx(height, int64(h)) <= viewportDriftTolerancePx
	}
	if matches(measured.Width, measured.Height) {
		return true, nil
	}
	active, target, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
	if err != nil {
		return false, err
	}
	capture := lv.mgr.CaptureSessionForPanel(lv.sessionID)
	var innerW, innerH, clientW, clientH int
	if runErr := lv.runCDP(ctx, viewportScaleTimeout, viewportContentGeometryAction{&innerW, &innerH, &clientW, &clientH}); runErr != nil {
		return false, runErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return false, contextErr
	}
	current, currentTarget, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
	if err != nil {
		return false, err
	}
	if current != active || currentTarget != target || string(target) != measured.TargetID || lv.mgr.CaptureSessionForPanel(lv.sessionID) != capture {
		return false, fmt.Errorf("browser live: target changed during content measurement")
	}
	// A newer inner size cannot validate an older, undersized capture sample.
	stable := viewportDeltaPx(measured.Width, int64(clientW)) <= viewportDriftTolerancePx && viewportDeltaPx(measured.Height, int64(clientH)) <= viewportDriftTolerancePx
	return stable && matches(innerW, innerH), nil
}

// viewportFrameGeometryAction reads the actual CSS viewport and device scale.
type viewportFrameGeometryAction struct {
	width, height *int
	scale         *float64
}

func (a viewportFrameGeometryAction) Do(ctx context.Context) error {
	w, h, err := readCSSLayoutViewport(ctx)
	if err != nil {
		return fmt.Errorf("layout viewport read: %w", err)
	}
	if err := chromedp.Evaluate("window.devicePixelRatio", a.scale).Do(ctx); err != nil {
		return fmt.Errorf("device pixel ratio read: %w", err)
	}
	*a.width, *a.height = int(w), int(h)
	return nil
}

func (lv *LiveView) measureCaptureFrame(ctx context.Context, cs *CaptureSession) (CaptureFrameState, error) {
	active, target, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
	if err != nil {
		return CaptureFrameState{}, err
	}
	if lv.mgr.CaptureSessionForPanel(lv.sessionID) != cs {
		return CaptureFrameState{}, fmt.Errorf("browser live: capture replaced during viewport update")
	}
	var w, h int
	var scale float64
	if measureErr := lv.runCDP(ctx, viewportScaleTimeout, viewportFrameGeometryAction{&w, &h, &scale}); measureErr != nil {
		return CaptureFrameState{}, measureErr
	}
	if canceledErr := ctx.Err(); canceledErr != nil {
		return CaptureFrameState{}, canceledErr
	}
	current, currentTarget, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
	if err != nil {
		return CaptureFrameState{}, err
	}
	if current != active || currentTarget != target || lv.mgr.CaptureSessionForPanel(lv.sessionID) != cs {
		return CaptureFrameState{}, fmt.Errorf("browser live: target changed during viewport measurement")
	}
	if w <= 0 || h <= 0 || scale < 1 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return CaptureFrameState{}, fmt.Errorf("browser live: invalid measured viewport")
	}
	return CaptureFrameState{TargetID: string(target), Width: w, Height: h, Scale: scale}, nil
}

func sameViewportGeometry(a, b CaptureFrameState) bool {
	return a.TargetID == b.TargetID && a.Width == b.Width && a.Height == b.Height && a.Scale == b.Scale
}

// acceptViewportConvergence fences capture publication while a newly selected
// target's viewport is still pending. An explicit resize may accept Chrome's
// measured clamp, just as it did before target convergence was introduced.
func (lv *LiveView) acceptViewportConvergence(ctx, target context.Context, measured CaptureFrameState, manualResize bool) error {
	lv.mu.Lock()
	if lv.pendingViewportTarget != target {
		lv.mu.Unlock()
		return nil
	}
	width, height := lv.lastRequestedW, lv.lastRequestedH
	lv.mu.Unlock()
	if !manualResize {
		matches, err := lv.viewportMatchesRequest(ctx, measured, width, height)
		if err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("browser live: new tab viewport is still settling")
		}
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.pendingViewportTarget == target && lv.lastRequestedW == width && lv.lastRequestedH == height {
		lv.pendingViewportTarget = nil
	}
	return nil
}

func (lv *LiveView) applyViewportContext(caller, tabCtx context.Context, width, height int, scale float64) (bool, error) {
	return lv.applyViewportContextWithConvergence(caller, tabCtx, width, height, scale, false)
}

// Newly active targets can briefly report their pre-compensation layout after
// Chrome acknowledges window bounds. Correct the content area once before capture;
// ordinary viewer resizes retain their existing measured-clamp behavior.
func (lv *LiveView) applyViewportContextWithConvergence(caller, tabCtx context.Context, width, height int, scale float64, converge bool) (bool, error) {
	var cs *CaptureSession
	var ready CaptureFrameState
	return lv.withViewportAdmission(caller, tabCtx, func(operation context.Context) (bool, error) {
		if lv.mgr != nil {
			cs = lv.mgr.CaptureSessionForPanel(lv.sessionID)
		}
		if cs != nil {
			before := cs.FrameState()
			_, target, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
			if err != nil {
				return false, fmt.Errorf("viewport no-op target lookup: %w", err)
			}
			// A different target cannot reuse the previous picture, even at
			// identical dimensions. Invalidate it through the resize path first.
			if before.TargetID == string(target) && before.Width == width && before.Height == height && before.Scale == scale {
				measured, err := lv.measureCaptureFrame(operation, cs)
				if err != nil {
					return false, fmt.Errorf("viewport initial geometry: %w", err)
				}
				if sameViewportGeometry(before, measured) {
					if err := lv.acceptViewportConvergence(operation, tabCtx, measured, !converge); err != nil {
						return false, fmt.Errorf("viewport cached convergence: %w", err)
					}
					lv.mu.Lock()
					lv.lastRequestedW, lv.lastRequestedH, lv.lastRequestedScale = width, height, scale
					lv.mu.Unlock()
					return true, nil
				}
			}
		}
		anyApplied := false
		measurementRetried := false
		initialLayoutUnverified := false
		for attempt := 0; ; attempt++ {
			if err := viewportContextError(caller, operation); err != nil {
				return anyApplied, err
			}
			if attempt > 0 {
				active, _, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
				if err != nil {
					return anyApplied, err
				}
				if active != tabCtx {
					return anyApplied, fmt.Errorf("browser live: viewport target changed before convergence")
				}
			}
			var applied bool
			var err error
			if attempt == 0 {
				applied, initialLayoutUnverified, err = lv.applyViewportAdmitted(caller, tabCtx, operation, width, height, scale)
			} else {
				// Size content directly: repeating outer bounds can retain Chrome's toolbar deficit.
				err = lv.runCDP(operation, viewportSetTimeout, windowContentsSizeAction{width, height})
				if err == nil {
					_, _, err = lv.settleCSSViewport(operation, width, height)
				}
			}
			anyApplied = anyApplied || applied
			if err != nil {
				stage := "viewport apply"
				if attempt > 0 {
					stage = "viewport contents resize/settle"
				}
				return anyApplied, fmt.Errorf("%s: %w", stage, err)
			}
			if cs == nil {
				return anyApplied, nil
			}
			active, target, identityErr := lv.mgr.activeTargetSnapshot(lv.sessionID)
			if identityErr != nil {
				return anyApplied, fmt.Errorf("viewport final geometry target lookup: %w", identityErr)
			}
			measured, err := lv.measureCaptureFrame(operation, cs)
			if !measurementRetried && errors.Is(err, context.DeadlineExceeded) && viewportContextError(caller, operation) == nil {
				// A stage timeout need not consume the operation's original budget.
				// Retry only the read, and only for the same target and capture.
				current, currentTarget, snapshotErr := lv.mgr.activeTargetSnapshot(lv.sessionID)
				if snapshotErr != nil || current != active || currentTarget != target || lv.mgr.CaptureSessionForPanel(lv.sessionID) != cs {
					return anyApplied, fmt.Errorf("viewport final geometry: identity changed after timed-out read: %w", err)
				}
				measurementRetried = true
				measured, err = lv.measureCaptureFrame(operation, cs)
			}
			if err != nil {
				return anyApplied, fmt.Errorf("viewport final geometry: %w", err)
			}
			// The initial read can fail before toolbar compensation, even when the
			// final read succeeds immediately. Correct that shortfall once; fresh
			// inner/CSS geometry distinguishes it from a normal scrollbar.
			recoverShortfall := attempt == 0 && initialLayoutUnverified &&
				(width-measured.Width > viewportDriftTolerancePx || height-measured.Height > viewportDriftTolerancePx)
			// A VERIFIED read LARGER than the request means the window-bounds
			// shrink was ignored outright, not overshot benignly: the CI worker
			// measured 2560x1297 (--window-size=2560,1440 minus chrome) against
			// a requested 561x628 on 2026-09-18, and every later click mis-aimed
			// because the capture kept depicting the un-reshaped tab. The
			// "overshoot needs no correction" rule in the mechanism comment
			// covers a window that legitimately ends bigger than asked; a shrink
			// request that moved nothing is that rule's blind spot. Give it the
			// same single content-size retry the new-tab convergence path has
			// (SetContentsSize — the one resize lever chrome.tabs.get, and so
			// the capture, actually reflects), then accept whatever settles,
			// exactly as the shortfall path does.
			recoverOvershoot := attempt == 0 && !initialLayoutUnverified &&
				(measured.Width-width > viewportDriftTolerancePx || measured.Height-height > viewportDriftTolerancePx)
			matches := true
			if converge || recoverShortfall || recoverOvershoot {
				matches, err = lv.viewportMatchesRequest(operation, measured, width, height)
				if err != nil {
					return anyApplied, fmt.Errorf("viewport convergence measurement: %w", err)
				}
			}
			if !matches {
				if attempt == 0 {
					continue
				}
				return anyApplied, fmt.Errorf("browser live: new tab viewport did not converge: requested %dx%d, measured %dx%d", width, height, measured.Width, measured.Height)
			}
			if convergenceErr := lv.acceptViewportConvergence(operation, tabCtx, measured, !converge); convergenceErr != nil {
				return anyApplied, fmt.Errorf("viewport convergence acceptance: %w", convergenceErr)
			}
			ready, err = cs.BeginFrameTransition(measured.TargetID, measured.Width, measured.Height, measured.Scale)
			if err != nil {
				return anyApplied, fmt.Errorf("viewport frame publication: %w", err)
			}
			return anyApplied, nil
		}
	}, func(operation context.Context) error {
		if cs != nil && ready.Generation != 0 && ready.Width > 0 && ready.Height > 0 && !cs.RecaptureFrameContext(operation, ready) {
			current := cs.FrameState()
			if current.CaptureID == ready.CaptureID && current.TargetID == ready.TargetID && current.Width == 0 && current.Height == 0 {
				return nil // A newer document owns completion of the measured resize.
			}
			return fmt.Errorf("browser live: measured recapture was not admitted")
		}
		return nil
	})
}

// RefreshCaptureFrameContext measures and refreshes only the original panel's
// capture. It sends after releasing browser admission and never guesses geometry.
func (r *LiveViewRegistry) RefreshCaptureFrameContext(caller context.Context, sessionID string, expected *CaptureSession) error {
	if err := caller.Err(); err != nil {
		return err
	}
	lv, ok := r.lookup(sessionID)
	if !ok || expected == nil || lv.mgr == nil {
		return fmt.Errorf("browser live: capture panel unavailable")
	}
	tabCtx, _, err := lv.mgr.activeTargetSnapshot(sessionID)
	if err != nil {
		return err
	}
	var ready CaptureFrameState
	_, err = lv.withViewportAdmission(caller, tabCtx, func(operation context.Context) (bool, error) {
		if lv.mgr.CaptureSessionForPanel(sessionID) != expected {
			return false, fmt.Errorf("browser live: original panel capture replaced")
		}
		_, target, snapshotErr := lv.mgr.activeTargetSnapshot(sessionID)
		if snapshotErr != nil {
			return false, snapshotErr
		}
		before := expected.FrameState()
		if before.TargetID != string(target) {
			if _, transitionErr := expected.BeginFrameTransition(string(target), 0, 0, before.Scale); transitionErr != nil {
				return false, transitionErr
			}
		}
		measured, snapshotErr := lv.measureCaptureFrame(operation, expected)
		if snapshotErr != nil {
			return false, snapshotErr
		}
		if convergenceErr := lv.acceptViewportConvergence(operation, tabCtx, measured, false); convergenceErr != nil {
			return false, convergenceErr
		}
		if sameViewportGeometry(before, measured) {
			return true, nil
		}
		ready, snapshotErr = expected.BeginFrameTransition(measured.TargetID, measured.Width, measured.Height, measured.Scale)
		return true, snapshotErr
	}, func(operation context.Context) error {
		if ready.Generation != 0 && ready.Width > 0 && ready.Height > 0 && !expected.RecaptureFrameContext(operation, ready) {
			current := expected.FrameState()
			if current.CaptureID == ready.CaptureID && current.TargetID == ready.TargetID && current.Width == 0 && current.Height == 0 {
				return nil
			}
			return fmt.Errorf("browser live: measured refresh was not admitted")
		}
		return nil
	})
	return err
}

// --- moved from live.go 2026-09-15 ---

// applyViewport resizes the tab reachable through tabCtx to width x height CSS
// pixels and renders it at deviceScaleFactor, so the capture's shape and
// resolution follow the viewer's panel instead of a fixed constant.
//
// The tab context is a PARAMETER rather than a read of lv.tabCtx because the
// active tab can change under us: onTabsChanged must resize and re-scale the
// tab the user just switched TO, and it knows that context before lv.tabCtx is
// necessarily rebound (rebindWatch is skipped outright when no viewer is
// attached). Passing it in removes the window where a re-apply would land on
// the tab the user just left.
//
// Why (operator UAT 2026-07-31): the tab was pinned to a hardcoded
// --window-size=1280,720 (exec_resolver.go) while the docked panel is an
// arbitrary, resizable shape — measured ~890x1010 (portrait). Since
// `object-fit: contain` preserves the SOURCE aspect, the page could only ever
// fill one dimension and the rest of the panel was letterboxed black. No CSS
// change can correct a source whose shape is wrong. The same report's second
// half was blur: the managed headless Chrome renders at DPR 1, so a capture
// displayed larger than its CSS size upscales — deviceScaleFactor fixes that.
//
// Mechanism (root-caused via live measurement, not hypothetical, see
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md Fault 1): this
// USED TO call only Emulation.setDeviceMetricsOverride(width, height, dsf,
// false). That override is real inside the CDP/renderer world — the page's own
// CSS media queries and layout genuinely see the new size — but it is NOT
// reflected in what the extension-side capture reads: encoder.js's
// captureActiveTabStream sizes the tabCapture stream from
// chrome.tabs.get(tabId).width/height, which is the tab's real OS window size
// and stays put regardless of the emulation override. Every layer logged
// success while the captured stream never reshaped — confirmed live: stream
// aspect stuck at 2.02 against a 0.96 panel. Textbook silent failure.
//
// Fixed by driving the actual OS-level browser window via
// Browser.getWindowForTarget + Browser.setWindowBounds, which DOES change what
// chrome.tabs.get() reports, so the extension's capture follows.
// Emulation.setDeviceMetricsOverride is kept, but ONLY for deviceScaleFactor —
// passing width/height 0 to it means "no size override" to CDP, so it can
// never fight the window-bounds resize. When deviceScaleFactor <= 1 this
// clears the override outright instead of setting a redundant no-op one, so a
// viewer moving from a 2x display back to a 1x one doesn't leave Chromium
// rendering at the old scale.
//
// The two are issued as SEPARATE, independently budgeted CDP calls, and their
// failure modes are deliberately different — see viewportScaleTimeout's doc
// comment for the measurement behind that (in one bundle, a renderer-bound
// scale override made a resize that had already succeeded report failure to
// the user). The window resize is the real operation: it keeps
// viewportSetTimeout and its single deadline-only retry, and a failure there
// is returned as an error. The scale override is cosmetic: it gets its own
// budget and, on failure, a warning — the sequence continues to the read-back
// either way, because the tab HAS been resized and the cache must learn its
// new size regardless of how sharply it happens to be rasterised.
//
// This ONLY changes the tab. A capture already in flight keeps its old
// geometry, because tabCapture constraints are pinned per stream (encoder.js's
// minWidth/maxWidth) and cannot be renegotiated on a running track. The caller
// must follow this with CaptureSession.Recapture()/RecaptureAt() — see
// pkg/gateway/browser_ws.go's handleViewport for the ordering.
//
// After applying, this reads back the tab's ACTUAL CSS layout viewport via
// Page.getLayoutMetrics — the only thing that can prove the resize really took
// effect, per the root-cause doc's "Exit proof" section — and caches it on the
// LiveView (cssViewportW/H, guarded by lv.mu). That cache is the source of
// truth dispatchInput's rescaleToCSSViewport uses to map a viewer's
// capture-space input coordinates into CSS pixels (Fault 3). The read-back is a
// settle POLL, not a single read: setWindowBounds returns before the renderer
// has relaid out, so an immediate read frequently reports the PRE-resize size
// (see viewportSettleBudget). A poll that never converges is not an error — the
// tab really is that size and recording it is what keeps clicks aimed — but a
// poll that never manages to READ anything invalidates the cache rather than
// leaving a value nothing confirmed.
//
// Chrome-delta compensation — SINGLE PASS, LOAD-BEARING, DO NOT DELETE.
// Browser.setWindowBounds sizes the OUTER OS-level window; the tab's own CSS
// layout viewport is that minus Chrome's window chrome (tab strip, toolbar),
// which the window-bounds call has no way to account for up front. Measured
// 2026-08-16 against headless Chrome 152: the deficit is exactly 143px of
// HEIGHT, width exact, and CONSTANT across sizes and across deviceScaleFactor
// 1 and 2 (outer 680 -> css 537, 750 -> 607, 686 -> 543, 1400 -> 1257). A
// constant offset is precisely what one correction converges on, and it does:
// 14 of 14 faithful replays landed on the requested size after a single
// re-apply of (request + observed shortfall).
//
// An older version of this comment claimed the compensation "frequently does
// not work" and invited its removal, on v52 logs where a second setWindowBounds
// changed nothing. That reading was wrong, and acting on it would have deleted
// the only reason the panel is ever the size the user asked for. Those logs
// show a DIFFERENT failure — a window Chrome would not grow at all, so neither
// the first nor the second bounds call moved anything — and "the resize was
// refused twice" is not evidence against compensating for a chrome delta when
// the resize IS honoured. What remains true is that ITERATING does not help:
// when a re-apply moves nothing, repeating it moves nothing N times. So this
// compensates exactly once and then accepts whatever the tab reports.
//
// Compensation only ever corrects a SHORTFALL. `width + (width - actual)`
// silently assumed actual < requested; against a read-back LARGER than the
// request (633 requested against a stale 2560 cached read) it computed -1294,
// clamped to a ONE PIXEL WIDE window — the "resolution collapse" failure class.
// An overshoot needs no correction (the tab is already at least as big as
// asked), so it now gets none.
//
// A requested/actual gap over viewportDriftTolerancePx in either dimension
// (after any compensation attempt) is logged at WARN, explicitly saying the
// window resize was not fully reflected — this is what would have caught
// Fault 1 instead of every layer silently reporting success. A partial resize
// still returns applied=true; it is not treated as a failure, only flagged.
//
// Returns true once the initial bounds are acknowledged. A later caller
// cancellation is returned alongside that observed effect.
func (lv *LiveView) applyViewport(tabCtx context.Context, width, height int, deviceScaleFactor float64) (bool, error) {
	return lv.applyViewportContext(context.Background(), tabCtx, width, height, deviceScaleFactor)
}

// viewportSetTimeout bounds the Browser.setWindowBounds round trip in
// applyViewport. Kept short: a resize arrives on the UI's debounce and a slow
// or wedged tab must not stall the WS reader goroutine that dispatched it.
const viewportSetTimeout = 5 * time.Second

// viewportScaleTimeout bounds the Emulation.set/clearDeviceMetricsOverride
// round trip, which applyViewport now issues as its OWN call rather than
// bundling it with the window resize under one budget.
//
// Why they are separate (measured 2026-08-15 against headless Chrome 152 with
// the renderer deliberately blocked for 7s):
//
//	Browser.getWindowForTarget            53ms
//	Browser.setWindowBounds               75ms   <- the resize ALREADY happened
//	Emulation.setDeviceMetricsOverride  6825ms   <- renderer-bound, waits for it
//
// Bundled, that is 6.95s against one 5s budget: DeadlineExceeded, one retry,
// up to 10s of stalling, and the operator's "could not resize the browser
// viewport" toast — for a resize that had SUCCEEDED 6.9 seconds earlier. Worse,
// applyViewport returned before the read-back, so cssViewportW/H kept
// describing the PRE-resize tab and mis-aimed every subsequent click.
//
// setWindowBounds is answered by the browser process; setDeviceMetricsOverride
// is answered by the renderer, so a busy page delays only the latter. The
// scale override is COSMETIC — it changes how sharply the tab is rasterised,
// nothing about its layout or its size — so it gets its own budget and, when
// that budget expires, a warning and a soft picture, never a failed resize.
const viewportScaleTimeout = 5 * time.Second

// viewportDriftTolerancePx is the acceptable gap between SetViewport's
// requested width/height and the tab's actual read-back CSS viewport before
// treating the resize as imperfectly reflected. Below this, a small
// scrollbar/AA-width discrepancy is normal noise; above it, something real
// diverged — confirmed live (UAT v24, 2026-07-31): a requested 615x744
// landed at an actual CSS viewport of 615x657, an 87px HEIGHT deficit from
// Chrome's own window chrome (tab strip/toolbar), width matching exactly.
// See the compensation step in SetViewport's mechanism doc comment.
const viewportDriftTolerancePx = 8

// Viewport bounds. Per-field limits mirror BrowserViewportFrame's schema so
// SetViewport is safe even when gateway.validate_inbound is off (it defaults
// to false, making this the ONLY check on a default install).
const (
	maxViewportDimension   = 8192
	maxViewportScaleFactor = 3.0
	// maxViewportPhysicalPixels bounds width*height*dsf^2 — the actual
	// framebuffer Chromium must allocate. ~33.2M is a generous 8K-class
	// surface (7680x4320 = 33.2M) while refusing the ~604M that the per-field
	// maxima alone would permit.
	maxViewportPhysicalPixels = 33_200_000.0
)

// windowBoundsAction folds Browser.getWindowForTarget + Browser.setWindowBounds
// into ONE chromedp.Action (test-seam review MEDIUM finding): the windowID no
// longer needs to live in a variable shared across two chromedp.Tasks slice
// entries — chromedp.Tasks runs its actions in order and aborts on the first
// error, so a two-entry [get, set] slice with an outer `var windowID
// browser.WindowID` behaved identically to resolving and using it entirely
// inside one action. Implemented as a small named type — rather than a bare
// chromedp.ActionFunc closure — specifically so SetViewport's compensation
// step is testable: live_test.go type-asserts the *requested* width/height
// straight off this struct's exported-to-the-package fields, without ever
// having to execute a real CDP round trip to observe them (a closure hides
// that same information behind an opaque func value with no way to inspect
// it short of actually calling Do(ctx) against a live browser). SetViewport
// constructs this at most twice: once for the requested size, and — only
// when the chrome-delta compensation step fires (see SetViewport's mechanism
// doc comment) — once more for the compensated size.
type windowBoundsAction struct {
	width, height int
}

// liveViewApplyViewportAdmitted carries the shared state of applyViewportAdmitted across its stages.
type liveViewApplyViewportAdmitted struct {
	lv                *LiveView
	caller            context.Context
	tabCtx            context.Context
	operationCtx      context.Context
	width             int
	height            int
	deviceScaleFactor float64
	run               func(timeout time.Duration, actions ...chromedp.Action) error
	startActiveCtx    context.Context
	scaleApplied      bool
	actualW           int64
	actualH           int64
	compensated       bool
	compensatedAskW   int
	compensatedAskH   int
	ret0              bool
	ret1              bool
	ret2              error
}

// liveViewApplyViewportAdmittedFlow reports how a block stage of liveViewApplyViewportAdmitted wants the conductor to proceed.
type liveViewApplyViewportAdmittedFlow int

const (
	liveViewApplyViewportAdmittedNext liveViewApplyViewportAdmittedFlow = iota
	liveViewApplyViewportAdmittedReturn
	liveViewApplyViewportAdmittedContinue
	liveViewApplyViewportAdmittedBreak
)

// applyViewportAdmitted keeps the original target identity for cache validation;
// only operationCtx is passed to browser work so caller cancellation reaches it.
// initialLayoutUnverified reports that the initial read failed before compensation.
func (lv *LiveView) applyViewportAdmitted(caller, tabCtx, operationCtx context.Context, width, height int, deviceScaleFactor float64) (applied bool, initialLayoutUnverified bool, err error) {
	av := &liveViewApplyViewportAdmitted{lv: lv, caller: caller, tabCtx: tabCtx, operationCtx: operationCtx, width: width, height: height, deviceScaleFactor: deviceScaleFactor}

	if av.tabCtx == nil {
		return false, false, nil
	}
	// Serialize the whole apply→compensate→settle→cache sequence per LiveView
	// (live UAT 2026-07-31, pop-out): two viewers may legally send viewport
	// frames near-simultaneously while the tab is uncontrolled (the docked
	// panel's first-frame re-send racing the pop-out's attach frame).
	// Interleaved, one caller's raw bounds-write lands in the middle of the
	// other's compensation and the window ends at a hybrid neither asked for
	// (measured: outer bounds stuck at the pop-out's UNcompensated first apply,
	// tab pinned 86px short, self-heal correctly seeing "no drift" against a
	// genuinely wrong tab). NOT lv.mu — this holds across several CDP round
	// trips, and lv.mu must never be held across a CDP call (ADR-038
	// discipline).
	av.lv.viewportMu.Lock()
	defer av.lv.viewportMu.Unlock()
	// The input gate excludes all other viewport applies before this mutex.
	// Keep browser executor values from tabCtx while limiting each stage to
	// both its existing timeout and the caller's remaining lifetime.
	av.run = func(timeout time.Duration, actions ...chromedp.Action) error {
		if err := viewportContextError(av.caller, av.operationCtx); err != nil {
			return err
		}
		return av.lv.runCDP(av.operationCtx, timeout, actions...)
	}
	// Bounds are also enforced by the wire schema (BrowserViewportFrame), but
	// re-checked here because this is reachable from a public registry method
	// and a future non-WS caller must not be able to hand Chromium a degenerate
	// or enormous allocation.

	switch av.validateAndRemember() {
	case liveViewApplyViewportAdmittedReturn:
		return av.ret0, av.ret1, av.ret2
	}

	// Step 1: reshape the OS-level browser window (Fault 1 fix — see the
	// mechanism section above). windowBoundsAction folds
	// Browser.getWindowForTarget (which resolves the current tab's own window
	// with no explicit target ID, because it is called "as a part of the
	// session" — tabCtx IS that session) and Browser.setWindowBounds into one
	// chromedp.Action. Routed through lv.runCDP, not the package-level
	// runCDPWithTimeout, like every other CDP call site in this file.

	switch av.resizeWindow() {
	case liveViewApplyViewportAdmittedReturn:
		return av.ret0, av.ret1, av.ret2
	}

	// Step 2: deviceScaleFactor only, on its OWN budget. A stage failure is
	// cosmetic while the caller remains active — the
	// window above is already the size the user asked for, and refusing that
	// because the renderer was slow to answer a sharpness request is the exact
	// bug viewportScaleTimeout's doc comment documents. dsf==1 clears any stale
	// override rather than setting a no-op one.

	switch av.applyScale() {
	case liveViewApplyViewportAdmittedReturn:
		return av.ret0, av.ret1, av.ret2
	}

	// Step 3: settle-poll the tab's ACTUAL CSS layout viewport (see the
	// mechanism section, and settleCSSViewport's own doc comment).

	switch av.measureInitialLayout() {
	case liveViewApplyViewportAdmittedReturn:
		return av.ret0, av.ret1, av.ret2
	}

	// Chrome-delta compensation, shortfall only, single pass — see the
	// mechanism section above for the measurement and for why this must not be
	// deleted, iterated, or allowed to run on an overshoot.

	switch av.compensateLayout() {
	case liveViewApplyViewportAdmittedReturn:
		return av.ret0, av.ret1, av.ret2
	}

	av.logOutcome()

	// The FINAL settled read is always sane/non-degenerate by this point — both
	// failure paths above already returned early via invalidateCSSViewportCache.
	return av.cacheMeasurement()
}

// validateAndRemember validates the requested viewport and records it with the active target at admission.
func (av *liveViewApplyViewportAdmitted) validateAndRemember() liveViewApplyViewportAdmittedFlow {
	if av.width < 1 || av.height < 1 || av.width > maxViewportDimension || av.height > maxViewportDimension {
		av.ret0 = false
		av.ret1 = false
		av.ret2 = fmt.Errorf("browser live: viewport %dx%d out of range", av.width, av.height)
		return liveViewApplyViewportAdmittedReturn
	}
	if av.deviceScaleFactor < 1 || av.deviceScaleFactor > maxViewportScaleFactor {
		// Reject rather than silently clamp (review finding): a caller asking
		// for dsf 50 got no feedback at all under the old clamp, while an
		// out-of-range width got an explicit error. Same input class, same
		// treatment.
		av.ret0 = false
		av.ret1 = false
		av.ret2 = fmt.Errorf("browser live: device scale factor %.2f out of range (1..%.0f)",
			av.deviceScaleFactor, maxViewportScaleFactor)
		return liveViewApplyViewportAdmittedReturn
	}
	// Combined ceiling. Each dimension and the scale factor are individually
	// bounded above, but nothing bounded their PRODUCT: 8192x8192 at dsf 3 is
	// inside every per-field limit and asks Chromium for a ~24576x24576
	// physical surface — on the order of gigabytes of framebuffer, against the
	// single shared Chrome backing the agent's browsing.
	physicalPixels := float64(av.width) * float64(av.height) * av.deviceScaleFactor * av.deviceScaleFactor
	if physicalPixels > maxViewportPhysicalPixels {
		av.ret0 = false
		av.ret1 = false
		av.ret2 = fmt.Errorf(
			"browser live: viewport %dx%d @%.1fx = %.0f physical pixels, over the %.0f ceiling",
			av.width, av.height, av.deviceScaleFactor, physicalPixels, maxViewportPhysicalPixels)
		return liveViewApplyViewportAdmittedReturn
	}

	// Remember what was asked for, BEFORE any CDP call. onTabsChanged replays
	// exactly this on the tab the user switches to: the deviceScaleFactor
	// override is PER TARGET (measured 2026-08-16 — tab A reports DPR 2 while a
	// tab opened afterwards in the same window reports 1 with identical
	// innerWidth/innerHeight), so without a replay every newly-opened tab
	// renders at 1x while the encoder is still capturing at 2x, which is blur
	// on every single tab open.
	av.lv.mu.Lock()
	av.lv.lastRequestedW, av.lv.lastRequestedH = av.width, av.height
	av.lv.lastRequestedScale = av.deviceScaleFactor
	// The active tab as it stood when this apply STARTED — half of the
	// stale-write guard on the cache write at the very end (see
	// viewportMeasurementIsStaleLocked). Everything between here and there is
	// several CDP round trips plus a settle poll, and the user can switch tabs
	// throughout.
	av.startActiveCtx = av.lv.lastKnownActiveCtx
	av.lv.mu.Unlock()
	return liveViewApplyViewportAdmittedNext
}

// resizeWindow starts the frame transition and resizes the browser window, retrying a transient timeout once.
func (av *liveViewApplyViewportAdmitted) resizeWindow() liveViewApplyViewportAdmittedFlow {
	if av.lv.mgr != nil {
		if cs := av.lv.mgr.CaptureSessionForPanel(av.lv.sessionID); cs != nil {
			_, target, err := av.lv.mgr.activeTargetSnapshot(av.lv.sessionID)
			if err != nil {
				av.ret0 = false
				av.ret1 = false
				av.ret2 = err
				return liveViewApplyViewportAdmittedReturn
			}
			if _, err := cs.BeginFrameTransition(string(target), 0, 0, av.deviceScaleFactor); err != nil {
				av.ret0 = false
				av.ret1 = false
				av.ret2 = err
				return liveViewApplyViewportAdmittedReturn
			}
		}
	}
	boundsAction := windowBoundsAction{width: av.width, height: av.height}
	if err := av.run(viewportSetTimeout, boundsAction); err != nil {
		// One retry, and ONLY for a deadline timeout (2026-08-13 UAT: "could
		// not resize the browser viewport" toast mid-session). A
		// GetWindowForTarget that cannot answer within viewportSetTimeout means
		// the browser process is momentarily starved (encode burst + input
		// backlog), not that the resize is invalid — by the second attempt the
		// stall has typically cleared. Any other error is a real failure and
		// still surfaces immediately.
		if viewportContextError(av.caller, av.operationCtx) != nil || !errors.Is(err, context.DeadlineExceeded) {
			av.ret0 = false
			av.ret1 = false
			av.ret2 = fmt.Errorf("browser live: resize viewport: %w", err)
			return liveViewApplyViewportAdmittedReturn
		}
		logger.WarnCF(
			"browser",
			"live view: set viewport timed out; retrying once (browser process momentarily starved)",
			map[string]any{"session_id": av.lv.sessionID},
		)
		if err := av.run(viewportSetTimeout, boundsAction); err != nil {
			av.ret0 = false
			av.ret1 = false
			av.ret2 = fmt.Errorf("browser live: resize viewport (after retry): %w", err)
			return liveViewApplyViewportAdmittedReturn
		}
	}

	if err := viewportContextError(av.caller, av.operationCtx); err != nil {
		av.ret0 = true
		av.ret1 = false
		av.ret2 = err
		return liveViewApplyViewportAdmittedReturn
	}
	return liveViewApplyViewportAdmittedNext
}

// applyScale applies the requested device scale factor and reports a non-fatal degradation.
func (av *liveViewApplyViewportAdmitted) applyScale() liveViewApplyViewportAdmittedFlow {
	var scaleAction chromedp.Action = emulation.ClearDeviceMetricsOverride()
	if av.deviceScaleFactor > 1 {
		scaleAction = emulation.SetDeviceMetricsOverride(0, 0, av.deviceScaleFactor, false)
	}
	av.scaleApplied = true
	if err := av.run(viewportScaleTimeout, scaleAction); err != nil {
		if ended := viewportContextError(av.caller, av.operationCtx); ended != nil {
			av.ret0 = true
			av.ret1 = false
			av.ret2 = ended
			return liveViewApplyViewportAdmittedReturn
		}
		av.scaleApplied = false
		logger.WarnCF(
			"browser",
			"live view: the browser window was resized successfully, but the display-sharpness setting did not take — the picture may look soft until the next resize",
			map[string]any{
				"error":               err.Error(),
				"session_id":          av.lv.sessionID,
				"requested_width":     av.width,
				"requested_height":    av.height,
				"device_scale_factor": av.deviceScaleFactor,
			},
		)
		// ...and TELL THE PERSON WATCHING (round-2 finding F5, ADR-061
		// discipline: a failure must name its cause to the user, not only in
		// a log). The line above is a WARN in a gateway whose production log
		// level is WARN-only for an operator who is not reading it live, and
		// invisible to the viewer entirely. This call is renderer-bound, so
		// it only ever times out on a loaded box — which means the hosted
		// Linux user got a persistently soft picture with no message, no
		// control and no stated recovery, while the macOS user never saw the
		// branch at all. Same behaviour, same message, same recovery on both
		// is the point.
		av.lv.notifyScaleDegraded()
	} else {
		// Re-arm the notice: the next degradation after a recovery is a new
		// event and must be reported, not swallowed by the previous one's
		// throttle window.
		av.lv.clearScaleDegraded()
	}

	if err := viewportContextError(av.caller, av.operationCtx); err != nil {
		av.ret0 = true
		av.ret1 = false
		av.ret2 = err
		return liveViewApplyViewportAdmittedReturn
	}
	return liveViewApplyViewportAdmittedNext
}

// measureInitialLayout settles and validates the initial CSS viewport measurement.
func (av *liveViewApplyViewportAdmitted) measureInitialLayout() liveViewApplyViewportAdmittedFlow {
	var readErr error
	av.actualW, av.actualH, readErr = av.lv.settleCSSViewport(av.operationCtx, av.width, av.height)
	if err := viewportContextError(av.caller, av.operationCtx); err != nil {
		av.ret0 = true
		av.ret1 = false
		av.ret2 = err
		return liveViewApplyViewportAdmittedReturn
	}
	if readErr != nil {
		// A failed read-back does not undo the resize above (best-effort: the
		// resize itself already succeeded), so this is logged and swallowed
		// rather than turned into an error return — but it DOES invalidate the
		// cache rather than leaving a stale value in place; see
		// invalidateCSSViewportCache's doc comment for why that matters.
		av.lv.invalidateCSSViewportCache()
		logger.WarnCF(
			"browser",
			"live view: set viewport applied but could not read back the actual CSS viewport to verify it — cache invalidated, input coordinates will re-fetch it on the next event",
			map[string]any{
				"error":            readErr.Error(),
				"session_id":       av.lv.sessionID,
				"requested_width":  av.width,
				"requested_height": av.height,
			},
		)
		av.ret0 = true
		av.ret1 = true
		av.ret2 = nil
		return liveViewApplyViewportAdmittedReturn
	}
	return liveViewApplyViewportAdmittedNext
}

// compensateLayout performs one shortfall compensation pass and records its settled measurement.
func (av *liveViewApplyViewportAdmitted) compensateLayout() liveViewApplyViewportAdmittedFlow {
	av.compensated = false

	shortW := av.width - int(av.actualW)
	shortH := av.height - int(av.actualH)
	if shortW > viewportDriftTolerancePx || shortH > viewportDriftTolerancePx {
		compW := clampViewportDim(av.width + max(shortW, 0))
		compH := clampViewportDim(av.height + max(shortH, 0))
		av.compensatedAskW, av.compensatedAskH = compW, compH
		if err := av.run(viewportSetTimeout, windowBoundsAction{width: compW, height: compH}); err != nil {
			if ended := viewportContextError(av.caller, av.operationCtx); ended != nil {
				av.ret0 = true
				av.ret1 = false
				av.ret2 = ended
				return liveViewApplyViewportAdmittedReturn
			}
			logger.WarnCF(
				"browser",
				"live view: set viewport — chrome-delta compensation re-apply failed, keeping the pre-compensation read-back",
				map[string]any{
					"error":              err.Error(),
					"session_id":         av.lv.sessionID,
					"compensated_width":  compW,
					"compensated_height": compH,
				},
			)
		} else {
			if err := viewportContextError(av.caller, av.operationCtx); err != nil {
				av.ret0 = true
				av.ret1 = false
				av.ret2 = err
				return liveViewApplyViewportAdmittedReturn
			}
			compW2, compH2, compErr := av.lv.settleCSSViewport(av.operationCtx, av.width, av.height)
			if err := viewportContextError(av.caller, av.operationCtx); err != nil {
				av.ret0 = true
				av.ret1 = false
				av.ret2 = err
				return liveViewApplyViewportAdmittedReturn
			}
			if compErr != nil {
				av.lv.invalidateCSSViewportCache()
				logger.WarnCF("browser",
					"live view: set viewport — could not read back the CSS viewport after "+
						"chrome-delta compensation — cache invalidated, input coordinates will "+
						"re-fetch it on the next event",
					map[string]any{
						"error":              compErr.Error(),
						"session_id":         av.lv.sessionID,
						"requested_width":    av.width,
						"requested_height":   av.height,
						"compensated_width":  compW,
						"compensated_height": compH,
					})
				av.ret0 = true
				av.ret1 = false
				av.ret2 = nil
				return liveViewApplyViewportAdmittedReturn
			}
			// The settled post-compensation read is authoritative, full stop.
			// This used to "keep the closest" of the two read-backs, a
			// heuristic that only existed because a read-ONCE read-back could
			// not tell a settled measurement from one taken mid-reflow: keeping
			// the closer number was a way of guessing which read had been
			// taken too early. The settle poll answers that question directly,
			// so the guess is gone — a compensated tab that legitimately ends
			// up further from the request than it started (it can, e.g. a
			// window clamped at the screen edge) must be recorded as it IS, not
			// replaced by a stale earlier number that flatters the request.
			av.actualW, av.actualH = compW2, compH2
			av.compensated = true
		}
	}

	if err := viewportContextError(av.caller, av.operationCtx); err != nil {
		av.ret0 = true
		av.ret1 = false
		av.ret2 = err
		return liveViewApplyViewportAdmittedReturn
	}
	return liveViewApplyViewportAdmittedNext
}

// logOutcome logs whether the final measured viewport matches the request.
func (av *liveViewApplyViewportAdmitted) logOutcome() {
	fields := map[string]any{
		"session_id":          av.lv.sessionID,
		"requested_width":     av.width,
		"requested_height":    av.height,
		"actual_width":        av.actualW,
		"actual_height":       av.actualH,
		"device_scale_factor": av.deviceScaleFactor,
		"scale_applied":       av.scaleApplied,
		"compensated":         av.compensated,
		// What compensation actually asked Chrome for (0 when it never ran).
		// Present so a recurrence of the DSF-2 shrink is diagnosable from the
		// log alone — reconstructing it by hand produced two wrong models.
		"compensated_ask_width":  av.compensatedAskW,
		"compensated_ask_height": av.compensatedAskH,
	}
	if viewportDeltaPx(av.width, av.actualW) > viewportDriftTolerancePx ||
		viewportDeltaPx(av.height, av.actualH) > viewportDriftTolerancePx {
		// The silent-success failure mode the root-cause doc documents: every
		// prior layer reported success while the capture never actually
		// reshaped. Loud enough here that it can't be missed the way it was
		// during the 2026-07-31 UAT.
		logger.WarnCF(
			"browser",
			"live view: set viewport — window resize not fully reflected in the tab's CSS viewport",
			fields,
		)
	} else {
		logger.InfoCF("browser", "live view: viewport applied", fields)
	}
}

// cacheMeasurement caches the final measurement unless the active target changed during the apply.
func (av *liveViewApplyViewportAdmitted) cacheMeasurement() (bool, bool, error) {
	av.lv.mu.Lock()
	if av.lv.viewportMeasurementIsStaleLocked(av.tabCtx, av.startActiveCtx) {
		// The measurement is real, but it describes a tab that is no longer
		// the one being watched and clicked. Writing it would be WORSE than
		// writing nothing: a positive value passes rescaleToCSSViewport's
		// cache-hit guard, so every subsequent click would be mapped through
		// the geometry of a tab the user has already left — silently, and with
		// no way for anything downstream to notice. Zeroing instead makes the
		// next input event re-fetch from the tab that is actually live.
		av.lv.cssViewportW, av.lv.cssViewportH = 0, 0
		av.lv.cssViewportScale = 0
		av.lv.mu.Unlock()
		logger.WarnCF(
			"browser",
			"live view: the active tab changed while its viewport was being applied — "+
				"discarding the measurement rather than caching another tab's geometry",
			map[string]any{
				"session_id":       av.lv.sessionID,
				"requested_width":  av.width,
				"requested_height": av.height,
				"actual_width":     av.actualW,
				"actual_height":    av.actualH,
			},
		)
		return true, false, nil
	}
	av.lv.cssViewportW = int(av.actualW)
	av.lv.cssViewportH = int(av.actualH)
	if av.scaleApplied {
		av.lv.cssViewportScale = av.deviceScaleFactor
		// lastAppliedScale survives cache invalidation on purpose: the CDP
		// override stays in force on the target until something clears it, so
		// a later cache refill (rescaleToCSSViewport's cache-miss fetch, which
		// can read the layout viewport but has no way to measure the scale)
		// can restore it instead of leaving the scale at zero forever.
		av.lv.lastAppliedScale = av.deviceScaleFactor
	} else {
		// The override did not land, so the tab is rendering at some scale we
		// did not choose and cannot name. Recording the requested one would be
		// a confident lie to viewportBasisForCapture; zero means "unknown",
		// which is what it actually is.
		av.lv.cssViewportScale = 0
	}
	av.lv.mu.Unlock()

	return true, false, nil
}

// viewportMeasurementIsStaleLocked reports whether a viewport measurement
// taken against tabCtx — with startActiveCtx being the active tab at the
// moment that apply began — must NOT be written to the CSS-viewport cache,
// because it no longer describes the tab the viewer is watching.
//
// Round-2 finding F1, the half that survives even after the re-apply itself
// coalesces: switching A -> B -> C leaves B's multi-round-trip apply still
// running while C is active, and B's apply then wrote B's geometry into the
// cache unconditionally. Every click after that was mapped through B's
// dimensions on C's page — the worst shape of this defect class, because a
// positive cache entry looks healthy to every guard downstream.
//
// Two independent ways to be stale, both needed:
//
//   - the active tab MOVED during the apply (cur != startActiveCtx) — the
//     A -> B -> C case above;
//   - this apply was aimed at a tab that is not the active one (cur !=
//     tabCtx) — the same race won a few microseconds earlier, before the
//     apply had snapshotted anything.
//
// A nil lastKnownActiveCtx means no tabs-changed event has ever been observed
// for this session (a single-tab session, or a hand-built LiveView in a test):
// there is no evidence of any other tab, so nothing is stale.
//
// Must be called with lv.mu held.
func (lv *LiveView) viewportMeasurementIsStaleLocked(tabCtx, startActiveCtx context.Context) bool {
	cur := lv.lastKnownActiveCtx
	if cur == nil {
		return false
	}
	return cur != startActiveCtx || cur != tabCtx
}

// scaleDegradedNoticeInterval floors how often the user-facing "the picture
// may look soft" notice is pushed to attached viewers (round-2 finding F5).
// The deviceScaleFactor override is renderer-bound, and the SPA re-sends a
// viewport frame throughout a panel drag, so a renderer that is wedged for a
// few seconds would otherwise produce one banner per drag frame.
const scaleDegradedNoticeInterval = 30 * time.Second

// notifyScaleDegraded tells every attached viewer, in the panel, that the
// window resized but the sharpness override did not land (round-2 finding F5).
//
// Routed through the StatusSink fan-out because that is the live view's ONLY
// user-visible channel — the gateway turns each message into a
// browser_status(error) frame the panel renders as its status banner, which is
// exactly what round 1's gateway lane used for the not-the-controller viewport
// refusal. No new wire field is involved.
//
// Throttled (see scaleDegradedNotified's doc comment): once per degradation
// episode, and never more often than scaleDegradedNoticeInterval, so a
// renderer that stays wedged through a panel drag produces one banner rather
// than one per drag frame.
func (lv *LiveView) notifyScaleDegraded() {
	now := time.Now()
	lv.mu.Lock()
	if lv.scaleDegradedNotified && now.Sub(lv.scaleDegradedNotifiedAt) < scaleDegradedNoticeInterval {
		lv.mu.Unlock()
		return
	}
	sinks := lv.snapshotStatusSinksLocked()
	if len(sinks) == 0 {
		// Nobody is watching, so nothing was actually told — do NOT burn the
		// throttle window on a notice that reached no one, or a viewer who
		// attaches a second later would be silently owed a message that has
		// already been "sent".
		lv.mu.Unlock()
		return
	}
	lv.scaleDegradedNotified = true
	lv.scaleDegradedNotifiedAt = now
	lv.mu.Unlock()

	broadcastStatus(sinks,
		"the browser window resized, but the picture may look soft — the display-sharpness "+
			"setting timed out because the browser was busy; it will re-sharpen the next time you resize the panel")
}

// clearScaleDegraded re-arms the soft-picture notice after a successful scale
// override, so a later degradation is reported as the new event it is instead
// of being swallowed by the previous one's throttle window.
func (lv *LiveView) clearScaleDegraded() {
	lv.mu.Lock()
	lv.scaleDegradedNotified = false
	lv.mu.Unlock()
}

// viewportSettleBudget / viewportSettlePollInterval bound applyViewport's
// read-back, which is a settle-POLL rather than a single read.
//
// Browser.setWindowBounds returns as soon as the browser process has accepted
// the new bounds; the renderer relays out afterwards, so the tab's own CSS
// layout viewport catches up only 40-120ms later on an idle page and ~350ms
// later on a busy one (measured 2026-08-16). A single read taken the instant
// setWindowBounds returns therefore records the PRE-resize size about as often
// as the real one — and that number is what every subsequent click is mapped
// through. Polling until the tab reaches the requested size (or the budget
// runs out) is the difference between a verified measurement and a coin flip.
//
// The budget is sized above the busiest measured settle time with headroom. It
// is spent in full only when the tab genuinely never reaches the requested size
// — which is exactly the case where the extra reads are buying the true value.
const (
	viewportSettleBudget       = 600 * time.Millisecond
	viewportSettlePollInterval = 20 * time.Millisecond
)

// layoutMetricsAction reads the tab's actual CSS layout viewport via
// Page.getLayoutMetrics and writes the result through w/h (test-seam review
// MEDIUM finding: the duplicated Page.GetLayoutMetrics closure in
// SetViewport's read-back and rescaleToCSSViewport's cache-miss fetch is
// factored into readCSSLayoutViewport, and this type is the one shared
// chromedp.Action wrapper both call sites use around it). A small named type
// with pointer output fields, like windowBoundsAction, rather than a bare
// chromedp.ActionFunc closure capturing outer variables — same rationale:
// live_test.go's scripted runCDP stubs need to write scripted width/height
// values straight through w/h without executing a real CDP round trip.
type layoutMetricsAction struct {
	w, h *int64
}

// settleCSSViewport polls the tab's CSS layout viewport until it is within
// viewportDriftTolerancePx of (targetW, targetH) or viewportSettleBudget
// expires, and returns the last value it successfully read.
//
// Why a poll rather than the single read this used to do: Browser.setWindowBounds
// is answered by the browser process the moment it accepts the new bounds, but
// the renderer relays out afterwards — measured settle times are 40-120ms on an
// idle page and ~350ms on a busy one. A read taken immediately therefore
// records the PRE-resize size about as often as the post-resize one, with
// nothing to distinguish the two, and that number is what every subsequent
// click is mapped through.
//
// A poll that reads successfully but never converges is NOT an error. The tab
// really is that size — the chrome delta before compensation, or a resize
// Chrome declined outright — and recording the true size is exactly what keeps
// input mapping correct while the panel renders smaller than requested. Only a
// poll that never completed a single valid read returns an error, which the
// caller turns into a cache invalidation: a value nothing confirmed must never
// be cached (see invalidateCSSViewportCache).
//
// Must be called with no LiveView lock held (it makes CDP calls).
func (lv *LiveView) settleCSSViewport(tabCtx context.Context, targetW, targetH int) (int64, int64, error) {
	settleCtx, cancel := context.WithTimeout(tabCtx, viewportSettleBudget)
	defer cancel()
	deadline, _ := settleCtx.Deadline()
	var (
		lastW, lastH int64
		haveRead     bool
		lastErr      error
	)
	for {
		var w, h int64
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		err := lv.runCDP(settleCtx, remaining, layoutMetricsAction{w: &w, h: &h})
		switch {
		case err != nil:
			lastErr = err
		case w <= 0 || h <= 0:
			// Degenerate (e.g. cssLayoutViewport came back nil) — treated as a
			// failed read, never as a measurement.
			lastErr = fmt.Errorf("degenerate CSS viewport read back (%dx%d)", w, h)
		default:
			lastW, lastH, haveRead, lastErr = w, h, true, nil
			if viewportDeltaPx(targetW, w) <= viewportDriftTolerancePx &&
				viewportDeltaPx(targetH, h) <= viewportDriftTolerancePx {
				return w, h, nil
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(min(viewportSettlePollInterval, time.Until(deadline)))
		select {
		case <-settleCtx.Done():
			timer.Stop()
			if haveRead {
				return lastW, lastH, nil
			}
			return 0, 0, settleCtx.Err()
		case <-timer.C:
		}
	}
	if haveRead {
		return lastW, lastH, nil
	}
	if lastErr == nil {
		lastErr = settleCtx.Err()
		if lastErr == nil {
			lastErr = errors.New("no CSS viewport read completed")
		}
	}
	return 0, 0, lastErr
}

// Do implements chromedp.Action.
func (a windowBoundsAction) Do(ctx context.Context) error {
	windowID, _, werr := browser.GetWindowForTarget().Do(ctx)
	if werr != nil {
		return fmt.Errorf("get window for target: %w", werr)
	}
	return browser.SetWindowBounds(windowID, &browser.Bounds{
		Width:  int64(a.width),
		Height: int64(a.height),
	}).Do(ctx)
}

// Do implements chromedp.Action.
func (a layoutMetricsAction) Do(ctx context.Context) error {
	w, h, err := readCSSLayoutViewport(ctx)
	if err != nil {
		return err
	}
	*a.w, *a.h = w, h
	return nil
}

// readCSSLayoutViewport issues Page.getLayoutMetrics and returns the tab's
// CSS layout viewport's client width/height — the only thing that can prove
// a resize actually took effect (root-cause doc's "Exit proof" section).
// Pure CDP-call helper wrapped by layoutMetricsAction above; factored out on
// its own so the two call sites (SetViewport's read-back, rescaleToCSSViewport's
// cache-miss fetch) share exactly one implementation of the underlying
// protocol call instead of two near-identical duplicated closures (review
// MEDIUM finding).
func readCSSLayoutViewport(ctx context.Context) (w, h int64, err error) {
	// CDP GetLayoutMetrics returns 7 values and only two are meaningful here;
	// naming five throwaways would be noisier than the blanks.
	//nolint:dogsled // see above
	_, _, _, cssLayout, _, _, lerr := page.GetLayoutMetrics().Do(ctx)
	if lerr != nil {
		return 0, 0, lerr
	}
	if cssLayout != nil {
		w = cssLayout.ClientWidth
		h = cssLayout.ClientHeight
	}
	return w, h, nil
}

// viewportDeltaPx returns the absolute pixel gap between a requested int
// dimension and an actual CDP-reported int64 dimension — shared by
// SetViewport's compensation-trigger check and its final requested-vs-actual
// log/cache decision, which both need the identical comparison.
func viewportDeltaPx(requested int, actual int64) int {
	d := requested - int(actual)
	if d < 0 {
		return -d
	}
	return d
}

// clampViewportDim bounds a compensated window-bounds dimension to
// maxViewportDimension (SetViewport's compensation step, item 1 of the
// 2026-07-31 fix wave): the compensated size legitimately exceeds the
// ORIGINAL request by the window's own chrome delta, so the per-field
// ceiling is re-applied to the compensated value alone. The combined
// physical-pixel ceiling is deliberately NOT re-run here — it already gated
// the original request in SetViewport above, and a compensation delta is a
// small, window-chrome-sized correction, not an independent size request.
func clampViewportDim(v int) int {
	if v > maxViewportDimension {
		return maxViewportDimension
	}
	if v < 1 {
		return 1
	}
	return v
}

// invalidateCSSViewportCache zeroes the cached CSS viewport (review CRITICAL
// finding, item 2 of the 2026-07-31 fix wave): a stale-but-positive cache
// passes rescaleToCSSViewport's cache-hit guard (cssViewportW/H > 0) and
// silently mis-maps every subsequent click by the old/new ratio — exactly the
// bug class this whole fix exists to kill. Called whenever SetViewport's
// read-back (or its at-most-one compensated re-read) fails outright or comes
// back degenerate, so a broken read-back can never leave a confidently-wrong
// cache behind — the next input event's rescaleToCSSViewport call re-fetches
// from a known-empty (0,0) state instead of trusting a value that no longer
// corresponds to reality.
func (lv *LiveView) invalidateCSSViewportCache() {
	lv.mu.Lock()
	lv.cssViewportW, lv.cssViewportH = 0, 0
	lv.cssViewportScale = 0
	lv.mu.Unlock()
}

// signalRecapture asks the agent's WebRTC capture session to tear its stream
// down and re-bind it, optionally carrying the CDP-verified CSS viewport the
// encoder should converge on (0,0 = "no measurement to offer", which makes the
// encoder fall back to its own chrome.tabs.get stability poll). A no-op when
// this LiveView has no manager (hand-built in tests) or no capture session is
// active for this panel. Another panel's capture is never a fallback.
func (lv *LiveView) signalRecapture(w, h int) {
	if lv.mgr == nil {
		return
	}
	cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
	if cs == nil {
		return
	}
	if w > 0 && h > 0 {
		cs.RecaptureAt(w, h)
		return
	}
	cs.Recapture()
}

// signalRecaptureForTabChange is signalRecapture's "the active tab moved"
// counterpart: same control frame and PLI burst, but the capture session ALSO
// re-asserts this agent's model-active tab as Chrome's foreground tab first,
// so the encoder's own chrome.tabs.query({active:true}) cannot answer with a
// tab this manager no longer considers active (see
// CaptureSession.RecaptureForTabChangeAt).
//
// Round-2 finding F3: that re-assert used to be reachable only from
// BrowserManager.SwitchTab's rare "the model did not move" recovery branch,
// while THIS — the path every ordinary tab click takes — trusted
// activateTabInChrome, whose failure is a WARN log and nothing more. The
// hardening now sits on the common path.
func (lv *LiveView) signalRecaptureForTabChange(w, h int) {
	if lv.mgr == nil {
		return
	}
	cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
	if cs == nil {
		return
	}
	cs.RecaptureForTabChangeAt(w, h)
}

// reapplyViewportToNewTarget replays the panel's last requested viewport and
// device scale onto the tab reachable through tabCtx, then asks for a
// tab-change recapture at the size the tab actually reached. See
// onTabsChanged's call site for the per-target deviceScaleFactor measurement
// that makes this necessary.
//
// Returns whether this call has taken OWNERSHIP of the recapture the tab
// change owes — true when a worker is running (this call's own, or one it
// coalesced into, which will loop once more against the new target), false
// when there is nothing to replay because no viewport has ever been requested
// for this session. A false return obliges the caller to signal the recapture
// itself; otherwise a tab change with no prior resize would move the model and
// leave the picture behind.
//
// COALESCES rather than drops (round-2 finding F1 — see
// viewportReapplyInFlight's doc comment for the A -> B -> C failure and why it
// is a Linux-only one). A call arriving while a worker is in flight records
// its target and sets the pending flag; the worker re-reads the target and the
// panel's last requested geometry at the top of every pass, so a burst
// converges on the last tab instead of applying the first one's geometry and
// silently skipping the rest.
func (lv *LiveView) reapplyViewportToNewTarget(tabCtx context.Context) bool {
	if tabCtx == nil {
		return false
	}
	lv.mu.Lock()
	if lv.lastRequestedW <= 0 || lv.lastRequestedH <= 0 || lv.lastRequestedScale < 1 {
		lv.mu.Unlock()
		return false
	}
	// Recorded for BOTH the spawning and the coalescing case: the worker
	// always applies to the most recently observed target, never to the one
	// whose switch happened to start the worker.
	lv.pendingViewportTarget = tabCtx
	lv.viewportReapplyTargetCtx = tabCtx
	if lv.viewportReapplyInFlight {
		lv.viewportReapplyPending = true
		lv.mu.Unlock()
		return true
	}
	lv.viewportReapplyInFlight = true
	lv.mu.Unlock()

	go func() {
		for {
			lv.mu.Lock()
			target := lv.viewportReapplyTargetCtx
			w, h, scale := lv.lastRequestedW, lv.lastRequestedH, lv.lastRequestedScale
			lv.mu.Unlock()

			lv.reapplyViewportPass(target, w, h, scale)

			lv.mu.Lock()
			if !lv.viewportReapplyPending {
				lv.viewportReapplyInFlight = false
				lv.mu.Unlock()
				return
			}
			lv.viewportReapplyPending = false
			lv.mu.Unlock()
		}
	}()
	return true
}

// reapplyViewportPass applies and measures the new target once. Failed browser
// work leaves the old picture locked; it cannot authorize guessed geometry.
func (lv *LiveView) reapplyViewportPass(tabCtx context.Context, w, h int, scale float64) {
	if tabCtx == nil {
		return
	}
	if _, err := lv.applyViewportContextWithConvergence(context.Background(), tabCtx, w, h, scale, true); err != nil {
		logger.WarnCF("browser", "live view: could not re-apply the panel viewport", map[string]any{"session_id": lv.sessionID, "error": err.Error()})
	}
}

// viewportInputFetchTimeout bounds rescaleToCSSViewport's best-effort
// cache-miss fetch of the tab's CSS viewport. Deliberately much shorter than
// viewportSetTimeout: that timeout is sized for a user-triggered resize
// (rare, debounced), while this fetch runs on the dispatchInput -> WS
// read-loop hot path, once per input event on every cache miss — a
// slow/wedged CDP transport must fail fast here rather than stall input
// throughput up to a multi-second timeout per event (review CRITICAL
// finding: under a sustained CDP hiccup, the old shared timeout collapsed
// input throughput to roughly one event per timeout, with a fresh WARN log
// line each time).
const viewportInputFetchTimeout = 1 * time.Second

// viewportInputFetchBackoff bounds how soon rescaleToCSSViewport retries its
// cache-miss fetch after a failure. Once a fetch fails, further input events
// dispatch unscaled — without retrying the fetch or re-logging the failure —
// for this long, rather than repeating the same (bounded, but non-zero cost)
// failing CDP round trip on every single subsequent input event. See
// rescaleToCSSViewport's doc comment for the full failure-backoff mechanism.
const viewportInputFetchBackoff = 3 * time.Second

// rescaleToCSSViewport maps (x, y) from the client's capture-frame pixel
// space (capW x capH) into the tab's actual CSS pixel space, using the
// cssViewportW/H cache SetViewport maintains (root-cause doc Fault 3). Called
// with no LiveView lock held, per the ADR-038 CDP-call discipline this file
// observes everywhere else.
//
// If nothing has populated the cache yet — no SetViewport call this session
// yet, or a prior read-back was invalidated (invalidateCSSViewportCache) —
// this fetches it once via Page.getLayoutMetrics, bounded by the much
// shorter viewportInputFetchTimeout, NOT SetViewport's viewportSetTimeout
// (review CRITICAL finding, item 3): this call runs on the dispatchInput ->
// WS-read-loop hot path, once per input event on every cache miss, not on a
// rare, debounced user-triggered resize, so a slow/wedged CDP transport must
// fail fast here instead of stalling input throughput up to a multi-second
// timeout per event.
//
// On fetch failure it reports ok=false and the caller DROPS the event.
// This reverses the previous behavior, which dispatched unscaled on the
// reasoning that "a slightly-off click still beats a completely dead panel".
// Measurement killed that argument (2026-08-03): the capture frame and the CSS
// viewport differed by 562 vs 369 px, so an unscaled click lands ~34% below
// where the user aimed — reliably on the WRONG element, not merely near the
// right one. A dropped click is a no-op the user retries; a mis-aimed click
// activates something they did not choose, which on a real page can navigate
// away, delete, or submit. Silence beats a wrong action.
//
// Failure backoff (item 3b): a failed fetch arms lv.nextFetchAfter
// viewportInputFetchBackoff into the future. While that window is open,
// further cache-miss calls are DROPPED (see above) WITHOUT retrying the fetch or
// re-logging the failure — before this fix, a sustained CDP hiccup meant
// every subsequent input event repeated the same (bounded, but non-zero
// cost) failing round trip and logged a fresh WARN, collapsing input
// throughput to roughly one event per timeout. The failure is logged once,
// when it actually happens, not once per event that finds the cache still
// empty.
func (lv *LiveView) rescaleToCSSViewport(tabCtx context.Context, x, y, capW, capH float64) (rx, ry float64, ok bool) {
	lv.mu.Lock()
	cssW, cssH := lv.cssViewportW, lv.cssViewportH
	inBackoff := !lv.nextFetchAfter.IsZero() && time.Now().Before(lv.nextFetchAfter)
	lv.mu.Unlock()

	if cssW <= 0 || cssH <= 0 {
		if inBackoff {
			// Already logged when the fetch actually failed — staying quiet
			// here is the whole point of the backoff window.
			return 0, 0, false
		}

		var w, h int64
		err := lv.runCDP(tabCtx, viewportInputFetchTimeout, layoutMetricsAction{w: &w, h: &h})
		// A canceled caller abandoned this read. It says nothing about the
		// target's health and must not put subsequent input into backoff.
		if tabCtx.Err() != nil {
			return 0, 0, false
		}
		if err != nil || w <= 0 || h <= 0 {
			logger.WarnCF(
				"browser",
				"live view: input rescale — could not read the tab's CSS viewport, DROPPING this positional event (backing off further fetches)",
				map[string]any{
					"session_id":      lv.sessionID,
					"backoff_seconds": viewportInputFetchBackoff.Seconds(),
				},
			)
			lv.mu.Lock()
			lv.nextFetchAfter = time.Now().Add(viewportInputFetchBackoff)
			lv.viewportFetchFailures++
			lv.mu.Unlock()
			return 0, 0, false
		}

		lv.mu.Lock()
		lv.cssViewportW, lv.cssViewportH = int(w), int(h)
		// Restore the device scale alongside the dimensions. Page.getLayoutMetrics
		// cannot report the scale, so this refill used to set width/height and
		// leave cssViewportScale at zero — meaning that after ANY cache
		// invalidation the scale stayed zero for the rest of the session and
		// viewportBasisForCapture's capture-derived fallback was permanently
		// disabled without anything saying so. lastAppliedScale is the scale
		// whose CDP override actually landed on this tab and is still in force
		// on it, so restoring it here is a statement of fact, not a guess (it is
		// zero only when no scale was ever successfully applied).
		lv.cssViewportScale = lv.lastAppliedScale
		lv.nextFetchAfter = time.Time{}
		lv.viewportFetchFailures = 0
		lv.mu.Unlock()
		cssW, cssH = int(w), int(h)
	}

	basisW, basisH := lv.viewportBasisForCapture(tabCtx, capW, capH, float64(cssW), float64(cssH))
	rx, ry = rescaleInputCoords(x, y, capW, capH, basisW, basisH)
	return rx, ry, true
}

// viewportBasisProbeTTL is how long viewportBasisForCapture reuses the answer
// of its "who is right, the cache or the capture?" probe for an unchanged
// capture/cache geometry pair. The probe costs one CDP round trip and its call
// site is per input event (hundreds per scroll), so without this a single
// disagreement would put a round trip in front of every mouse move.
const viewportBasisProbeTTL = 2 * time.Second

// viewportBasisForCapture returns the CSS width/height that the capture frame
// (capW x capH) actually depicts, which is what input coordinates must be
// mapped into.
//
// Normally that is the cached layout viewport (cssW x cssH) — including when
// the encoder downscales the stream under load, because a downscale preserves
// the aspect ratio, so the capture is a scaled copy of the same surface and the
// ratio math stays exact (root-cause doc Fault 3). That is the fast path and it
// costs nothing.
//
// When the two disagree in SHAPE, one of them is describing a surface the tab
// does not have — and the whole question is WHICH. The first version of this
// guard answered it by assuming the capture is always the tab rendered 1:1, so
// capture-divided-by-scale had to be the truth. encoder.js documents that
// assumption as false: it letterboxes whenever its pinned stream size does not
// match the tab, and a letterboxed frame contains the tab plus bars that are
// not part of any page. On the operator's install (2026-08-15) a cached
// 633x686 met a 1600x1018 capture with no drift warning anywhere — there the
// CACHE was right and the CAPTURE was wrong, and re-basing onto the capture
// mapped every click onto a surface the tab never had.
//
// So this no longer guesses from either side's shape. It spends ONE
// Page.getLayoutMetrics round trip asking the tab itself, and then:
//
//   - the tab backs the CACHE -> the capture is the wrong one. Coordinates keep
//     mapping through the viewport (which is correct), AND a recapture is
//     requested at the verified size so the PICTURE gets fixed too, not just the
//     arithmetic behind it. Rate-limited: the condition persists until the
//     encoder actually re-negotiates, and an unlimited request would loop the
//     video.
//   - the tab backs the CAPTURE -> the cache is stale. It is refreshed from the
//     verified read and coordinates map through that, which is exact whatever
//     the scale happens to be.
//   - neither -> nothing here can be trusted, so the previous behaviour is kept
//     (capture-derived when a scale is recorded, cached viewport otherwise) and
//     the disagreement is logged loudly.
//
// The probe's verdict is memoized per capture/cache geometry for
// viewportBasisProbeTTL, because this is called once per input event and a
// round trip in front of every mouse move would cost more than the mis-aim it
// prevents. The warning latches on the same key rather than once per LiveView,
// so a mismatch that RECURS is visible as a recurrence instead of looking like
// one historical event somebody already dealt with.
//
// Must be called with no LiveView lock held (it can make a CDP call).
func (lv *LiveView) viewportBasisForCapture(tabCtx context.Context, capW, capH, cssW, cssH float64) (float64, float64) {
	if capW <= 0 || capH <= 0 || cssW <= 0 || cssH <= 0 {
		return cssW, cssH
	}
	if viewportAspectsAgree(capW/capH, cssW/cssH) {
		return cssW, cssH
	}

	key := viewportBasisKey(capW, capH, cssW, cssH)
	lv.mu.Lock()
	if lv.basisProbeKey == key && !lv.basisProbeAt.IsZero() &&
		time.Since(lv.basisProbeAt) < viewportBasisProbeTTL {
		w, h := lv.basisProbeW, lv.basisProbeH
		lv.mu.Unlock()
		return w, h
	}
	lv.mu.Unlock()

	fields := map[string]any{
		"session_id":        lv.sessionID,
		"capture_width":     capW,
		"capture_height":    capH,
		"cached_css_width":  cssW,
		"cached_css_height": cssH,
	}

	var tw, th int64
	if tabCtx == nil || lv.runCDP == nil {
		lv.warnBasisOnce(key,
			"live view: input rescale — the video frame's shape disagrees with the browser tab's known size, and the tab cannot be asked which is right; mapping through the known size (clicks may be mis-aimed)",
			fields)
		lv.rememberBasis(key, cssW, cssH)
		return cssW, cssH
	}
	if err := lv.runCDP(tabCtx, viewportInputFetchTimeout, layoutMetricsAction{w: &tw, h: &th}); err != nil || tw <= 0 || th <= 0 {
		if err != nil {
			fields["error"] = err.Error()
		}
		lv.warnBasisOnce(key,
			"live view: input rescale — the video frame's shape disagrees with the browser tab's known size, and the tab did not answer when asked which is right; mapping through the known size (clicks may be mis-aimed)",
			fields)
		// Remembered so a wedged transport does not put a failing round trip in
		// front of every input event; the TTL retries on its own.
		lv.rememberBasis(key, cssW, cssH)
		return cssW, cssH
	}
	tabW, tabH := float64(tw), float64(th)
	fields["tab_width"], fields["tab_height"] = tabW, tabH

	switch {
	case viewportDeltaPx(int(cssW), tw) <= viewportDriftTolerancePx &&
		viewportDeltaPx(int(cssH), th) <= viewportDriftTolerancePx:
		// The tab agrees with what we already had, so the VIDEO is the thing
		// that is wrong — it is showing a differently-shaped surface (the
		// encoder letterboxing a stream pinned at a size the tab no longer has).
		// Clicks stay correct by continuing to map through the tab's real size;
		// the picture is fixed by asking for a fresh capture at that size.
		lv.warnBasisOnce(key,
			"live view: the video frame does not match the shape of the browser tab it is showing — clicks are still mapped correctly, and a fresh capture has been requested to fix the picture",
			fields)
		lv.requestBasisRecapture(int(cssW), int(cssH))
		lv.rememberBasis(key, cssW, cssH)
		return cssW, cssH

	case viewportAspectsAgree(capW/capH, tabW/tabH):
		// The tab agrees with the capture, so the CACHE is the stale one.
		// Refresh it from the verified read and map through that — exact ratio
		// math, with no dependence on a recorded device scale factor.
		lv.mu.Lock()
		lv.cssViewportW, lv.cssViewportH = int(tabW), int(tabH)
		lv.mu.Unlock()
		lv.warnBasisOnce(key,
			"live view: input rescale — the remembered browser-tab size was out of date; refreshed it from the tab itself so clicks land where they are aimed",
			fields)
		lv.rememberBasis(key, tabW, tabH)
		return tabW, tabH

	default:
		// The tab matches neither. Keep the previous behaviour rather than
		// inventing a third answer, and say so loudly — this is the case where
		// clicks genuinely may be mis-aimed and nothing here can prove otherwise.
		lv.mu.Lock()
		scale := lv.cssViewportScale
		lv.mu.Unlock()
		basisW, basisH := cssW, cssH
		if scale >= 1 {
			basisW, basisH = capW/scale, capH/scale
		}
		fields["device_scale_factor"] = scale
		fields["basis_width"], fields["basis_height"] = basisW, basisH
		lv.warnBasisOnce(key,
			"live view: input rescale — the video frame, the remembered tab size and the tab's own reported size all disagree; clicks may be mis-aimed until the next resize",
			fields)
		lv.rememberBasis(key, basisW, basisH)
		return basisW, basisH
	}
}

// viewportAspectTolerance is how far the capture frame's aspect ratio may
// differ from the cached layout viewport's before the two are treated as
// describing DIFFERENT surfaces. 2% absorbs rounding (odd pixel dimensions,
// the encoder's even-number alignment) while the failure this guards against
// is an order of magnitude larger — measured live on UAT at 26%.
const viewportAspectTolerance = 0.02

// viewportAspectsAgree reports whether two aspect ratios describe the same
// surface within viewportAspectTolerance (relative to the second).
func viewportAspectsAgree(a, b float64) bool {
	return math.Abs(a-b) <= viewportAspectTolerance*b
}

// viewportBasisKey identifies one capture-vs-cache geometry pair, for the probe
// memo and the warning latch.
func viewportBasisKey(capW, capH, cssW, cssH float64) string {
	return fmt.Sprintf("%.0fx%.0f|%.0fx%.0f", capW, capH, cssW, cssH)
}

// rememberBasis memoizes a probe verdict for viewportBasisProbeTTL.
func (lv *LiveView) rememberBasis(key string, basisW, basisH float64) {
	lv.mu.Lock()
	lv.basisProbeKey = key
	lv.basisProbeW, lv.basisProbeH = basisW, basisH
	lv.basisProbeAt = time.Now()
	lv.mu.Unlock()
}

// warnBasisOnce logs msg at most once per capture/cache geometry — see
// basisWarnedKey's doc comment for why the latch is per geometry rather than
// once per LiveView.
func (lv *LiveView) warnBasisOnce(key, msg string, fields map[string]any) {
	lv.mu.Lock()
	already := lv.basisWarnedKey == key
	lv.basisWarnedKey = key
	lv.mu.Unlock()
	if already {
		return
	}
	logger.WarnCF("browser", msg, fields)
}

// viewportBasisRecaptureInterval rate-limits the recapture viewportBasisForCapture
// asks for when it proves the CAPTURE (not the cache) is the wrong one. A
// recapture tears down and re-negotiates the WebRTC stream, and the condition
// that triggers it can persist, so an unlimited request would loop the video.
const viewportBasisRecaptureInterval = 5 * time.Second

// requestBasisRecapture asks for a fresh capture at the tab's verified size,
// no more often than viewportBasisRecaptureInterval. The rate limit is the
// point: the shape mismatch persists until the encoder actually re-negotiates
// its stream, so an unconditional request on every probe would tear the video
// down in a loop and the panel would never settle.
func (lv *LiveView) requestBasisRecapture(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	lv.mu.Lock()
	if !lv.nextBasisRecaptureAt.IsZero() && time.Now().Before(lv.nextBasisRecaptureAt) {
		lv.mu.Unlock()
		return
	}
	lv.nextBasisRecaptureAt = time.Now().Add(viewportBasisRecaptureInterval)
	lv.mu.Unlock()
	lv.signalRecapture(w, h)
}

// rescaleInputCoords maps (x, y) from the capture frame's pixel space
// (capW x capH) into the tab's CSS pixel space (cssW x cssH). Pure math, no
// CDP — factored out of rescaleToCSSViewport so it is unit-testable without
// a real Chromium (root-cause doc Fault 3: the assumption that
// videoWidth/videoHeight == page CSS pixels is false whenever the encoder
// downscales, e.g. the measured 319x158 capture against a ~1280-wide page).
// Callers must guard against capW/capH <= 0 themselves — this function does
// not, so it can stay a trivial, allocation-free ratio computation.
func rescaleInputCoords(x, y, capW, capH, cssW, cssH float64) (float64, float64) {
	return x * cssW / capW, y * cssH / capH
}

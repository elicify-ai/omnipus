package browser

import (
	"context"
	"fmt"
	"math"

	"github.com/chromedp/chromedp"
)

// viewportFrameGeometryAction reads the actual CSS viewport and device scale.
type viewportFrameGeometryAction struct {
	width, height *int
	scale         *float64
}

func (a viewportFrameGeometryAction) Do(ctx context.Context) error {
	w, h, err := readCSSLayoutViewport(ctx)
	if err != nil {
		return err
	}
	if err := chromedp.Evaluate("window.devicePixelRatio", a.scale).Do(ctx); err != nil {
		return err
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
func (lv *LiveView) acceptViewportConvergence(target context.Context, measured CaptureFrameState, manualResize bool) error {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.pendingViewportTarget != target {
		return nil
	}
	if !manualResize && (viewportDeltaPx(lv.lastRequestedW, int64(measured.Width)) > viewportDriftTolerancePx || viewportDeltaPx(lv.lastRequestedH, int64(measured.Height)) > viewportDriftTolerancePx) {
		return fmt.Errorf("browser live: new tab viewport is still settling")
	}
	lv.pendingViewportTarget = nil
	return nil
}

func (lv *LiveView) applyViewportContext(caller, tabCtx context.Context, width, height int, scale float64) (bool, error) {
	return lv.applyViewportContextWithConvergence(caller, tabCtx, width, height, scale, false)
}

// Newly active targets can briefly report their pre-compensation layout after
// Chrome acknowledges window bounds. Reapply once before pinning capture to it;
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
			if before.Width == width && before.Height == height && before.Scale == scale {
				measured, err := lv.measureCaptureFrame(operation, cs)
				if err != nil {
					return false, err
				}
				if sameViewportGeometry(before, measured) {
					if err := lv.acceptViewportConvergence(tabCtx, measured, !converge); err != nil {
						return false, err
					}
					lv.mu.Lock()
					lv.lastRequestedW, lv.lastRequestedH, lv.lastRequestedScale = width, height, scale
					lv.mu.Unlock()
					return true, nil
				}
			}
		}
		anyApplied := false
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
			applied, err := lv.applyViewportAdmitted(caller, tabCtx, operation, width, height, scale)
			anyApplied = anyApplied || applied
			if err != nil || cs == nil {
				return anyApplied, err
			}
			measured, err := lv.measureCaptureFrame(operation, cs)
			if err != nil {
				return anyApplied, err
			}
			if converge && (viewportDeltaPx(width, int64(measured.Width)) > viewportDriftTolerancePx || viewportDeltaPx(height, int64(measured.Height)) > viewportDriftTolerancePx) {
				if attempt == 0 {
					continue
				}
				return anyApplied, fmt.Errorf("browser live: new tab viewport did not converge: requested %dx%d, measured %dx%d", width, height, measured.Width, measured.Height)
			}
			if err := lv.acceptViewportConvergence(tabCtx, measured, !converge); err != nil {
				return anyApplied, err
			}
			ready, err = cs.BeginFrameTransition(measured.TargetID, measured.Width, measured.Height, measured.Scale)
			return anyApplied, err
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
		if convergenceErr := lv.acceptViewportConvergence(tabCtx, measured, false); convergenceErr != nil {
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

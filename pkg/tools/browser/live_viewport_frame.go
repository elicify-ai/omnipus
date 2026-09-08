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
	if err := lv.runCDP(ctx, viewportScaleTimeout, viewportFrameGeometryAction{&w, &h, &scale}); err != nil {
		return CaptureFrameState{}, err
	}
	if err := ctx.Err(); err != nil {
		return CaptureFrameState{}, err
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

func (lv *LiveView) applyViewportContext(caller, tabCtx context.Context, width, height int, scale float64) (bool, error) {
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
					lv.mu.Lock()
					lv.lastRequestedW, lv.lastRequestedH, lv.lastRequestedScale = width, height, scale
					lv.mu.Unlock()
					return true, nil
				}
			}
		}
		applied, err := lv.applyViewportAdmitted(caller, tabCtx, operation, width, height, scale)
		if err != nil || cs == nil {
			return applied, err
		}
		measured, err := lv.measureCaptureFrame(operation, cs)
		if err != nil {
			return applied, err
		}
		ready, err = cs.BeginFrameTransition(measured.TargetID, measured.Width, measured.Height, measured.Scale)
		return applied, err
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
		_, target, err := lv.mgr.activeTargetSnapshot(sessionID)
		if err != nil {
			return false, err
		}
		before := expected.FrameState()
		if before.TargetID != string(target) {
			if _, err := expected.BeginFrameTransition(string(target), 0, 0, before.Scale); err != nil {
				return false, err
			}
		}
		measured, err := lv.measureCaptureFrame(operation, expected)
		if err != nil {
			return false, err
		}
		if sameViewportGeometry(before, measured) {
			return true, nil
		}
		ready, err = expected.BeginFrameTransition(measured.TargetID, measured.Width, measured.Height, measured.Scale)
		return true, err
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

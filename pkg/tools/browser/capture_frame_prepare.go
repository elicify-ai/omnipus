package browser

import (
	"context"
	"fmt"
	"math"

	"github.com/chromedp/chromedp"
)

// captureGeometryReader measures the selected target at the browser boundary.
type captureGeometryReader func(context.Context) (int, int, float64, error)

func (cs *CaptureSession) prepareEncoderFrame(ctx context.Context, measure captureGeometryReader) (CaptureFrameState, error) {
	ctx, cancel := context.WithTimeout(ctx, captureStartTimeout)
	defer cancel()
	if cs.mgr == nil || measure == nil {
		return CaptureFrameState{}, fmt.Errorf("capture session: browser and geometry reader are required")
	}
	if err := ctx.Err(); err != nil {
		return CaptureFrameState{}, err
	}
	select {
	case <-cs.Done():
		return CaptureFrameState{}, context.Canceled
	default:
	}
	// Preparation precedes the encoder target, so Stop cannot cancel it by
	// closing that target. This waiter ends on Stop or the deferred cancel.
	go func() {
		select {
		case <-cs.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	panelID := cs.panelTabSet()
	// Normal viewer startup already has a tab. Only the boot-time capture
	// path may need lazy session creation; never recreate while owning admission.
	if _, _, err := cs.mgr.activeTargetSnapshot(panelID); err != nil {
		if _, err := cs.mgr.SessionContext(ctx, panelID); err != nil {
			return CaptureFrameState{}, fmt.Errorf("capture session: prepare browsing session: %w", err)
		}
	}
	release, err := cs.mgr.acquireLiveTabCommand(ctx, panelID)
	if err != nil {
		return CaptureFrameState{}, err
	}
	defer release()
	targetCtx, targetID, err := cs.mgr.activeTargetSnapshot(panelID)
	if err != nil {
		return CaptureFrameState{}, err
	}
	cs.mgr.mu.Lock()
	entry := cs.mgr.sessions[panelID]
	cs.mgr.mu.Unlock()
	deadline, _ := ctx.Deadline()
	measureCtx, cancelMeasure := context.WithDeadline(targetCtx, deadline)
	stop := context.AfterFunc(ctx, cancelMeasure)
	defer func() { stop(); cancelMeasure() }()
	width, height, scale, err := measure(measureCtx)
	if err != nil {
		return CaptureFrameState{}, fmt.Errorf("capture session: measure target layout: %w", err)
	}
	if err := measureCtx.Err(); err != nil {
		return CaptureFrameState{}, err
	}
	if width < 1 || height < 1 || width > 16384 || height > 16384 || math.IsNaN(scale) || scale < 1 || scale > 4 {
		return CaptureFrameState{}, fmt.Errorf("capture session: target layout is not a valid measured viewport")
	}
	// Close/shutdown can invalidate a session while browser I/O is pending.
	// Admission serializes ordinary mutations; this check also fences removal.
	cs.mgr.mu.Lock()
	current := cs.mgr.sessions[panelID]
	valid := current == entry && current != nil && current.active() != nil && current.active().ctx == targetCtx && current.active().targetID == targetID
	cs.mgr.mu.Unlock()
	if !valid {
		return CaptureFrameState{}, fmt.Errorf("capture session: target changed during layout measurement")
	}
	return cs.BeginFrameTransition(string(targetID), width, height, scale)
}

func measureCaptureGeometry(ctx context.Context) (int, int, float64, error) {
	var width, height int64
	var scale float64
	err := chromedp.Run(ctx,
		layoutMetricsAction{w: &width, h: &height},
		chromedp.Evaluate("window.devicePixelRatio", &scale),
	)
	return int(width), int(height), scale, err
}

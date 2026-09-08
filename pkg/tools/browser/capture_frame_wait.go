package browser

import (
	"context"
	"errors"
	"fmt"
)

var ErrStaleCaptureFrame = errors.New("capture session: stale frame claim")

// WaitConfirmedFrame waits for measured CSS geometry. Empty ID and zero
// generation mean that this initial request has not claimed a frame yet; it
// follows pending transitions until a measured frame exists. A supplied claim
// stays exact and returns ErrStaleCaptureFrame if the current identity changes.
// Decoder readiness is separate and is not required by signaling.
func (cs *CaptureSession) WaitConfirmedFrame(ctx context.Context, captureID string, generation uint64) (CaptureFrameState, error) {
	if ctx == nil || (captureID == "") != (generation == 0) {
		return CaptureFrameState{}, fmt.Errorf("capture session: frame wait requires a context and a complete claim")
	}
	for {
		cs.mu.Lock()
		if err := context.Cause(ctx); err != nil {
			cs.mu.Unlock()
			return CaptureFrameState{}, err
		}
		if cs.stopped {
			cs.mu.Unlock()
			return CaptureFrameState{}, fmt.Errorf("capture session: stopped while waiting for geometry")
		}
		frame := cs.frameStateLocked()
		if captureID != "" && (frame.CaptureID != captureID || frame.Generation != generation) {
			cs.mu.Unlock()
			return CaptureFrameState{}, ErrStaleCaptureFrame
		}
		if frame.Generation != 0 && frame.Width > 0 && frame.Height > 0 {
			cs.mu.Unlock()
			return frame, nil
		}
		if cs.frameCtx == nil {
			cs.frameCtx, cs.frameCancel = context.WithCancel(context.Background())
		}
		changed, stopped := cs.frameCtx.Done(), cs.done
		cs.mu.Unlock()
		select {
		case <-ctx.Done():
			return CaptureFrameState{}, context.Cause(ctx)
		case <-stopped:
			return CaptureFrameState{}, fmt.Errorf("capture session: stopped while waiting for geometry")
		case <-changed:
		}
	}
}

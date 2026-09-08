package browser

import (
	"context"
	"fmt"
)

// A document lifetime is independent of viewport generations. The caller proves
// a new-document paint and measures under the live command gate before completing.
type captureDocumentTransition struct { // not-wire-format: capture-local document lifetime.
	ctx      context.Context
	cancel   context.CancelFunc
	targetID string
}

func (cs *CaptureSession) beginDocumentTransition(targetID string) (*captureDocumentTransition, error) {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return nil, context.Canceled
	}
	scale := cs.frames.snapshot().Geometry.Scale
	if scale == 0 {
		scale = 1
	}
	if _, err := cs.frames.beginForced(captureFrameGeometry{TargetID: targetID, Scale: scale}); err != nil {
		cs.mu.Unlock()
		return nil, err
	}
	cs.retireDocumentTransitionLocked()
	ctx, cancel := context.WithCancel(context.Background())
	token := &captureDocumentTransition{ctx: ctx, cancel: cancel, targetID: targetID}
	cs.documentTransition = token
	cs.replaceFrameLifetimeLocked()
	cs.resetCaptureHealthForFrameLocked()
	event := cs.claimFramePublicationLocked()
	frame, notify := cs.frameStateLocked(), cs.onFrameState
	cs.mu.Unlock()
	if event.Version != 0 {
		cs.emitVideoHealth(event)
	}
	if notify != nil {
		notify(frame)
	}
	return token, nil
}

// Completion always creates a fresh generation: hidden pending geometry and
// measured geometry must never share one immutable wire identity. The token's
// context ends here; subsequent recapture uses the caller's original lifetime.
func (cs *CaptureSession) completeDocumentTransition(token *captureDocumentTransition, width, height int, scale float64) (CaptureFrameState, error) {
	cs.mu.Lock()
	if !cs.documentTransitionCurrentLocked(token) {
		cs.mu.Unlock()
		return CaptureFrameState{}, ErrStaleCaptureFrame
	}
	if width <= 0 || height <= 0 {
		cs.mu.Unlock()
		return CaptureFrameState{}, fmt.Errorf("capture session: document geometry must be measured")
	}
	if _, err := cs.frames.beginForced(captureFrameGeometry{TargetID: token.targetID, Width: width, Height: height, Scale: scale}); err != nil {
		cs.mu.Unlock()
		return CaptureFrameState{}, err
	}
	cs.retireDocumentTransitionLocked()
	cs.replaceFrameLifetimeLocked()
	cs.resetCaptureHealthForFrameLocked()
	event := cs.claimFramePublicationLocked()
	frame, notify := cs.frameStateLocked(), cs.onFrameState
	cs.mu.Unlock()
	if event.Version != 0 {
		cs.emitVideoHealth(event)
	}
	if notify != nil {
		notify(frame)
	}
	return frame, nil
}

func (cs *CaptureSession) documentTransitionCurrent(token *captureDocumentTransition) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.documentTransitionCurrentLocked(token)
}

// Caller holds cs.mu for these predicates and context-only retirement.
func (cs *CaptureSession) documentTransitionCurrentLocked(token *captureDocumentTransition) bool {
	return !cs.stopped && token != nil && token == cs.documentTransition && token.ctx.Err() == nil && token.targetID == cs.frames.snapshot().Geometry.TargetID
}
func (cs *CaptureSession) documentPendingLocked() bool { return cs.documentTransition != nil }
func (cs *CaptureSession) retireDocumentTransitionLocked() {
	if token := cs.documentTransition; token != nil {
		cs.documentTransition = nil
		token.cancel()
	}
}

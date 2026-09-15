package browser

import (
	"context"
	"errors"
)

// RecaptureFrame requests an ordinary recapture of the original measured frame.
func (cs *CaptureSession) RecaptureFrame(frame CaptureFrameState) bool {
	cs.mu.Lock()
	if cs.stopped || cs.documentPendingLocked() || !sameCaptureFrameIdentity(cs.frameStateLocked(), frame) ||
		(cs.ingestContextBound && (frame.Width <= 0 || frame.Height <= 0 || cs.ingestRecapture == nil || cs.ingestBindingCtx == nil || cs.ingestBindingCtx.Err() != nil)) {
		cs.mu.Unlock()
		return false
	}
	request := context.Background()
	if cs.ingestContextBound {
		request = cs.ingestBindingCtx
	}
	cs.noteExplicitRecaptureIssuedLocked(ingestRecoverySettle)
	cs.mu.Unlock()
	return cs.RecaptureFrameContext(request, frame)
}

// RecaptureFrameContext keeps the originating frame, binding and request alive
// through final transport admission. Readiness is not required: the first
// recapture establishes the media needed to make a measured frame ready.
func (cs *CaptureSession) RecaptureFrameContext(ctx context.Context, frame CaptureFrameState) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	cs.mu.Lock()
	if cs.documentPendingLocked() {
		cs.mu.Unlock()
		return false
	}
	// The explicit legacy BindIngest API predates frame-qualified transport.
	// Authenticated bindings retain ingestContextBound even after unbinding,
	// so they cannot enter this compatibility path when their sender disappears.
	if !cs.ingestContextBound {
		valid := !cs.stopped && sameCaptureFrameIdentity(cs.frameStateLocked(), frame)
		send := cs.ingestSend
		cs.mu.Unlock()
		if !valid || ctx.Err() != nil {
			return false
		}
		if send != nil && send("recapture", nil, frame.Width, frame.Height, 0) != nil {
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		cs.relay.SignalRecapture()
		return true
	}
	if cs.stopped || frame.Generation == 0 || frame.TargetID == "" || frame.Width <= 0 || frame.Height <= 0 ||
		!sameCaptureFrameIdentity(cs.frameStateLocked(), frame) || cs.ingestRecapture == nil ||
		cs.ingestBindingCtx == nil || cs.ingestBindingCtx.Err() != nil || cs.frameCtx == nil || cs.frameCtx.Err() != nil {
		cs.mu.Unlock()
		return false
	}
	send, epoch := cs.ingestRecapture, cs.ingestEpoch
	binding, frameCtx := cs.ingestBindingCtx, cs.frameCtx
	cs.mu.Unlock()
	request, cancel := context.WithCancel(ctx)
	stopBinding := context.AfterFunc(binding, cancel)
	stopFrame := context.AfterFunc(frameCtx, cancel)
	defer func() {
		stopBinding()
		stopFrame()
		cancel()
	}()
	current := func() bool {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		return request.Err() == nil && binding.Err() == nil && frameCtx.Err() == nil && !cs.stopped && !cs.documentPendingLocked() &&
			cs.ingestEpoch == epoch && cs.ingestBindingCtx == binding && cs.frameCtx == frameCtx &&
			sameCaptureFrameIdentity(cs.frameStateLocked(), frame)
	}
	if !current() {
		return false
	}
	if err := send(request, frame, current); err != nil {
		if request.Err() == nil && !errors.Is(err, context.Canceled) {
			cs.logf("capture[%s]: frame recapture failed: %v", cs.agentID, err)
		}
		return false
	}
	if !current() {
		return false
	}
	cs.relay.SignalRecapture()
	return true
}

func sameCaptureFrameIdentity(a, b CaptureFrameState) bool {
	return a.CaptureID == b.CaptureID && a.Generation == b.Generation && a.TargetID == b.TargetID && a.Width == b.Width && a.Height == b.Height && a.Scale == b.Scale
}

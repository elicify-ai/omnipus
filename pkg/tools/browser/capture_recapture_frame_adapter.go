package browser

import "context"

// RecaptureFrame requests an ordinary recapture of the original measured frame.
func (cs *CaptureSession) RecaptureFrame(frame CaptureFrameState) bool {
	return cs.RecaptureFrameContext(context.Background(), frame)
}

// RecaptureFrameContext is a temporary integration adapter. The root writer
// owner must replace its legacy requestControl call with immutable command and
// binding/frame/episode admission at final write. This pre-dispatch check alone
// cannot fence a command already waiting in that legacy writer.
func (cs *CaptureSession) RecaptureFrameContext(ctx context.Context, frame CaptureFrameState) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	cs.mu.Lock()
	current := !cs.stopped && sameCaptureFrameIdentity(cs.frameStateLocked(), frame)
	cs.mu.Unlock()
	if !current || ctx.Err() != nil {
		return false
	}
	cs.requestControl("recapture", nil, frame.Width, frame.Height)
	cs.relay.SignalRecapture()
	return true
}

func sameCaptureFrameIdentity(a, b CaptureFrameState) bool {
	return a.CaptureID == b.CaptureID && a.Generation == b.Generation && a.TargetID == b.TargetID && a.Width == b.Width && a.Height == b.Height && a.Scale == b.Scale
}

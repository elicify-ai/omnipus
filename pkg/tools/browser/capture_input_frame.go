package browser

import "context"

// Keep an admitted command inside the exact picture lifetime. The protocol
// may already have accepted a press when cancellation arrives; held-input
// bookkeeping still records uncertain delivery and performs its usual cleanup.
func (cs *CaptureSession) inputFrameLifetime(captureID string, generation uint64) (context.Context, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	frame := cs.frameStateLocked()
	if !frame.Ready || frame.CaptureID != captureID || frame.Generation != generation || cs.frameCtx == nil || cs.frameCtx.Err() != nil {
		return nil, false
	}
	return cs.frameCtx, true
}

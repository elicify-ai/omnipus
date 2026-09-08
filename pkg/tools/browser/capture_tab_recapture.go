package browser

import "context"

type captureTabRecaptureRequest struct { // not-wire-format: internal coalesced work.
	width, height int
	send          func(string, *string, int, int, int) error
	qualified     bool
	frame         CaptureFrameState
	binding       context.Context
	frameCtx      context.Context
}

func (cs *CaptureSession) runTabRecaptures(request captureTabRecaptureRequest) {
	first := true
	for {
		if !first && !request.qualified {
			cs.relay.SignalRecapture()
		}
		first = false
		cs.runTabRecapture(request)
		cs.mu.Lock()
		if !cs.tabChangeRecapturePending || cs.stopped {
			cs.tabChangeRecaptureRunning = false
			cs.tabChangeRecapturePending = false
			cs.tabChangeRecaptureRequest = captureTabRecaptureRequest{}
			cs.mu.Unlock()
			return
		}
		request = cs.tabChangeRecaptureRequest
		cs.tabChangeRecapturePending = false
		cs.mu.Unlock()
	}
}

func (cs *CaptureSession) runTabRecapture(request captureTabRecaptureRequest) {
	if request.qualified {
		ctx, cancel := context.WithCancel(request.binding)
		stopFrame := context.AfterFunc(request.frameCtx, cancel)
		defer func() { stopFrame(); cancel() }()
		if ctx.Err() != nil || request.frameCtx.Err() != nil {
			return
		}
		cs.assertForeground(ctx)
		cs.mu.Lock()
		current := ctx.Err() == nil && request.frameCtx.Err() == nil && !cs.stopped &&
			cs.ingestBindingCtx == request.binding && cs.frameCtx == request.frameCtx &&
			sameCaptureFrameIdentity(cs.frameStateLocked(), request.frame)
		if current {
			cs.noteExplicitRecaptureIssuedLocked(ingestRecoverySettle)
		}
		cs.mu.Unlock()
		if current {
			cs.RecaptureFrameContext(ctx, request.frame)
		}
		return
	}
	cs.assertForeground(context.Background())
	cs.mu.Lock()
	current := !cs.stopped && !cs.ingestContextBound
	if current {
		cs.noteExplicitRecaptureIssuedLocked(ingestRecoverySettle)
	}
	cs.mu.Unlock()
	if current && request.send != nil {
		if err := request.send("recapture", nil, request.width, request.height, 0); err != nil {
			cs.logf("capture[%s]: legacy tab recapture failed: %v", cs.agentID, err)
		}
	}
}

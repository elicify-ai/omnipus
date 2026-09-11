package browser

import "context"

// Caller holds cs.mu. Context cancellation never calls capture observers.
func (cs *CaptureSession) retireIngestRecoveryEpisodeLocked() {
	cs.ingestRecoveryEpoch++
	if cs.ingestRecoveryCancel != nil {
		cs.ingestRecoveryCancel()
	}
	cs.ingestRecoveryCtx, cs.ingestRecoveryCancel = nil, nil
	cs.stopIngestRecoveryLocked()
}

func (cs *CaptureSession) beginIngestRecoveryEpisodeLocked() {
	cs.retireIngestRecoveryEpisodeLocked()
	parent := cs.ingestBindingCtx
	if parent == nil {
		parent = context.Background()
	}
	cs.ingestRecoveryCtx, cs.ingestRecoveryCancel = context.WithCancel(parent)
}

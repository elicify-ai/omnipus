package browser

import "time"

// Caller holds cs.mu after validating the ordinary request's original frame
// and binding. Replace queued automatic work without resetting its budget or
// announcing recovery before matching media arrives.
func (cs *CaptureSession) noteExplicitRecaptureIssuedLocked(window time.Duration) {
	active := cs.ingestRecoveryCtx != nil && cs.ingestRecoveryCtx.Err() == nil
	if active {
		if cs.ingestRecoveryGaveUp {
			cs.retireIngestRecoveryEpisodeLocked()
		} else {
			cs.beginIngestRecoveryEpisodeLocked()
		}
	}
	cs.noteRecaptureIssuedLocked(window)
	if active && !cs.ingestRecoveryGaveUp {
		cs.armIngestRecoveryLocked(window)
	}
}

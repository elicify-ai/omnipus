package browser

// IsCurrentVideoHealthEvent validates an original claim before publication.
// Callbacks and transport writes must remain outside cs.mu; the writer retains
// this validation through its own final admission rather than caching a bool.
func (cs *CaptureSession) IsCurrentVideoHealthEvent(event VideoHealthEvent) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return !cs.stopped && !cs.videoHealthExhausted && event.Version != 0 && event.Version == cs.videoHealthVersion && sameCaptureFrameIdentity(event.Frame, cs.frameStateLocked())
}

// Caller holds cs.mu. Exhaustion cannot wrap and reauthorize an old claim.
func (cs *CaptureSession) nextVideoHealthVersionLocked() uint64 {
	if cs.videoHealthExhausted || cs.videoHealthVersion == ^uint64(0) {
		cs.videoHealthExhausted = true
		return 0
	}
	cs.videoHealthVersion++
	return cs.videoHealthVersion
}

package browser

import "time"

// CaptureHealthObservation is an internal snapshot of independently measured
// capture stages. Counter presence is explicit: zero is a valid measurement.
type CaptureHealthObservation struct { // not-wire-format: gateway maps the generated contract into this snapshot.
	BindingEpoch      uint64 // Assigned by the server; never trusted from an encoder payload.
	Generation        int64
	TrackState        string
	TrackMuted        bool
	PeerState         string
	SourceFrames      int64
	HasSourceFrames   bool
	EncodedFrames     int64
	HasEncodedFrames  bool
	PacketsSent       int64
	HasPacketsSent    bool
	SampleTimestampMS float64
	ObservedAt        time.Time
}

// RecordIngestHeartbeat updates liveness and optional stage evidence together,
// only while the sender still owns the authenticated ingest binding.
func (cs *CaptureSession) RecordIngestHeartbeat(epoch uint64, sample *CaptureHealthObservation) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.stopped || epoch == 0 || epoch != cs.ingestEpoch || cs.ingestSend == nil || (cs.ingestBindingCtx != nil && cs.ingestBindingCtx.Err() != nil) {
		return false
	}
	now := time.Now()
	cs.lastPingAt = now
	if sample != nil {
		cs.captureHealth = *sample
		cs.captureHealth.BindingEpoch = epoch
		cs.captureHealth.ObservedAt = now
	}
	return true
}

func (cs *CaptureSession) RecordCaptureHealth(sample CaptureHealthObservation) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	sample.ObservedAt = time.Now()
	cs.captureHealth = sample
}

func (cs *CaptureSession) CaptureHealth() CaptureHealthObservation {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.captureHealth
}

// ReportCaptureFailure reuses the bounded recovery policy without destroying
// healthy viewer connections. The observer reports attempts and exhaustion.
func (cs *CaptureSession) ReportCaptureFailure() {
	cs.onIngestLost()
}

func (cs *CaptureSession) ReportCaptureFailureForObservation(sample CaptureHealthObservation) bool {
	return cs.reportIngestLoss(&sample)
}

// RecordVideoProgress completes a recovery when a reused ingest connection
// resumes forwarding without producing another track-arrival callback.
func (cs *CaptureSession) RecordVideoProgress() {
	cs.recordIngestVideoLive(true)
}

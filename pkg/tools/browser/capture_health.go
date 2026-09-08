package browser

import "time"

// CaptureHealthObservation is an internal snapshot of independently measured
// capture stages. Counter presence is explicit: zero is a valid measurement.
type CaptureHealthObservation struct { // not-wire-format: gateway maps the generated contract into this snapshot.
	BindingEpoch      uint64 // Assigned by the server; never trusted from an encoder payload.
	CaptureGeneration uint64 // Server-assigned frame generation, separate from the local attempt.
	TargetID          string
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

// RecordIngestHeartbeat admits liveness only from the current authenticated
// binding. Its return value reports socket admission; frame evidence additionally
// requires the sampled peer to match the current measured capture frame.
func (cs *CaptureSession) RecordIngestHeartbeat(epoch uint64, sample *CaptureHealthObservation) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.stopped || epoch == 0 || epoch != cs.ingestEpoch || cs.ingestSend == nil || (cs.ingestBindingCtx != nil && cs.ingestBindingCtx.Err() != nil) {
		return false
	}
	now := time.Now()
	cs.lastPingAt = now
	if sample != nil && cs.healthMatchesFrameLocked(*sample) {
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

// RecordVideoProgress rechecks actual matching ingress receipt, including a
// finite packet observed before the watchdog clears an earlier stage failure.
func (cs *CaptureSession) RecordVideoProgress() {
	cs.recordIngestVideoLive(true)
}

// Caller holds cs.mu. Legacy unbound test adapters have no frame identity;
// authenticated context-bound encoders must name their actual sampled frame.
func (cs *CaptureSession) healthMatchesFrameLocked(sample CaptureHealthObservation) bool {
	if !cs.ingestContextBound {
		return true
	}
	frame := cs.frames.snapshot()
	return cs.ingestBindingCtx != nil && cs.ingestBindingCtx.Err() == nil && sample.CaptureGeneration != 0 && sample.CaptureGeneration == frame.Generation &&
		sample.TargetID != "" && sample.TargetID == frame.Geometry.TargetID &&
		frame.Geometry.Width > 0 && frame.Geometry.Height > 0
}

// StopIfIngestHeartbeatStale atomically claims shutdown only while the sampled
// binding and heartbeat are still current. Shutdown I/O occurs outside cs.mu.
func (cs *CaptureSession) StopIfIngestHeartbeatStale(epoch uint64, sampled, now time.Time, staleAfter time.Duration) bool {
	return cs.stopWhen(func() bool {
		return epoch == cs.ingestEpoch && (!cs.ingestContextBound || epoch != 0) && !sampled.IsZero() &&
			cs.lastPingAt.Equal(sampled) && now.Sub(sampled) > staleAfter
	})
}

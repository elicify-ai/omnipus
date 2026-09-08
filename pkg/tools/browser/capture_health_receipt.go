package browser

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// CaptureWatchdogSnapshot keeps socket and frame evidence at one claim point.
type CaptureWatchdogSnapshot struct { // not-wire-format: internal watchdog observation.
	BindingEpoch   uint64
	LastPingAt     time.Time
	ViewerCount    int
	Health         CaptureHealthObservation
	VideoReceipt   webrtc.VideoReceipt
	ReceiptCurrent bool
}

func (cs *CaptureSession) WatchdogSnapshot() CaptureWatchdogSnapshot {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	snapshot := CaptureWatchdogSnapshot{BindingEpoch: cs.ingestEpoch, LastPingAt: cs.lastPingAt, ViewerCount: len(cs.viewers), Health: cs.captureHealth}
	stats := cs.relay.Stats()
	snapshot.VideoReceipt = stats.VideoReceipt
	if cs.ingestContextBound {
		snapshot.ReceiptCurrent = cs.receiptMatchesFrameLocked(stats.VideoReceipt)
		if !cs.healthMatchesFrameLocked(snapshot.Health) {
			snapshot.Health = CaptureHealthObservation{}
		}
	} else {
		// Older relay/test implementations expose only successful-forward counts.
		snapshot.VideoReceipt.Serial = uint64(max(int64(0), stats.VideoPackets))
		snapshot.ReceiptCurrent = snapshot.VideoReceipt.Serial > 0
	}
	if cs.documentPendingLocked() {
		snapshot.Health = CaptureHealthObservation{}
		snapshot.ReceiptCurrent = false
	}
	return snapshot
}

func (cs *CaptureSession) receiptMatchesFrameLocked(receipt webrtc.VideoReceipt) bool {
	frame := cs.frames.snapshot()
	return !cs.stopped && !cs.documentPendingLocked() && cs.ingestBindingCtx != nil && cs.ingestBindingCtx.Err() == nil && cs.ingestSend != nil &&
		receipt.Serial > 0 && receipt.BindingToken != 0 && receipt.BindingToken == cs.ingestBindingToken &&
		receipt.Generation != 0 && receipt.Generation == frame.Generation && receipt.TargetID != "" && receipt.TargetID == frame.Geometry.TargetID &&
		frame.Geometry.Width > 0 && frame.Geometry.Height > 0
}

func (cs *CaptureSession) captureProgressSerialLocked() uint64 {
	if cs.relay == nil {
		return 0
	}
	stats := cs.relay.Stats()
	if cs.ingestContextBound {
		return stats.VideoReceipt.Serial
	}
	return uint64(max(int64(0), stats.VideoPackets))
}

// Caller holds cs.mu. Capture the baseline before the recapture command can
// replace the peer; OnTrack alone cannot consume an older packet as success.
func (cs *CaptureSession) noteRecaptureIssuedLocked(window time.Duration) {
	cs.recapturePendingUntil = time.Now().Add(window)
	if cs.ingestContextBound {
		cs.ingestRecoveryProgressBaseline = cs.captureProgressSerialLocked()
		cs.ingestVideoLive = false
	}
}

// resetCaptureHealthForFrameLocked retires frame-specific recovery work.
// The transition owner calls it only on an actual generation change with cs.mu held.
func (cs *CaptureSession) resetCaptureHealthForFrameLocked() {
	cs.nextVideoHealthVersionLocked()
	cs.retireIngestRecoveryEpisodeLocked()
	cs.captureHealth = CaptureHealthObservation{}
	cs.ingestRecoveryAttempts = 0
	cs.ingestRecoveryGaveUp = false
	cs.ingestVideoLive = false
	cs.noteRecaptureIssuedLocked(ingestRecoverySettle)
}

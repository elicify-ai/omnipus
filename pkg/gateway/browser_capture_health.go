package gateway

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func recordCaptureHealth(cs *browser.CaptureSession, epoch uint64, frame generated.BrowserCaptureControlFrame) bool {
	h := frame.CaptureHealth
	if h == nil {
		return cs.RecordIngestHeartbeat(epoch, nil)
	}
	s := browser.CaptureHealthObservation{Generation: int64(h.Generation), TrackState: h.TrackState, TrackMuted: h.TrackMuted, PeerState: h.PeerState}
	if h.SourceFrames != nil {
		s.SourceFrames = int64(*h.SourceFrames)
		s.HasSourceFrames = true
	}
	if h.EncodedFrames != nil {
		s.EncodedFrames = int64(*h.EncodedFrames)
		s.HasEncodedFrames = true
	}
	if h.PacketsSent != nil {
		s.PacketsSent = int64(*h.PacketsSent)
		s.HasPacketsSent = true
	}
	if h.SampleTimestampMs != nil {
		s.SampleTimestampMS = *h.SampleTimestampMs
	}
	return cs.RecordIngestHeartbeat(epoch, &s)
}

// captureStageFailure requires positive failure evidence. A live, muted or
// unchanged source is ambiguous and must not be restarted for silence alone.
func captureStageFailure(previous, current browser.CaptureHealthObservation, now time.Time, staleAfter time.Duration) string {
	if current.ObservedAt.IsZero() || now.Sub(current.ObservedAt) > staleAfter {
		return ""
	}
	if current.TrackState == "ended" || current.TrackState == "absent" {
		return "capture source is unavailable"
	}
	if current.PeerState == "failed" || current.PeerState == "closed" {
		return "encoder media connection failed"
	}
	if current.TrackMuted {
		return ""
	}
	if previous.Generation != current.Generation || current.SampleTimestampMS <= previous.SampleTimestampMS {
		return ""
	}
	if current.HasSourceFrames && previous.HasSourceFrames && current.SourceFrames > previous.SourceFrames && current.HasEncodedFrames && previous.HasEncodedFrames && current.EncodedFrames == previous.EncodedFrames {
		return "source frames advanced but encoding stopped"
	}
	if current.HasEncodedFrames && previous.HasEncodedFrames && current.EncodedFrames > previous.EncodedFrames && current.HasPacketsSent && previous.HasPacketsSent && current.PacketsSent == previous.PacketsSent {
		return "encoded frames advanced but sending stopped"
	}
	if current.HasPacketsSent && previous.HasPacketsSent && current.PacketsSent > previous.PacketsSent {
		return "encoder sent packets but relay delivery stopped"
	}
	return ""
}

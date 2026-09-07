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
	if frame.CaptureGeneration != nil && *frame.CaptureGeneration > 0 {
		s.CaptureGeneration = uint64(*frame.CaptureGeneration)
	}
	if frame.TargetId != nil {
		s.TargetID = *frame.TargetId
	}
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

// captureHealthTracker is scoped to one watchdog goroutine. A finite repaint
// remains unresolved until its downstream stage progresses; another repaint
// is not required to keep fresh failure evidence alive.
type captureHealthTracker struct { // not-wire-format: internal watchdog state.
	previous browser.CaptureHealthObservation
	failure  string
}

func (t *captureHealthTracker) observe(sample browser.CaptureHealthObservation, now time.Time, staleAfter time.Duration) string {
	if sample.ObservedAt.IsZero() || now.Sub(sample.ObservedAt) > staleAfter {
		*t = captureHealthTracker{}
		return ""
	}
	previous := t.previous
	t.previous = sample
	// Track/peer terminal states are independently measured, including when
	// counters are absent or the source is muted.
	if failure := captureStageFailure(browser.CaptureHealthObservation{}, sample, now, staleAfter); failure != "" {
		t.failure = failure
		return failure
	}
	if sample.TrackMuted {
		*t = captureHealthTracker{}
		return ""
	}
	if previous.BindingEpoch != sample.BindingEpoch || previous.CaptureGeneration != sample.CaptureGeneration || previous.TargetID != sample.TargetID || previous.Generation != sample.Generation ||
		(previous.HasSourceFrames && sample.HasSourceFrames && sample.SourceFrames < previous.SourceFrames) ||
		(previous.HasEncodedFrames && sample.HasEncodedFrames && sample.EncodedFrames < previous.EncodedFrames) ||
		(previous.HasPacketsSent && sample.HasPacketsSent && sample.PacketsSent < previous.PacketsSent) {
		t.failure = ""
		previous = browser.CaptureHealthObservation{}
	}
	// Polling one snapshot repeatedly is expected. A newly received but
	// nonadvancing stats timestamp cannot refresh old counter evidence.
	if !previous.ObservedAt.IsZero() && sample.ObservedAt != previous.ObservedAt && sample.SampleTimestampMS <= previous.SampleTimestampMS {
		*t = captureHealthTracker{}
		return ""
	}
	switch t.failure {
	case "source frames advanced but encoding stopped":
		if !sample.HasSourceFrames || !sample.HasEncodedFrames {
			*t = captureHealthTracker{}
			return ""
		}
		if sample.EncodedFrames > previous.EncodedFrames {
			t.failure = ""
		}
	case "encoded frames advanced but sending stopped":
		if !sample.HasEncodedFrames || !sample.HasPacketsSent {
			*t = captureHealthTracker{}
			return ""
		}
		if sample.PacketsSent > previous.PacketsSent {
			t.failure = ""
		}
	case "encoder sent packets but relay delivery stopped":
		if !sample.HasPacketsSent {
			*t = captureHealthTracker{}
			return ""
		}
	default:
		// A fresh nonterminal state retires an earlier explicit track/peer
		// failure, but may provide new counter-based evidence below.
		t.failure = ""
	}
	if t.failure == "" {
		t.failure = captureStageFailure(previous, sample, now, staleAfter)
	}
	return t.failure
}

// noteRelayProgress reports receipt at the relay, not viewer presentation.
// Return the updated verdict so a caller cannot reuse a stale local string.
func (t *captureHealthTracker) noteRelayProgress() string {
	if t.failure == "encoder sent packets but relay delivery stopped" {
		t.failure = ""
	}
	return t.failure
}

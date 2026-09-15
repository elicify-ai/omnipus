package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/require"
)

func healthTrackerSample(at time.Time) browser.CaptureHealthObservation {
	return browser.CaptureHealthObservation{BindingEpoch: 1, Generation: 1, TrackState: "live", PeerState: "connected", ObservedAt: at, SampleTimestampMS: 1000, SourceFrames: 20, HasSourceFrames: true, EncodedFrames: 20, HasEncodedFrames: true, PacketsSent: 30, HasPacketsSent: true}
}

func nextHealthTrackerSample(sample browser.CaptureHealthObservation, after time.Duration) browser.CaptureHealthObservation {
	sample.ObservedAt = sample.ObservedAt.Add(after)
	sample.SampleTimestampMS += float64(after / time.Millisecond)
	return sample
}

func TestCaptureHealthTrackerRetainsFiniteRepaintFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		advance func(*browser.CaptureHealthObservation)
		want    string
	}{
		{"encoding", func(h *browser.CaptureHealthObservation) { h.SourceFrames++ }, "source frames advanced but encoding stopped"},
		{"sending", func(h *browser.CaptureHealthObservation) { h.SourceFrames++; h.EncodedFrames++ }, "encoded frames advanced but sending stopped"},
		{"relay", func(h *browser.CaptureHealthObservation) { h.SourceFrames++; h.EncodedFrames++; h.PacketsSent++ }, "encoder sent packets but relay delivery stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracker captureHealthTracker
			sample := healthTrackerSample(time.Unix(1000, 0))
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, 40*time.Second))
			sample = nextHealthTrackerSample(sample, 15*time.Second)
			tc.advance(&sample)
			require.Equal(t, tc.want, tracker.observe(sample, sample.ObservedAt, 40*time.Second))
			// Four fresh idle samples span the production 60-second debounce.
			for tick := 1; tick <= 4; tick++ {
				sample = nextHealthTrackerSample(sample, 15*time.Second)
				require.Equal(t, tc.want, tracker.observe(sample, sample.ObservedAt, 40*time.Second), "fresh idle sample %d", tick)
			}
		})
	}
}

func TestCaptureHealthTrackerClearsOnlyCorrespondingDownstreamProgress(t *testing.T) {
	var tracker captureHealthTracker
	sample := healthTrackerSample(time.Unix(1000, 0))
	sample.HasPacketsSent = false // Packet telemetry is not needed to prove encoding.
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
	sample = nextHealthTrackerSample(sample, time.Second)
	sample.SourceFrames++
	require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
	require.Equal(t, "source frames advanced but encoding stopped", tracker.noteRelayProgress(), "unrelated delivery cannot prove this pending frame encoded")
	sample = nextHealthTrackerSample(sample, time.Second)
	sample.EncodedFrames++
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
	sample = nextHealthTrackerSample(sample, time.Second)
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
}

func TestCaptureHealthTrackerSendingCatchupThenRelayDelivery(t *testing.T) {
	var tracker captureHealthTracker
	sample := healthTrackerSample(time.Unix(1000, 0))
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
	sample = nextHealthTrackerSample(sample, time.Second)
	sample.SourceFrames++
	sample.EncodedFrames++
	require.Equal(t, "encoded frames advanced but sending stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
	sample = nextHealthTrackerSample(sample, time.Second)
	sample.PacketsSent++
	require.Equal(t, "encoder sent packets but relay delivery stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
	require.Equal(t, "", tracker.noteRelayProgress(), "current tick must not keep the old relay verdict")
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute), "polling the acknowledged sample cannot recreate its failure")
	sample = nextHealthTrackerSample(sample, time.Second)
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
}

func TestCaptureHealthTrackerResetsInvalidCounterEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*browser.CaptureHealthObservation)
	}{
		{"generation", func(h *browser.CaptureHealthObservation) { h.Generation++ }},
		{"binding with same generation", func(h *browser.CaptureHealthObservation) { h.BindingEpoch++; h.SourceFrames += 10 }},
		{"source rollback", func(h *browser.CaptureHealthObservation) { h.SourceFrames = 0 }},
		{"encoded rollback", func(h *browser.CaptureHealthObservation) { h.EncodedFrames = 0 }},
		{"packets rollback", func(h *browser.CaptureHealthObservation) { h.PacketsSent = 0 }},
		{"source missing", func(h *browser.CaptureHealthObservation) { h.HasSourceFrames = false }},
		{"encoding missing", func(h *browser.CaptureHealthObservation) { h.HasEncodedFrames = false }},
		{"muted", func(h *browser.CaptureHealthObservation) { h.TrackMuted = true }},
		{"sample timestamp repeated", func(h *browser.CaptureHealthObservation) { h.SampleTimestampMS = 2000 }},
		{"sample timestamp regressed", func(h *browser.CaptureHealthObservation) { h.SampleTimestampMS = 1999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracker captureHealthTracker
			sample := healthTrackerSample(time.Unix(1000, 0))
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
			sample = nextHealthTrackerSample(sample, time.Second)
			sample.SourceFrames++
			require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
			sample = nextHealthTrackerSample(sample, time.Second)
			tc.change(&sample)
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
			sample = nextHealthTrackerSample(sample, time.Second)
			sample.TrackMuted = false
			sample.HasSourceFrames = true
			sample.HasEncodedFrames = true
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute), "old evidence must not return after telemetry resumes")
			sample = nextHealthTrackerSample(sample, time.Second)
			sample.SourceFrames++
			require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute), "a reset must not disable detection of a new failed repaint")
		})
	}
}

func TestCaptureHealthTrackerStaleBoundaryAndMissingObservation(t *testing.T) {
	for _, age := range []time.Duration{40*time.Second - time.Nanosecond, 40 * time.Second, 40*time.Second + time.Nanosecond} {
		t.Run(age.String(), func(t *testing.T) {
			var tracker captureHealthTracker
			sample := healthTrackerSample(time.Unix(1000, 0))
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, 40*time.Second))
			sample = nextHealthTrackerSample(sample, time.Second)
			sample.SourceFrames++
			require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, 40*time.Second))
			want := "source frames advanced but encoding stopped"
			if age > 40*time.Second {
				want = ""
			}
			require.Equal(t, want, tracker.observe(sample, sample.ObservedAt.Add(age), 40*time.Second))
			require.Equal(t, "", tracker.observe(browser.CaptureHealthObservation{}, sample.ObservedAt.Add(age), 40*time.Second))
		})
	}
}

func TestCaptureHealthTrackerExplicitFailureDoesNotRequireCounters(t *testing.T) {
	for _, tc := range []struct{ track, peer, want string }{
		{"ended", "connected", "capture source is unavailable"},
		{"absent", "connected", "capture source is unavailable"},
		{"live", "failed", "encoder media connection failed"},
		{"live", "closed", "encoder media connection failed"},
	} {
		t.Run(tc.track+"/"+tc.peer, func(t *testing.T) {
			var tracker captureHealthTracker
			now := time.Unix(1000, 0)
			sample := browser.CaptureHealthObservation{BindingEpoch: 1, Generation: 1, TrackState: tc.track, PeerState: tc.peer, TrackMuted: true, ObservedAt: now}
			require.Equal(t, tc.want, tracker.observe(sample, now, time.Minute))
		})
	}
}

func TestCaptureHealthTrackerZeroCountersRemainMeasured(t *testing.T) {
	var tracker captureHealthTracker
	sample := healthTrackerSample(time.Unix(1000, 0))
	sample.SourceFrames = 0
	sample.EncodedFrames = 0
	sample.PacketsSent = 0
	require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
	sample = nextHealthTrackerSample(sample, time.Second)
	sample.SourceFrames = 1
	require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
	sample = nextHealthTrackerSample(sample, time.Second)
	require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
}

func TestCaptureHealthTrackerDiscardsIncompleteSendingAndRelayEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		advance func(*browser.CaptureHealthObservation)
		remove  func(*browser.CaptureHealthObservation)
		want    string
	}{
		{"sending without encoded counter", func(h *browser.CaptureHealthObservation) { h.EncodedFrames++ }, func(h *browser.CaptureHealthObservation) { h.HasEncodedFrames = false }, "encoded frames advanced but sending stopped"},
		{"sending without packet counter", func(h *browser.CaptureHealthObservation) { h.EncodedFrames++ }, func(h *browser.CaptureHealthObservation) { h.HasPacketsSent = false }, "encoded frames advanced but sending stopped"},
		{"relay without packet counter", func(h *browser.CaptureHealthObservation) { h.PacketsSent++ }, func(h *browser.CaptureHealthObservation) { h.HasPacketsSent = false }, "encoder sent packets but relay delivery stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracker captureHealthTracker
			sample := healthTrackerSample(time.Unix(1000, 0))
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
			sample = nextHealthTrackerSample(sample, time.Second)
			tc.advance(&sample)
			require.Equal(t, tc.want, tracker.observe(sample, sample.ObservedAt, time.Minute))
			sample = nextHealthTrackerSample(sample, time.Second)
			tc.remove(&sample)
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
			sample = nextHealthTrackerSample(sample, time.Second)
			sample.HasEncodedFrames = true
			sample.HasPacketsSent = true
			require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, time.Minute))
		})
	}
}

func TestCaptureHealthTrackerNeverInventsFailureOnFreshIdleSamples(t *testing.T) {
	var tracker captureHealthTracker
	sample := healthTrackerSample(time.Unix(1000, 0))
	for i := 0; i < 6; i++ {
		require.Equal(t, "", tracker.observe(sample, sample.ObservedAt, 40*time.Second))
		sample = nextHealthTrackerSample(sample, 15*time.Second)
	}
}

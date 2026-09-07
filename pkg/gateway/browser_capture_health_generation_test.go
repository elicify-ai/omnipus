package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/require"
)

func TestCaptureHealthTrackerResetsAcrossServerFrameIdentity(t *testing.T) {
	for _, changed := range []string{"generation", "target"} {
		t.Run(changed, func(t *testing.T) {
			now := time.Unix(100, 0)
			sample := browser.CaptureHealthObservation{BindingEpoch: 3, CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "live", PeerState: "connected", HasSourceFrames: true, HasEncodedFrames: true, HasPacketsSent: true, SourceFrames: 20, EncodedFrames: 20, PacketsSent: 30, SampleTimestampMS: 1, ObservedAt: now}
			var tracker captureHealthTracker
			require.Empty(t, tracker.observe(sample, now, time.Minute))
			sample.SourceFrames = 21
			sample.SampleTimestampMS = 2
			sample.ObservedAt = now.Add(time.Second)
			require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute))
			if changed == "generation" {
				sample.CaptureGeneration = 2
			} else {
				sample.TargetID = "page-b"
			}
			sample.SampleTimestampMS = 3
			sample.ObservedAt = now.Add(2 * time.Second)
			require.Empty(t, tracker.observe(sample, sample.ObservedAt, time.Minute), "new frame cannot inherit previous frame's finite repaint failure")
			sample.SourceFrames = 22
			sample.SampleTimestampMS = 4
			sample.ObservedAt = now.Add(3 * time.Second)
			require.Equal(t, "source frames advanced but encoding stopped", tracker.observe(sample, sample.ObservedAt, time.Minute), "new frame's own independently observed failure still counts")
		})
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

func TestCaptureHealthWireMappingRetainsZeroCountersAndRejectsOldBinding(t *testing.T) {
	cs := &browser.CaptureSession{}
	bind := func() uint64 {
		_, epoch := cs.BindIngest(func(string, *string, int, int, int) error { return nil }, func() {})
		return epoch
	}
	first := bind()
	var frame generated.BrowserCaptureControlFrame
	require.NoError(t, json.Unmarshal([]byte(`{"type":"browser_capture_control","action":"ping","capture_health":{"generation":3,"track_state":"live","track_muted":false,"peer_state":"connected","source_frames":0,"encoded_frames":0,"packets_sent":0,"sample_timestamp_ms":12}}`), &frame))
	require.True(t, recordCaptureHealth(cs, first, frame))
	got := cs.CaptureHealth()
	require.False(t, got.ObservedAt.IsZero())
	require.Equal(t, browser.CaptureHealthObservation{BindingEpoch: first, Generation: 3, TrackState: "live", PeerState: "connected", HasSourceFrames: true, HasEncodedFrames: true, HasPacketsSent: true, SampleTimestampMS: 12, ObservedAt: got.ObservedAt}, got)
	bind()
	ping := cs.LastPingAt()
	require.False(t, recordCaptureHealth(cs, first, frame))
	require.Equal(t, browser.CaptureHealthObservation{}, cs.CaptureHealth())
	require.Equal(t, ping, cs.LastPingAt())
}

// FR-012: unchanged packets from a healthy idle source are not evidence of
// failure. Keep both control heartbeat and stage observations fresh.
func TestBrowserWatchdogPreservesHealthyIdleCapture(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)
	relay := &fakeRelay{}
	relay.setStats(webrtc.Stats{HasVideo: true, VideoPackets: 100})
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "healthy-idle", relay, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	require.NoError(t, err)
	cs.AddViewer("idle-viewer")
	cs.RecordPing()
	cs.RecordCaptureHealth(browser.CaptureHealthObservation{Generation: 1, TrackState: "live", PeerState: "connected", SourceFrames: 20, HasSourceFrames: true, EncodedFrames: 20, HasEncodedFrames: true})
	finished := make(chan struct{})
	go func() { handler.watchEncoderLiveness(cs, "healthy-idle", time.Millisecond, time.Hour); close(finished) }()
	t.Cleanup(func() { cs.Stop(); <-finished })
	select {
	case <-cs.Done():
		t.Fatal("healthy idle capture was stopped merely because video packets did not change")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCaptureStageFailureEvidence(t *testing.T) {
	now := time.Now()
	previous := browser.CaptureHealthObservation{Generation: 1, TrackState: "live", PeerState: "connected", ObservedAt: now, SampleTimestampMS: 1, SourceFrames: 20, HasSourceFrames: true, EncodedFrames: 20, HasEncodedFrames: true, PacketsSent: 30, HasPacketsSent: true}
	for _, tc := range []struct {
		name   string
		change func(*browser.CaptureHealthObservation)
		want   string
	}{
		{"healthy idle", func(*browser.CaptureHealthObservation) {}, ""},
		{"ended track", func(h *browser.CaptureHealthObservation) { h.TrackState = "ended" }, "capture source is unavailable"},
		{"failed peer", func(h *browser.CaptureHealthObservation) { h.PeerState = "failed" }, "encoder media connection failed"},
		{"encoding stalled", func(h *browser.CaptureHealthObservation) { h.SourceFrames = 30 }, "source frames advanced but encoding stopped"},
		{"sending stalled", func(h *browser.CaptureHealthObservation) { h.SourceFrames = 30; h.EncodedFrames = 30 }, "encoded frames advanced but sending stopped"},
		{"relay stalled", func(h *browser.CaptureHealthObservation) {
			h.SourceFrames = 30
			h.EncodedFrames = 30
			h.PacketsSent = 40
		}, "encoder sent packets but relay delivery stopped"},
		{"generation reset", func(h *browser.CaptureHealthObservation) { h.Generation = 2; h.SourceFrames = 30 }, ""},
		{"stale sample", func(h *browser.CaptureHealthObservation) {
			h.ObservedAt = now.Add(-time.Minute)
			h.TrackState = "ended"
		}, ""},
		{"muted source", func(h *browser.CaptureHealthObservation) { h.TrackMuted = true; h.SourceFrames = 30 }, ""},
		{"missing counter", func(h *browser.CaptureHealthObservation) { h.SourceFrames = 30; h.HasEncodedFrames = false }, ""},
		{"repeated sample", func(h *browser.CaptureHealthObservation) { h.SampleTimestampMS = 1; h.SourceFrames = 30 }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := previous
			current.SampleTimestampMS = 2
			tc.change(&current)
			require.Equal(t, tc.want, captureStageFailure(previous, current, now, 40*time.Second))
		})
	}
}

// FR-013 requires recovery of a failed capture stage without discarding healthy
// viewer peers. Relay progress, not a new connection or track event, completes
// recovery when the original ingest connection resumes.
func TestBrowserWatchdogRecoversFailedStageAndKeepsViewers(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)
	relay := &fakeRelay{}
	relay.setStats(webrtc.Stats{HasVideo: true, VideoPackets: 100})
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "failed-stage", relay, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	require.NoError(t, err)
	cs.AddViewer("retained-viewer")
	controls := make(chan string, 16)
	_, epoch := cs.BindIngest(func(action string, _ *string, _, _, _ int) error {
		controls <- action
		return nil
	}, func() {})
	events := make(chan browser.VideoHealthEvent, 16)
	cs.SetOnVideoHealth(func(event browser.VideoHealthEvent) { events <- event })
	require.True(t, cs.RecordIngestHeartbeat(epoch, &browser.CaptureHealthObservation{Generation: 1, TrackState: "ended", PeerState: "connected"}))
	finished := make(chan struct{})
	go func() { handler.watchEncoderLiveness(cs, "failed-stage", time.Millisecond, time.Hour); close(finished) }()
	t.Cleanup(func() { cs.Stop(); <-finished })
	for _, state := range []browser.VideoHealthState{browser.VideoHealthLost, browser.VideoHealthRecovering} {
		select {
		case event := <-events:
			require.Equal(t, state, event.State)
			require.Equal(t, 1, event.Attempt)
			require.Equal(t, []string{"retained-viewer"}, event.ViewerIDs)
		case <-cs.Done():
			t.Fatal("failed stage destroyed the capture instead of recovering it")
		case <-time.After(2 * time.Second):
			t.Fatalf("failed stage never emitted %s", state)
		}
	}
	select {
	case action := <-controls:
		require.Equal(t, "recapture", action)
	case <-time.After(2 * time.Second):
		t.Fatal("failed stage did not request recapture")
	}
	require.Zero(t, relay.closeCount(), "stage recovery closed healthy viewer peers")
	require.Equal(t, []string{"retained-viewer"}, cs.ViewerIDs())
	require.True(t, cs.RecordIngestHeartbeat(epoch, &browser.CaptureHealthObservation{Generation: 2, TrackState: "live", PeerState: "connected"}))
	relay.setStats(webrtc.Stats{HasVideo: true, VideoPackets: 101})
	select {
	case event := <-events:
		require.Equal(t, browser.VideoHealthRecovered, event.State)
		require.Zero(t, event.Attempt)
	case <-time.After(2 * time.Second):
		t.Fatal("resumed packet delivery never completed same-connection recovery")
	}
	require.Zero(t, relay.closeCount(), "recovered capture closed its viewers")
}

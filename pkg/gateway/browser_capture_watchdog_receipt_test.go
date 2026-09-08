package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

// Only the external relay observation is synthetic: each observation represents
// another accepted packet, independently of whether viewer writes succeeded.
type watchdogReceiptRelay struct {
	wireIngestRelay
	reads            chan struct{}
	serial           atomic.Uint64
	advanceForwarded bool
}

func (r *watchdogReceiptRelay) Stats() webrtc.Stats {
	stats := r.fakeRelay.Stats()
	n := r.serial.Add(1)
	stats.VideoReceipt.Serial += n
	if r.advanceForwarded {
		stats.VideoPackets += int64(n)
	}
	select {
	case r.reads <- struct{}{}:
	default:
	}
	return stats
}

func TestBrowserWatchdogQualifiesReceiptProgressByStageAndFrame(t *testing.T) {
	for _, scenario := range []string{"encoding failure survives incoming old encoded packets", "failed viewer writes do not erase ingress", "old binding receipt is not current progress", "old generation receipt is not current progress"} {
		t.Run(scenario, func(t *testing.T) {
			h, _ := newBrowserWSTestHandler(t, nil)
			t.Cleanup(h.Wait)
			r := &watchdogReceiptRelay{wireIngestRelay: wireIngestRelay{token: 60, entered: make(chan wireIngestOffer, 1)}, reads: make(chan struct{}, 128), advanceForwarded: scenario != "failed viewer writes do not erase ingress"}
			receipt := webrtc.VideoReceipt{BindingToken: 71, Generation: 1, TargetID: "page-a", Serial: 10}
			r.setStats(webrtc.Stats{HasVideo: true, VideoGeneration: 1, VideoTargetID: "page-a", VideoPackets: 100, VideoReceipt: receipt})
			var starts int32
			cs, err := browser.NewCaptureSessionWithDeps(nil, "receipt-watchdog", r, fakeEncoderStarter(&starts, nil), nil)
			require.NoError(t, err)
			t.Cleanup(cs.Stop)
			_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
			require.NoError(t, err)
			controls := make(chan string, 32)
			_, epoch, err := cs.BindIngestRecaptureContext(context.Background(), func(action string, _ *string, _, _, _ int) error { controls <- action; return nil }, func(ctx context.Context, frame browser.CaptureFrameState, current func() bool) error {
				if ctx.Err() != nil || !current() {
					return context.Canceled
				}
				if frame.Generation != 1 || frame.TargetID != "page-a" || frame.Width != 800 || frame.Height != 600 {
					t.Errorf("recovery changed original measured frame: %+v", frame)
				}
				controls <- "recapture"
				return nil
			}, func() {})
			require.NoError(t, err)
			// Establish healthy current media before testing a later failure. This
			// retires the initial frame's legitimate recapture settle window.
			cs.RecordVideoProgress()
			if scenario == "old binding receipt is not current progress" {
				receipt.BindingToken = 60
			}
			if scenario == "old generation receipt is not current progress" {
				receipt.Generation = 2
			}
			r.setStats(webrtc.Stats{HasVideo: true, VideoGeneration: 1, VideoTargetID: "page-a", VideoPackets: 100, VideoReceipt: receipt})
			cs.AddViewer("retained-viewer")
			sample := browser.CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "live", PeerState: "connected", SourceFrames: 20, HasSourceFrames: true, EncodedFrames: 20, HasEncodedFrames: true, PacketsSent: 30, HasPacketsSent: true, SampleTimestampMS: 1}
			require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
			for len(r.reads) > 0 {
				<-r.reads
			}
			done := make(chan struct{})
			go func() { defer close(done); h.watchEncoderLiveness(cs, "receipt-watchdog", time.Millisecond, time.Hour) }()
			t.Cleanup(func() { cs.Stop(); <-done })
			awaitReads := func(count int) {
				for i := 0; i < count; i++ {
					select {
					case <-r.reads:
					case <-time.After(time.Second):
						t.Fatal("watchdog stopped sampling relay")
					}
				}
			}
			awaitReads(2)
			sample.SourceFrames = 21
			sample.SampleTimestampMS = 2
			if scenario != "encoding failure survives incoming old encoded packets" {
				sample.EncodedFrames = 21
				sample.PacketsSent = 31
			}
			require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
			if scenario == "failed viewer writes do not erase ingress" {
				awaitReads(20)
				select {
				case action := <-controls:
					t.Fatalf("current accepted packets triggered %s merely because viewer writes did not advance", action)
				default:
				}
			} else {
				select {
				case action := <-controls:
					require.Equal(t, "recapture", action)
				case <-time.After(time.Second):
					t.Fatal("unrelated progress masked affirmative capture failure")
				}
			}
			require.Equal(t, []string{"retained-viewer"}, cs.ViewerIDs())
			require.Zero(t, r.closeCount())
		})
	}
}

type finiteWatchdogReceiptRelay struct {
	watchdogReceiptRelay
	held atomic.Bool
}

func (r *finiteWatchdogReceiptRelay) Stats() webrtc.Stats {
	if !r.held.Load() {
		return r.watchdogReceiptRelay.Stats()
	}
	stats := r.fakeRelay.Stats()
	n := r.serial.Load()
	stats.VideoReceipt.Serial += n
	stats.VideoPackets += int64(n)
	select {
	case r.reads <- struct{}{}:
	default:
	}
	return stats
}

func TestBrowserWatchdogRetainsFiniteReceiptUntilStageClears(t *testing.T) {
	h, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(h.Wait)
	r := &finiteWatchdogReceiptRelay{watchdogReceiptRelay: watchdogReceiptRelay{wireIngestRelay: wireIngestRelay{token: 60, entered: make(chan wireIngestOffer, 1)}, reads: make(chan struct{}, 128), advanceForwarded: true}}
	r.setStats(webrtc.Stats{HasVideo: true, VideoPackets: 100, VideoReceipt: webrtc.VideoReceipt{BindingToken: 71, Generation: 1, TargetID: "page-a", Serial: 10}})
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "finite-receipt", r, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	_, epoch, err := cs.BindIngestContext(context.Background(), func(string, *string, int, int, int) error { return nil }, func() {})
	require.NoError(t, err)
	cs.AddViewer("viewer")
	events := make(chan browser.VideoHealthEvent, 128)
	cs.SetOnVideoHealth(func(event browser.VideoHealthEvent) { events <- event })
	sample := browser.CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "live", PeerState: "connected", SourceFrames: 20, HasSourceFrames: true, EncodedFrames: 20, HasEncodedFrames: true, PacketsSent: 30, HasPacketsSent: true, SampleTimestampMS: 1}
	require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
	done := make(chan struct{})
	go func() { defer close(done); h.watchEncoderLiveness(cs, "finite-receipt", time.Millisecond, time.Hour) }()
	t.Cleanup(func() { cs.Stop(); <-done })
	awaitReads := func(count int) {
		for i := 0; i < count; i++ {
			select {
			case <-r.reads:
			case <-time.After(time.Second):
				t.Fatal("watchdog stopped observing")
			}
		}
	}
	awaitReads(2)
	sample.SourceFrames = 21
	sample.SampleTimestampMS = 2
	require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
	awaitReads(20)
	// Explicitly establish a loss claim even in the baseline implementation,
	// whose old watchdog incorrectly masks encoding failures with packet totals.
	cs.ReportCaptureFailure()
	awaitReads(20)
	for len(events) > 0 {
		event := <-events
		require.NotEqual(t, browser.VideoHealthRecovered, event.State, "packets cannot reset the budget while encoding remains unresolved")
	}
	r.held.Store(true)
	sample.EncodedFrames = 21
	sample.PacketsSent = 31
	sample.SampleTimestampMS = 3
	require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.State == browser.VideoHealthRecovered {
				return
			}
		case <-deadline.C:
			t.Fatal("the retained finite packet was forgotten when stage evidence cleared")
		}
	}
}

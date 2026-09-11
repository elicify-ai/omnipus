package browser

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

func receiptHealthFixture(t *testing.T) (*CaptureSession, *adapterRelay, *healthRecorder, uint64) {
	t.Helper()
	cs, r := adapterFixture(t)
	epoch := recoveryBind(t, cs, r)
	rec := &healthRecorder{}
	r.mu.Lock()
	r.stats = webrtc.Stats{HasVideo: true, VideoGeneration: 1, VideoTargetID: "page-a", VideoPackets: 99, VideoReceipt: webrtc.VideoReceipt{BindingToken: 47, Generation: 1, TargetID: "page-a", Serial: 10}}
	r.mu.Unlock()
	cs.CommitFrameBoundary(1, "page-a", 100)
	cs.RecordVideoProgress()
	cs.SetOnVideoHealth(rec.observe)
	return cs, r, rec, epoch
}

func TestCaptureHealthReceiptRequiresCurrentPostLossPacket(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, scenario := range []string{"finite packet with failed viewer write", "pre-loss packet", "old binding", "old generation", "wrong target", "canceled socket", "unbound socket", "replacement socket", "track arrival without packet"} {
		t.Run(scenario, func(t *testing.T) {
			cs, r, rec, epoch := receiptHealthFixture(t)
			claimedFrame := cs.FrameState()
			cs.ReportCaptureFailure()
			require.Equal(t, []CaptureFrameState{claimedFrame}, r.recoveryFrames())
			r.mu.Lock()
			r.stats.VideoReceipt.Serial = 11
			r.stats.VideoPackets = 100 // tempting unrelated successful-forward total
			switch scenario {
			case "finite packet with failed viewer write":
				r.stats.VideoPackets = 99
			case "pre-loss packet", "track arrival without packet":
				r.stats.VideoReceipt.Serial = 10
			case "old binding":
				r.stats.VideoReceipt.BindingToken = 40
			case "old generation":
				r.stats.VideoReceipt.Generation = 2
			case "wrong target":
				r.stats.VideoReceipt.TargetID = "page-b"
			}
			r.mu.Unlock()
			switch scenario {
			case "canceled socket":
				cs.mu.Lock()
				cs.ingestBindingCancel()
				cs.mu.Unlock()
			case "unbound socket":
				cs.UnbindIngest(epoch)
			case "replacement socket":
				adapterBind(t, cs, context.Background())
			}
			if scenario == "track arrival without packet" {
				r.triggerIngestLive()
			} else {
				cs.RecordVideoProgress()
			}
			want := 0
			if scenario == "finite packet with failed viewer write" {
				want = 1
			}
			require.Equal(t, want, rec.count(VideoHealthRecovered), "only actual current post-loss ingress proves recovery")
			cs.mu.Lock()
			live, pending := cs.ingestVideoLive, cs.ingestRecoveryTimer != nil
			cs.mu.Unlock()
			require.Equal(t, want == 1, live)
			require.Equal(t, want == 0, pending)
		})
	}
}

func TestCaptureHealthReceiptCannotReuseConsumedOrPreRecapturePacket(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, consumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unobserved old packet", true: "already consumed packet"}[consumed], func(t *testing.T) {
			cs, r, _, _ := receiptHealthFixture(t)
			r.mu.Lock()
			r.stats.VideoReceipt.Serial = 11
			r.mu.Unlock()
			if consumed {
				cs.RecordVideoProgress()
			}
			cs.RecaptureAt(800, 600)
			r.triggerIngestLive()
			cs.mu.Lock()
			pending := cs.recapturePendingUntil
			cs.mu.Unlock()
			require.False(t, pending.IsZero(), "OnTrack before a new packet cannot clear recapture settle window")
			r.mu.Lock()
			r.stats.VideoReceipt.Serial = 12
			r.mu.Unlock()
			cs.RecordVideoProgress()
			cs.mu.Lock()
			pending = cs.recapturePendingUntil
			cs.mu.Unlock()
			require.True(t, pending.IsZero(), "one new matching packet completes the recapture")
		})
	}
}

func TestCaptureHealthReceiptSnapshotKeepsStaleSerialButDropsItsProof(t *testing.T) {
	cs, r, _, epoch := receiptHealthFixture(t)
	cs.AddViewer("viewer")
	sample := CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "live"}
	require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
	first := cs.WatchdogSnapshot()
	require.Equal(t, epoch, first.BindingEpoch)
	require.Equal(t, 1, first.ViewerCount)
	require.False(t, first.LastPingAt.IsZero())
	require.Equal(t, uint64(1), first.Health.CaptureGeneration)
	require.Equal(t, webrtc.VideoReceipt{BindingToken: 47, Generation: 1, TargetID: "page-a", Serial: 10}, first.VideoReceipt)
	require.True(t, first.ReceiptCurrent)
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	next := cs.WatchdogSnapshot()
	require.Equal(t, first.VideoReceipt, next.VideoReceipt, "global old serial remains visible for monotonic baseline tracking")
	require.False(t, next.ReceiptCurrent)
	require.Equal(t, CaptureHealthObservation{}, next.Health, "stored old-frame health must not drive the new frame watchdog")
	r.mu.Lock()
	r.stats.VideoReceipt = webrtc.VideoReceipt{BindingToken: 47, Generation: 2, TargetID: "page-b", Serial: 11}
	r.mu.Unlock()
	require.True(t, cs.WatchdogSnapshot().ReceiptCurrent)
}

func TestCaptureHealthEventPreservesClaimedFrameThroughDelayedDelivery(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, rec, _ := receiptHealthFixture(t)
	claimed := cs.FrameState()
	cs.ReportCaptureFailure()
	r.mu.Lock()
	r.stats.VideoReceipt.Serial = 11
	r.stats.VideoPackets = 100
	r.mu.Unlock()
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cs.logf = func(format string, args ...any) {
		if strings.Contains(format, "video is flowing again") {
			close(entered)
			<-resume
		}
	}
	go func() { defer close(done); cs.RecordVideoProgress() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("recovery did not reach delivery barrier")
	}
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery delivery blocked")
	}
	rec.mu.Lock()
	events := append([]VideoHealthEvent(nil), rec.events...)
	rec.mu.Unlock()
	var found bool
	for _, event := range events {
		if event.State == VideoHealthRecovered {
			found = true
			require.Equal(t, claimed, event.Frame, "delivery cannot relabel an old claim with the replacement frame")
		}
	}
	require.True(t, found)
}

func TestCaptureHealthFrameResetRetiresRecoveryButKeepsSocket(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, _, _, epoch := receiptHealthFixture(t)
	sample := CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "ended"}
	require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
	cs.ReportCaptureFailure()
	ping := cs.LastPingAt()
	cs.mu.Lock()
	oldEpoch := cs.ingestRecoveryEpoch
	cs.mu.Unlock()
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	cs.mu.Lock()
	attempts, pending, nextEpoch := cs.ingestRecoveryAttempts, cs.ingestRecoveryTimer != nil, cs.ingestRecoveryEpoch
	cs.mu.Unlock()
	require.Zero(t, attempts)
	require.False(t, pending)
	require.NotEqual(t, oldEpoch, nextEpoch)
	require.Equal(t, CaptureHealthObservation{}, cs.CaptureHealth())
	require.Equal(t, ping, cs.LastPingAt(), "frame reset preserves original socket heartbeat")
	require.Equal(t, epoch, cs.WatchdogSnapshot().BindingEpoch)
	require.Equal(t, uint64(10), cs.WatchdogSnapshot().VideoReceipt.Serial)
}

func TestCaptureHealthFrameResetInvalidatesAlreadyRunningLoss(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, _, _ := receiptHealthFixture(t)
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cs.SetOnVideoHealth(func(event VideoHealthEvent) {
		if event.State == VideoHealthLost {
			close(entered)
			<-resume
		}
	})
	go func() { defer close(done); cs.ReportCaptureFailure() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("loss did not reach observer barrier")
	}
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old loss did not finish")
	}
	require.Zero(t, r.recaptureCount(), "already-running old-frame continuation cannot request new-frame recapture")
}

func TestCaptureHealthFrameResetRejectsRetainedTimerEpoch(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, _, _ := receiptHealthFixture(t)
	cs.mu.Lock()
	retainedEpoch := cs.ingestRecoveryEpoch
	cs.mu.Unlock()
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	cs.mu.Lock()
	currentEpoch := cs.ingestRecoveryEpoch
	cs.mu.Unlock()
	cs.runIngestRecoveryForEpoch(retainedEpoch)
	require.Zero(t, r.recaptureCount(), "a callback already retained by a timer cannot recover a replacement frame")
	cs.runIngestRecoveryForEpoch(currentEpoch)
	require.Equal(t, 1, r.recaptureCount(), "current frame's own recovery continuation remains functional")
}

func TestCaptureHealthFrameResetInvalidatesAlreadyRunningAttempt(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, _, _ := receiptHealthFixture(t)
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cs.SetOnVideoHealth(func(event VideoHealthEvent) {
		if event.State == VideoHealthRecovering {
			close(entered)
			<-resume
		}
	})
	go func() { defer close(done); cs.ReportCaptureFailure() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("attempt did not reach observer barrier")
	}
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old attempt did not finish")
	}
	require.Zero(t, r.recaptureCount(), "reset during attempt delivery must retire its later recapture dispatch")
}

func TestCaptureHealthConcurrentLossClaimsShareOneEpisode(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, _, _ := receiptHealthFixture(t)
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var lost int
	var lossMu sync.Mutex
	cs.SetOnVideoHealth(func(event VideoHealthEvent) {
		if event.State != VideoHealthLost {
			return
		}
		lossMu.Lock()
		lost++
		first := lost == 1
		lossMu.Unlock()
		if first {
			close(entered)
			<-resume
		}
	})
	go func() { defer close(done); cs.ReportCaptureFailure() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("first loss did not reach observer barrier")
	}
	cs.ReportCaptureFailure()
	// Retain cleanup for the baseline bug, which overwrites the first timer handle.
	cs.mu.Lock()
	retained := cs.ingestRecoveryTimer
	cs.mu.Unlock()
	if retained != nil {
		t.Cleanup(func() { retained.Stop() })
	}
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first loss did not finish")
	}
	require.Equal(t, 1, r.recaptureCount(), "concurrent notifications must claim one recovery episode")
	lossMu.Lock()
	defer lossMu.Unlock()
	require.Equal(t, 1, lost)
}

func TestCaptureHealthRecoveryCancelsQueuedEpisode(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, finished := range []string{"matching receipt", "frame reset", "stop", "binding replacement"} {
		t.Run(finished, func(t *testing.T) {
			cs, r, _, _ := receiptHealthFixture(t)
			cs.ReportCaptureFailure()
			cs.mu.Lock()
			episode, binding := cs.ingestRecoveryCtx, cs.ingestBindingCtx
			cs.mu.Unlock()
			require.NotNil(t, episode, "loss claim owns a cancellation lifetime before callbacks/queueing")
			require.NoError(t, episode.Err())
			switch finished {
			case "matching receipt":
				r.mu.Lock()
				r.stats.VideoReceipt.Serial = 11
				r.mu.Unlock()
				cs.RecordVideoProgress()
			case "frame reset":
				_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
				require.NoError(t, err)
			case "stop":
				cs.Stop()
			case "binding replacement":
				adapterBind(t, cs, context.Background())
				cs.mu.Lock()
				currentBinding := cs.ingestBindingCtx
				cs.mu.Unlock()
				require.NoError(t, currentBinding.Err(), "old episode cannot cancel the replacement binding")
			}
			select {
			case <-episode.Done():
			default:
				t.Fatal("retired recovery left its queued commands alive")
			}
			if finished != "stop" && finished != "binding replacement" {
				require.NoError(t, binding.Err(), "episode cancellation must not replace/cancel original socket lifetime")
			}
		})
	}
}

func TestCaptureHealthRecoveryBeforeFirstAttemptRetiresContinuation(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, rec, _ := receiptHealthFixture(t)
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cs.SetOnVideoHealth(func(event VideoHealthEvent) {
		rec.observe(event)
		if event.State == VideoHealthLost {
			close(entered)
			<-resume
		}
	})
	go func() { defer close(done); cs.ReportCaptureFailure() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("loss did not reach observer barrier")
	}
	r.mu.Lock()
	r.stats.VideoReceipt.Serial = 11 // One packet after fixture's loss baseline 10.
	r.mu.Unlock()
	cs.RecordVideoProgress()
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("resolved loss continuation did not finish")
	}
	require.Equal(t, 1, rec.count(VideoHealthRecovered), "recovery does not require an automatic attempt to have started")
	require.Zero(t, r.recaptureCount(), "recovered episode cannot restart after its delayed Lost observer returns")
}

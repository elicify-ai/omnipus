package browser

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureHealthEventVersionRejectsDelayedSameFrameLoss(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, _, _ := receiptHealthFixture(t)
	entered, resume, done := make(chan VideoHealthEvent, 1), make(chan struct{}), make(chan struct{})
	var recovered VideoHealthEvent
	cs.SetOnVideoHealth(func(event VideoHealthEvent) {
		if event.State == VideoHealthLost {
			entered <- event
			<-resume
		}
		if event.State == VideoHealthRecovered {
			recovered = event
		}
	})
	go func() { defer close(done); cs.ReportCaptureFailure() }()
	var lost VideoHealthEvent
	select {
	case lost = <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("lost event did not reach barrier")
	}
	r.mu.Lock()
	r.stats.VideoReceipt.Serial = 11
	r.mu.Unlock()
	cs.RecordVideoProgress()
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loss continuation did not finish")
	}
	require.False(t, cs.IsCurrentVideoHealthEvent(lost), "delayed same-frame loss cannot overwrite recovery")
	require.True(t, cs.IsCurrentVideoHealthEvent(recovered), "newest recovery remains publishable after episode cancellation")
	require.NotZero(t, lost.Version)
	require.Greater(t, recovered.Version, lost.Version)
}

func TestCaptureHealthEventVersionRejectsResetStopAndExhaustion(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, retire := range []string{"reset", "stop", "exhaustion"} {
		t.Run(retire, func(t *testing.T) {
			cs, _, _, _ := receiptHealthFixture(t)
			var claimed VideoHealthEvent
			cs.SetOnVideoHealth(func(event VideoHealthEvent) { claimed = event })
			if retire == "exhaustion" {
				cs.mu.Lock()
				cs.videoHealthVersion = math.MaxUint64
				cs.mu.Unlock()
			}
			cs.ReportCaptureFailure()
			switch retire {
			case "reset":
				cs.mu.Lock()
				cs.resetCaptureHealthForFrameLocked()
				cs.mu.Unlock()
			case "stop":
				cs.Stop()
			}
			require.False(t, cs.IsCurrentVideoHealthEvent(claimed), "retired/exhausted claim must not authorize publication")
		})
	}
}

func TestCaptureHealthExhaustionCancelsQueuedEpisode(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, _, rec, _ := receiptHealthFixture(t)
	cs.ReportCaptureFailure()
	cs.mu.Lock()
	episode := cs.ingestRecoveryCtx
	cs.stopIngestRecoveryLocked()
	cs.ingestRecoveryAttempts = maxIngestRecoveryAttempts
	cs.mu.Unlock()
	cs.runIngestRecovery()
	require.Equal(t, 1, rec.count(VideoHealthUnrecoverable))
	select {
	case <-episode.Done():
	default:
		t.Fatal("exhausted episode left an earlier queued automatic recapture alive")
	}
}

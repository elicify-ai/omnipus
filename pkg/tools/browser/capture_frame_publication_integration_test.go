package browser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureFrameTransitionPublishesActualBoundaries(t *testing.T) {
	cs, _ := adapterFixture(t)
	var events []VideoHealthEvent
	cs.SetOnVideoHealth(func(event VideoHealthEvent) {
		if !cs.mu.TryLock() {
			t.Error("frame observer ran under the capture lock")
			return
		}
		cs.mu.Unlock()
		events = append(events, event)
	})
	frame, err := cs.BeginFrameTransition("page-b", 756, 413, 1.25)
	require.NoError(t, err)
	require.Len(t, events, 1, "actual transition must publish without a helper call")
	require.Equal(t, VideoHealthTransitioning, events[0].State)
	require.Equal(t, frame, events[0].Frame)
	require.NotZero(t, events[0].Version)
	_, err = cs.BeginFrameTransition("page-b", 756, 413, 1.25)
	require.NoError(t, err)
	require.Len(t, events, 1, "identical geometry is not another transition")
	require.True(t, cs.CommitFrameBoundary(frame.Generation, "page-b", 0))
	require.Len(t, events, 2, "actual boundary must publish decoder readiness")
	frame.Ready = true
	require.Equal(t, frame, events[1].Frame)
	require.Equal(t, VideoHealthRecovered, events[1].State)
	require.Greater(t, events[1].Version, events[0].Version)
	current, ok := cs.CurrentVideoHealthEvent()
	require.True(t, ok)
	require.Equal(t, events[1], current)
}

func TestCaptureFrameTransitionRetiresOldRecovery(t *testing.T) {
	cs, _ := adapterFixture(t)
	cs.mu.Lock()
	episode, cancel := context.WithCancel(context.Background())
	cs.ingestRecoveryCtx, cs.ingestRecoveryCancel = episode, cancel
	cs.ingestRecoveryAttempts, cs.ingestRecoveryGaveUp = 2, true
	cs.captureHealth = CaptureHealthObservation{Generation: 1, TrackState: "ended"}
	old := cs.videoHealthEventLocked(VideoHealthUnrecoverable, 2, "old picture failed")
	cs.mu.Unlock()
	defer cancel()
	frame, err := cs.BeginFrameTransition("page-b", 640, 480, 1)
	require.NoError(t, err)
	require.ErrorIs(t, episode.Err(), context.Canceled, "new target must retire old recovery immediately")
	cs.mu.Lock()
	attempts, gaveUp, health := cs.ingestRecoveryAttempts, cs.ingestRecoveryGaveUp, cs.captureHealth
	cs.mu.Unlock()
	require.Zero(t, attempts)
	require.False(t, gaveUp)
	require.Equal(t, CaptureHealthObservation{}, health)
	current, ok := cs.CurrentVideoHealthEvent()
	require.True(t, ok)
	require.Equal(t, frame, current.Frame)
	require.Equal(t, VideoHealthTransitioning, current.State)
	require.Empty(t, current.Detail)
	require.Greater(t, current.Version, old.Version)
}

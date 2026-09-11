package browser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureRecaptureExplicitSupersedesAutomaticEpisode(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		name := "automatic preserves original episode"
		if explicit {
			name = "explicit retires queued retry"
		}
		t.Run(name, func(t *testing.T) {
			cs, _ := adapterFixture(t)
			frame := cs.FrameState()
			_, _, err := cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil },
				func(context.Context, CaptureFrameState, func() bool) error { return nil }, func() {})
			require.NoError(t, err)
			cs.mu.Lock()
			cs.beginIngestRecoveryEpisodeLocked()
			cs.ingestRecoveryAttempts = 2
			cs.ingestRecoveryProgressBaseline = 99 // Earlier loss receipt, distinct from the fixture's current zero receipts.
			cs.armIngestRecoveryLocked(time.Hour)
			oldCtx, oldTimer := cs.ingestRecoveryCtx, cs.ingestRecoveryTimer
			oldEvent := cs.videoHealthEventLocked(VideoHealthLost, 2, "original failure")
			cs.mu.Unlock()
			if explicit {
				require.True(t, cs.RecaptureFrame(frame))
			} else {
				require.True(t, cs.RecaptureFrameContext(oldCtx, frame))
			}
			cs.mu.Lock()
			currentCtx, currentTimer := cs.ingestRecoveryCtx, cs.ingestRecoveryTimer
			baseline, attempts, gaveUp := cs.ingestRecoveryProgressBaseline, cs.ingestRecoveryAttempts, cs.ingestRecoveryGaveUp
			cs.mu.Unlock()
			if explicit {
				require.ErrorIs(t, oldCtx.Err(), context.Canceled, "explicit request left an old retry authorized")
				require.NotNil(t, currentCtx, "explicit request lost bounded failure monitoring")
				require.NoError(t, currentCtx.Err())
				require.NotEqual(t, oldCtx, currentCtx)
				require.NotNil(t, currentTimer)
				require.NotSame(t, oldTimer, currentTimer)
				require.Zero(t, baseline)
			} else {
				require.NoError(t, oldCtx.Err())
				require.Equal(t, oldCtx, currentCtx)
				require.Same(t, oldTimer, currentTimer)
				require.Equal(t, uint64(99), baseline)
			}
			require.Equal(t, 2, attempts)
			require.False(t, gaveUp)
			currentEvent, ok := cs.CurrentVideoHealthEvent()
			require.True(t, ok)
			require.Equal(t, oldEvent, currentEvent, "explicit recapture invented recovery or reset failure accounting")
		})
	}
}

package browser

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestViewportNewTargetSkipsImpossibleNoopMeasurement(t *testing.T) {
	f := newViewportFrameFixture(t)
	// Select another real manager target while the capture still belongs to A.
	_, err := f.mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	next, target, err := f.mgr.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	require.NotEqual(t, f.before.TargetID, string(target))
	f.measuredW, f.measuredH, f.measuredScale = 800, 600, 1
	f.lv.mu.Lock()
	f.lv.lastKnownActiveCtx = next
	f.lv.pendingViewportTarget = next
	f.lv.lastRequestedW, f.lv.lastRequestedH, f.lv.lastRequestedScale = 800, 600, 1
	f.lv.mu.Unlock()
	original := f.lv.runCDP
	firstAction := ""
	f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		for _, a := range actions {
			switch a.(type) {
			case viewportFrameGeometryAction:
				if firstAction == "" {
					firstAction = "geometry"
				}
				if f.bounds == 0 {
					return errors.New("new target preflight must not run")
				}
			case windowBoundsAction:
				if firstAction == "" {
					firstAction = "bounds"
				}
			}
		}
		return original(ctx, timeout, actions...)
	}
	f.atWrite = func() {
		state := f.cs.FrameState()
		require.False(t, state.Ready)
		require.Equal(t, string(target), state.TargetID)
		require.Greater(t, state.Generation, f.before.Generation)
		require.False(t, f.cs.AcceptsInputGeneration(f.before.CaptureID, f.before.Generation))
	}
	applied, err := f.lv.applyViewportContextWithConvergence(context.Background(), next, 800, 600, 1, true)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "bounds", firstAction)
	require.Equal(t, 1, f.bounds)
	require.Len(t, f.commands, 1)
	require.Equal(t, string(target), f.commands[0].TargetID)
	require.Equal(t, 800, f.commands[0].Width)
	require.Equal(t, 600, f.commands[0].Height)
}

func TestViewportSameTargetNoopMeasurementFailureDoesNotResize(t *testing.T) {
	f := newViewportFrameFixture(t)
	fault := errors.New("same target geometry unavailable")
	f.measureErr = fault
	_, err := f.lv.applyViewportContextWithConvergence(context.Background(), f.lv.tabCtx, 800, 600, 1, false)
	require.ErrorIs(t, err, fault)
	require.Zero(t, f.bounds)
	require.Empty(t, f.commands)
	require.Equal(t, f.before, f.cs.FrameState())
}

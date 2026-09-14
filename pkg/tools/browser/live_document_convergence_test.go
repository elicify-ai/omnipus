package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/stretchr/testify/require"
)

func TestLiveDocumentCannotBypassScheduledViewportConvergence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		f.lv.lastRequestedW, f.lv.lastRequestedH, f.lv.lastRequestedScale = 1426, 718, 2
		// A coalesced worker has not entered browser admission yet. The fence
		// must already exist when document events for its new target arrive.
		f.lv.viewportReapplyInFlight = true
		require.True(t, f.lv.reapplyViewportToNewTarget(f.lv.tabCtx))
		close(f.paint)
		f.commit("new-page")
		synctest.Wait()
		require.Empty(t, f.commands, "document paint captured a new target before its pending viewport converged")
		require.False(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentCannotBypassFailedViewportConvergenceAndRecoversFreshMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		f.lv.lastRequestedW, f.lv.lastRequestedH, f.lv.lastRequestedScale = 1426, 718, 2
		require.True(t, f.lv.reapplyViewportToNewTarget(f.lv.tabCtx))
		require.Eventually(t, func() bool {
			f.lv.mu.Lock()
			defer f.lv.mu.Unlock()
			return !f.lv.viewportReapplyInFlight
		}, 5*time.Second, 10*time.Millisecond)
		require.Empty(t, f.commands)
		close(f.paint)
		f.commit("first-page")
		synctest.Wait()
		require.Empty(t, f.commands, "document paint bypassed failed new-target convergence")
		require.False(t, f.cs.FrameState().Ready)
		f.measuredH = 718
		// A new navigation must announce its loader before commit. Sending a
		// different loader's commit alone is correctly fenced as a late event.
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "corrected-page"})
		f.commit("corrected-page")
		synctest.Wait()
		require.Len(t, f.commands, 1, "fresh matching document geometry must recover")
		require.Equal(t, 718, f.commands[0].Height)
	})
}

func TestLiveDocumentManualResizeClearsConvergenceWithoutChangingClampPolicy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		f.lv.lastRequestedW, f.lv.lastRequestedH, f.lv.lastRequestedScale = 1426, 718, 2
		require.True(t, f.lv.reapplyViewportToNewTarget(f.lv.tabCtx))
		require.Eventually(t, func() bool {
			f.lv.mu.Lock()
			defer f.lv.mu.Unlock()
			return !f.lv.viewportReapplyInFlight
		}, 5*time.Second, 10*time.Millisecond)
		require.Empty(t, f.commands)
		_, err := f.reg.SetViewportContext(context.Background(), testSessionID, 1426, 718, 2)
		require.NoError(t, err, "manual resize retains existing measured-clamp behavior")
		require.Len(t, f.commands, 1)
		close(f.paint)
		f.commit("after-manual-resize")
		synctest.Wait()
		require.Len(t, f.commands, 2, "successful manual resize left the old convergence fence behind")
		require.Equal(t, 575, f.commands[1].Height)
	})
}

func TestViewportRefreshCannotBypassPendingTargetConvergence(t *testing.T) {
	for _, geometry := range []struct {
		name          string
		width, height int
	}{
		{"changed geometry", 1426, 575},
		{"unchanged geometry", 800, 600},
	} {
		t.Run(geometry.name, func(t *testing.T) {
			f := newViewportFrameFixture(t)
			f.measuredW, f.measuredH, f.measuredScale = geometry.width, geometry.height, 1
			f.lv.lastRequestedW, f.lv.lastRequestedH, f.lv.lastRequestedScale = 1426, 718, 1
			f.lv.viewportReapplyInFlight = true
			require.True(t, f.lv.reapplyViewportToNewTarget(f.lv.tabCtx))
			err := f.reg.RefreshCaptureFrameContext(context.Background(), testSessionID, f.cs)
			require.ErrorContains(t, err, "still settling")
			require.Empty(t, f.commands, "refresh bypassed the pending target viewport")
			f.measuredW, f.measuredH = 1426, 718
			require.NoError(t, f.reg.RefreshCaptureFrameContext(context.Background(), testSessionID, f.cs))
			require.Len(t, f.commands, 1)
			require.Equal(t, 718, f.commands[0].Height)
		})
	}
}

func TestPrepareEncoderFrameCannotBypassPendingTargetConvergence(t *testing.T) {
	f := newViewportFrameFixture(t)
	f.lv.lastRequestedW, f.lv.lastRequestedH, f.lv.lastRequestedScale = 1426, 718, 1
	f.lv.viewportReapplyInFlight = true
	require.True(t, f.lv.reapplyViewportToNewTarget(f.lv.tabCtx))
	_, err := f.cs.prepareEncoderFrame(context.Background(), func(context.Context) (int, int, float64, error) { return 1426, 575, 1, nil })
	require.ErrorContains(t, err, "still settling")
	require.Equal(t, f.before, f.cs.FrameState(), "encoder preparation published unconverged geometry")
	frame, err := f.cs.prepareEncoderFrame(context.Background(), func(context.Context) (int, int, float64, error) { return 1426, 718, 1, nil })
	require.NoError(t, err)
	require.Equal(t, 718, frame.Height)
	require.Greater(t, frame.Generation, f.before.Generation)
}

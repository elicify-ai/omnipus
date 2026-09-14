package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestNewTargetViewportConvergesBeforeRecapture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newViewportFrameFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		contentsCalls := 0
		original := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			for _, action := range actions {
				if _, ok := action.(windowContentsSizeAction); ok {
					require.Empty(t, f.commands, "temporary geometry reached capture before convergence")
					contentsCalls++
					f.measuredH = 718
				}
			}
			return original(ctx, timeout, actions...)
		}
		f.lv.reapplyViewportPass(f.lv.tabCtx, 1426, 718, 2)
		require.Equal(t, 2, f.bounds, "outer bounds must not be repeated")
		require.Equal(t, 1, contentsCalls, "one contents correction must follow the initial compensated attempt")
		require.Len(t, f.commands, 1)
		require.Equal(t, 1426, f.commands[0].Width)
		require.Equal(t, 718, f.commands[0].Height)
		require.False(t, f.cs.FrameState().Ready, "measured geometry is not yet presented media")
	})
}

func TestNewTargetViewportPersistentMismatchNeverRecaptures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newViewportFrameFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		contentsCalls := 0
		original := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			for _, action := range actions {
				if _, ok := action.(windowContentsSizeAction); ok {
					contentsCalls++
				}
			}
			return original(ctx, timeout, actions...)
		}
		f.lv.reapplyViewportPass(f.lv.tabCtx, 1426, 718, 2)
		require.Equal(t, 1, contentsCalls, "convergence must stop after one contents correction")
		require.Equal(t, 2, f.bounds, "outer bounds must not be repeated")
		require.Empty(t, f.commands, "unconverged target geometry was captured")
		require.False(t, f.cs.FrameState().Ready)
	})
}

func TestNewTargetViewportCancellationStopsConvergence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newViewportFrameFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		caller, cancel := context.WithCancel(context.Background())
		defer cancel()
		original := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			err := original(ctx, timeout, actions...)
			for _, action := range actions {
				if _, ok := action.(viewportFrameGeometryAction); ok {
					cancel()
				}
			}
			return err
		}
		applied, err := f.lv.applyViewportContextWithConvergence(caller, f.lv.tabCtx, 1426, 718, 2, true)
		require.True(t, applied, "initial bounds acknowledgement must survive later cancellation")
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 2, f.bounds, "canceled target must not receive another bounds write")
		require.Empty(t, f.commands)
	})
}

func TestNewTargetViewportSupersededStopsConvergence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newViewportFrameFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		next, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		original := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			err := original(ctx, timeout, actions...)
			for _, action := range actions {
				if _, ok := action.(viewportFrameGeometryAction); ok {
					f.mgr.mu.Lock()
					entry := f.mgr.sessions[testSessionID]
					entry.tabs = append(entry.tabs, &tabEntry{ctx: next, cancel: cancel, targetID: "replacement"})
					entry.activeIdx = len(entry.tabs) - 1
					f.mgr.mu.Unlock()
				}
			}
			return err
		}
		f.lv.reapplyViewportPass(f.lv.tabCtx, 1426, 718, 2)
		require.Equal(t, 2, f.bounds, "superseded target must not receive another bounds write")
		require.Empty(t, f.commands)
	})
}

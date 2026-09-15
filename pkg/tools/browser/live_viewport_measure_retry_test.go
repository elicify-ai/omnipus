package browser

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestViewportFinalMeasurementRetriesOnlyLiveDeadline(t *testing.T) {
	for _, mode := range []string{"recover", "second deadline", "nondeadline", "caller canceled", "overall deadline", "target replaced", "capture replaced"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newViewportFrameFixture(t)
				caller, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				original := f.lv.runCDP
				fault := errors.New("geometry rejected")
				f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
					for _, a := range actions {
						if _, ok := a.(viewportFrameGeometryAction); ok {
							calls++
							if calls == 1 {
								switch mode {
								case "nondeadline":
									return fault
								case "caller canceled":
									cancel()
									return context.DeadlineExceeded
								case "overall deadline":
									<-ctx.Done()
									return ctx.Err()
								case "target replaced":
									// Publish a different active identity while the mocked CDP call is
									// suspended; production's post-read identity fence must reject it.
									f.mgr.mu.Lock()
									f.mgr.sessions[testSessionID].active().targetID = "replacement"
									f.mgr.mu.Unlock()
								case "capture replaced":
									f.mgr.mu.Lock()
									delete(f.mgr.captures, testSessionID)
									f.mgr.mu.Unlock()
								}
								return context.DeadlineExceeded
							}
							if mode == "second deadline" {
								return context.DeadlineExceeded
							}
						}
					}
					return original(ctx, timeout, actions...)
				}
				_, err := f.lv.applyViewportContextWithConvergence(caller, f.lv.tabCtx, 1000, 700, 1.25, false)
				require.Equal(t, 1, f.bounds, "measurement retry must never resize again")
				switch mode {
				case "recover":
					require.NoError(t, err)
					require.Equal(t, 2, calls)
					require.Len(t, f.commands, 1)
					require.Equal(t, 1000, f.commands[0].Width)
					require.Equal(t, 700, f.commands[0].Height)
				case "second deadline":
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.ErrorContains(t, err, "viewport final geometry")
					require.Equal(t, 2, calls)
				case "nondeadline":
					require.ErrorIs(t, err, fault)
					require.Equal(t, 1, calls)
				case "caller canceled":
					require.ErrorIs(t, err, context.Canceled)
					require.Equal(t, 1, calls)
				case "overall deadline":
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Equal(t, 1, calls)
				default:
					require.Error(t, err)
					require.Equal(t, 1, calls, "changed identity must not start another CDP read")
				}
				if mode != "recover" {
					require.Empty(t, f.commands)
					require.False(t, f.cs.FrameState().Ready)
				}
			})
		})
	}
}

func TestViewportFinalMeasurementSharesRetryAcrossConvergencePasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newViewportFrameFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		reads, corrections := 0, 0
		original := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			for _, action := range actions {
				switch a := action.(type) {
				case viewportFrameGeometryAction:
					reads++
					// The first pass spends the sole read retry, then reports a real
					// undersized viewport. A later pass must not gain another retry.
					if reads == 1 || reads == 3 {
						return context.DeadlineExceeded
					}
				case viewportContentGeometryAction:
					*a.width, *a.height = f.measuredW, f.measuredH
					*a.clientWidth, *a.clientHeight = f.measuredW, f.measuredH
				case windowContentsSizeAction:
					corrections++
					f.measuredH = 718
				}
			}
			return original(ctx, timeout, actions...)
		}
		applied, err := f.lv.applyViewportContextWithConvergence(context.Background(), f.lv.tabCtx, 1426, 718, 2, true)
		require.True(t, applied)
		require.ErrorIs(t, err, context.DeadlineExceeded, "second pass must retain the exhausted measurement retry budget")
		require.ErrorContains(t, err, "viewport final geometry")
		require.Equal(t, 3, reads, "at most one additional read across the whole operation")
		require.Equal(t, 2, f.bounds, "only the initial bounds and Chrome-delta compensation are allowed")
		require.Equal(t, 1, corrections, "the measured shortfall must exercise the second convergence pass")
		require.Empty(t, f.commands, "a fourth successful read must never authorize capture")
		require.False(t, f.cs.FrameState().Ready)
	})
}

package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestNewTargetViewportUsesContentsSizeWhenOuterBoundsDoNotConverge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newViewportFrameFixture(t)
		f.measuredW, f.measuredH, f.measuredScale = 1426, 575, 2
		calls := 0
		executor := liveInputExecutor(func(ctx context.Context, method string, params, result any) error {
			switch method {
			case "Browser.getWindowForTarget":
				fixtureValue[*browser.GetWindowForTargetReturns](result).WindowID = 42
			case "Browser.setContentsSize":
				got, ok := params.(*browser.SetContentsSizeParams)
				require.True(t, ok, "contents-size parameters have unexpected type %T", params)
				require.Equal(t, browser.WindowID(42), got.WindowID)
				require.Equal(t, int64(1426), got.Width)
				require.Equal(t, int64(718), got.Height)
				require.Empty(t, f.commands, "capture was authorized before resizing actual content")
				calls++
				f.measuredH = 718
			default:
				t.Fatalf("unexpected browser command %s", method)
			}
			return nil
		})
		previous := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			for _, action := range actions {
				switch a := action.(type) {
				case viewportContentGeometryAction:
					*a.width, *a.height = f.measuredW, f.measuredH
					*a.clientWidth, *a.clientHeight = f.measuredW, f.measuredH
					continue
				case windowBoundsAction, layoutMetricsAction, viewportFrameGeometryAction:
					if err := previous(ctx, timeout, action); err != nil {
						return err
					}
					continue
				}
				if isScaleAction(action) {
					continue
				}
				if err := action.Do(cdp.WithExecutor(ctx, executor)); err != nil {
					return err
				}
			}
			return nil
		}
		f.lv.reapplyViewportPass(f.lv.tabCtx, 1426, 718, 2)
		require.Equal(t, 1, calls, "outer bounds retry must be replaced with actual content sizing")
		require.Equal(t, 2, f.bounds, "do not repeat the failed outer-window sizing sequence")
		require.Len(t, f.commands, 1)
		require.Equal(t, 718, f.commands[0].Height)
	})
}

func TestViewportRequestUsesFreshInnerDimensionsForScrollbar(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		innerW, innerH, clientW, clientH int
		want                             bool
	}{
		{"measured scrollbar", 1426, 718, 1411, 718, true},
		{"real width shortfall", 1411, 718, 1411, 718, false},
		{"real height shortfall", 1426, 575, 1411, 575, false},
		{"stale CSS measurement", 1426, 718, 1400, 718, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newViewportFrameFixture(t)
			f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
				for _, action := range actions {
					a, ok := action.(viewportContentGeometryAction)
					require.True(t, ok, "postcondition must freshly measure inner and CSS geometry")
					*a.width, *a.height = tc.innerW, tc.innerH
					*a.clientWidth, *a.clientHeight = tc.clientW, tc.clientH
				}
				return nil
			}
			got, err := f.lv.viewportMatchesRequest(context.Background(), CaptureFrameState{TargetID: f.before.TargetID, Width: 1411, Height: 718}, 1426, 718)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
